package worker

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// runSessionMultiStatement renders one whole transaction (every
// template/repeat's PREPARE+EXECUTE) into a single multi-statement SQL
// string and sends it as one round trip, instead of one round trip per
// statement. This is the multi_statements=true counterpart to runSession.
func (w *Worker) runSessionMultiStatement(ctx context.Context) {
	conn, ownConn, err := w.acquireConn(ctx)
	if err != nil {
		log.Printf("Worker %d: ERROR failed to get DB connection: %v", w.id, err)
		return
	}
	if ownConn {
		defer conn.Close()
	}

	sessionFailed := false
	defer func() {
		if sessionFailed && !w.isShortConn {
			w.longConn = nil
			w.multiStmtNames = make(map[string]string)
		}
	}()

	// txVars holds "transaction"-scoped session variables, same as
	// runSession: fresh for every transaction, so e.g. a random id picked
	// by the first statement can be reused by later statements in the
	// same transaction.
	txVars := make(map[string]interface{})

	batch, rerr := w.buildMultiStatementBatch(txVars)
	if rerr != nil {
		log.Printf("Worker %d: ERROR %v", w.id, rerr)
		sessionFailed = true
		return
	}
	if batch == "" {
		// Every template in this session was local-only (no SQL to run).
		return
	}

	rows, err := conn.QueryContext(ctx, batch)
	if err != nil {
		log.Printf("Worker %d: ERROR multi-statement batch failed: %v", w.id, err)
		sessionFailed = true
		return
	}
	defer rows.Close()
	for {
		for rows.Next() {
		}
		if err := rows.Err(); err != nil {
			log.Printf("Worker %d: ERROR multi-statement batch failed: %v", w.id, err)
			sessionFailed = true
			return
		}
		if !rows.NextResultSet() {
			break
		}
	}
}

// buildMultiStatementBatch renders every template/repeat in one
// session/transaction into a single "begin; ...; commit;" string. Any SQL
// shape not yet PREPAREd on this connection gets a "PREPARE ... FROM ...;"
// prefixed onto the same batch, so even the very first transaction that
// needs it is still sent as a single round trip.
func (w *Worker) buildMultiStatementBatch(txVars map[string]interface{}) (string, error) {
	var prepares strings.Builder
	var body strings.Builder
	body.WriteString("begin;")
	hasStatement := false

	for i, tmpl := range w.templates {
		repeatTimes := tmpl.GetRepeat()
		for r := 0; r < repeatTimes; r++ {
			args := make([]interface{}, len(tmpl.Params))
			for j := range tmpl.Params {
				v, err := w.resolveArg(&tmpl.Params[j], w.generators[i][j], txVars)
				if err != nil {
					return "", err
				}
				args[j] = v
			}

			if strings.TrimSpace(tmpl.SQL) == "" {
				// Local-only template: generates/saves session variables,
				// nothing to send to the DB.
				continue
			}

			literalSQL, bindArgs := splitLiteralAndBindArgs(tmpl.SQL, tmpl.Params, args)
			renderedSQL, finalArgs := handleArrayParams(literalSQL, bindArgs)

			stmtName, isNew := w.getOrRegisterMultiStmtName(renderedSQL)
			if isNew {
				quoted, err := sqlStringLiteral(renderedSQL)
				if err != nil {
					return "", err
				}
				prepares.WriteString(fmt.Sprintf("PREPARE %s FROM %s;", stmtName, quoted))
			}

			varNames := make([]string, len(finalArgs))
			if len(finalArgs) > 0 {
				var setParts []string
				for k, a := range finalArgs {
					vn := fmt.Sprintf("@mv_%s_%d", stmtName, k)
					lit, err := sqlValueLiteral(a)
					if err != nil {
						return "", err
					}
					setParts = append(setParts, fmt.Sprintf("%s=%s", vn, lit))
					varNames[k] = vn
				}
				body.WriteString("SET ")
				body.WriteString(strings.Join(setParts, ", "))
				body.WriteString(";")
			}

			body.WriteString("EXECUTE ")
			body.WriteString(stmtName)
			if len(varNames) > 0 {
				body.WriteString(" USING ")
				body.WriteString(strings.Join(varNames, ", "))
			}
			body.WriteString(";")
			hasStatement = true
		}
	}

	if !hasStatement {
		return "", nil
	}

	body.WriteString("commit;")
	return prepares.String() + body.String(), nil
}

// getOrRegisterMultiStmtName returns the SQL-level PREPARE name for a
// rendered SQL shape, registering a new one (and reporting isNew=true) the
// first time this shape is seen on this worker/connection.
func (w *Worker) getOrRegisterMultiStmtName(renderedSQL string) (name string, isNew bool) {
	if name, ok := w.multiStmtNames[renderedSQL]; ok {
		return name, false
	}
	w.multiStmtNext++
	name = fmt.Sprintf("dw_ps_%d", w.multiStmtNext)
	w.multiStmtNames[renderedSQL] = name
	return name, true
}

// sqlStringLiteral quotes s as a single-quoted SQL string literal, for use
// as the argument to "PREPARE name FROM '...'".
func sqlStringLiteral(s string) (string, error) {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'", nil
}

// sqlValueLiteral formats a generated arg as a SQL literal suitable for a
// "SET @v = <literal>" assignment. Only the value types this tool's
// generators actually produce need to be supported.
func sqlValueLiteral(v interface{}) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case string:
		s, _ := sqlStringLiteral(x)
		return s, nil
	case int:
		return fmt.Sprintf("%d", x), nil
	case int32:
		return fmt.Sprintf("%d", x), nil
	case int64:
		return fmt.Sprintf("%d", x), nil
	case uint:
		return fmt.Sprintf("%d", x), nil
	case uint64:
		return fmt.Sprintf("%d", x), nil
	case float32:
		return fmt.Sprintf("%v", x), nil
	case float64:
		return fmt.Sprintf("%v", x), nil
	case time.Time:
		s, _ := sqlStringLiteral(x.Format("2006-01-02 15:04:05"))
		return s, nil
	default:
		return "", fmt.Errorf("unsupported value type %T for multi-statement SET variable", v)
	}
}
