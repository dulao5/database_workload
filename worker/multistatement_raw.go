package worker

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/dulao5/tidb-multistmt"
)

// runSessionMultiStatementRaw is multi_statements_mode "raw": every
// statement's args are substituted directly into the SQL text as literals
// (no PREPARE/EXECUTE at all, no bind params), and the whole transaction is
// sent as one semicolon-joined COM_QUERY. This is the baseline multi_statements
// has no prepared-statement machinery to compare multi_statements_mode
// "prepared_cache" (tidb-multistmt's PreparedCache) against.
func (w *Worker) runSessionMultiStatementRaw(ctx context.Context) {
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
			w.dropLongConn()
		}
	}()

	// txVars holds "transaction"-scoped session variables, same as
	// runSession/runSessionMultiStatement: fresh for every transaction.
	txVars := make(map[string]interface{})

	batchSQL, hasStatement, rerr := w.buildMultiStatementRawBatch(txVars)
	if rerr != nil {
		log.Printf("Worker %d: ERROR %v", w.id, rerr)
		sessionFailed = true
		return
	}
	if !hasStatement {
		// Every template in this session was local-only (no SQL to run).
		return
	}

	rows, err := conn.QueryContext(ctx, batchSQL)
	if err != nil {
		log.Printf("Worker %d: ERROR multi-statement (raw) batch failed: %v", w.id, err)
		sessionFailed = true
		return
	}
	defer rows.Close()
	for {
		for rows.Next() {
		}
		if err := rows.Err(); err != nil {
			log.Printf("Worker %d: ERROR multi-statement (raw) batch failed: %v", w.id, err)
			sessionFailed = true
			return
		}
		if !rows.NextResultSet() {
			break
		}
	}
}

// buildMultiStatementRawBatch renders every template/repeat in one
// session/transaction into a single "begin; ...; commit;" string, with every
// arg substituted directly into the SQL text as a literal — no PREPARE, no
// bind params, no persistent per-connection state.
func (w *Worker) buildMultiStatementRawBatch(txVars map[string]interface{}) (string, bool, error) {
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
					return "", false, err
				}
				args[j] = v
			}

			if strings.TrimSpace(tmpl.SQL) == "" {
				// Local-only template: generates/saves session variables,
				// nothing to send to the DB.
				continue
			}

			literalSQL, bindArgs := splitLiteralAndBindArgs(tmpl.SQL, tmpl.Params, args)
			renderedSQL, finalArgs, eerr := multistmt.ExpandIn(literalSQL, bindArgs)
			if eerr != nil {
				return "", false, fmt.Errorf("statement %d: %w", i, eerr)
			}

			inlinedSQL, err := inlineLiterals(renderedSQL, finalArgs)
			if err != nil {
				return "", false, fmt.Errorf("statement %d: %w", i, err)
			}

			body.WriteString(inlinedSQL)
			body.WriteString(";")
			hasStatement = true
		}
	}

	if !hasStatement {
		return "", false, nil
	}

	body.WriteString("commit;")
	return body.String(), true, nil
}

// inlineLiterals replaces each remaining "?" placeholder in sqlText, in
// order, with args' literal SQL text.
func inlineLiterals(sqlText string, args []interface{}) (string, error) {
	sqlParts := strings.Split(sqlText, "?")
	if len(sqlParts)-1 != len(args) {
		return "", fmt.Errorf("placeholder count (%d) doesn't match arg count (%d) in %q", len(sqlParts)-1, len(args), sqlText)
	}

	var b strings.Builder
	for i, arg := range args {
		b.WriteString(sqlParts[i])
		lit, err := rawSQLValueLiteral(arg)
		if err != nil {
			return "", err
		}
		b.WriteString(lit)
	}
	b.WriteString(sqlParts[len(sqlParts)-1])
	return b.String(), nil
}

// rawSQLStringLiteral quotes s as a single-quoted SQL string literal.
func rawSQLStringLiteral(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}

// rawSQLValueLiteral formats a generated arg as a SQL literal, mirroring the
// set of types this tool's generators actually produce.
func rawSQLValueLiteral(v interface{}) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case string:
		return rawSQLStringLiteral(x), nil
	case int:
		return strconv.Itoa(x), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case time.Time:
		return rawSQLStringLiteral(x.Format("2006-01-02 15:04:05")), nil
	default:
		return "", fmt.Errorf("unsupported arg type %T", v)
	}
}
