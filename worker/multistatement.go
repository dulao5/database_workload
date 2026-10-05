package worker

import (
	"context"
	"log"
	"strings"

	"github.com/dulao5/tidb-multistmt"
)

// runSessionMultiStatement renders one whole transaction (every
// template/repeat's EXECUTE) into a single multistmt.Batch and sends it as
// one round trip, instead of one round trip per statement. This is the
// multi_statements=true counterpart to runSession.
//
// Per-statement PREPARE reuse across sessions is handled by w.preparedCache
// (see multistmt.PreparedCache), not by this worker tracking SQL-shape names
// itself: PreparedCache is keyed by the underlying physical connection, so
// it stays correct even for connection_type "short", where acquireConn hands
// back a brand new *sql.Conn Go object every session — a worker-local "have
// I seen this SQL before" map would wrongly skip PREPARE for a session that
// landed on a different physical connection than the one that map was built
// against.
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
		}
	}()

	// txVars holds "transaction"-scoped session variables, same as
	// runSession: fresh for every transaction, so e.g. a random id picked
	// by the first statement can be reused by later statements in the same
	// transaction.
	txVars := make(map[string]interface{})

	batch, rerr := w.buildMultiStatementBatch(txVars)
	if rerr != nil {
		log.Printf("Worker %d: ERROR %v", w.id, rerr)
		sessionFailed = true
		return
	}
	if batch == nil {
		// Every template in this session was local-only (no SQL to run).
		return
	}

	if err := batch.Execute(ctx, conn, multistmt.WithPreparedCache(w.preparedCache)); err != nil {
		log.Printf("Worker %d: ERROR multi-statement batch failed: %v", w.id, err)
		sessionFailed = true
		return
	}
}

// buildMultiStatementBatch renders every template/repeat in one
// session/transaction into a multistmt.Batch: "begin", each
// template/repeat's SQL (bound args, not literal substitution — multistmt
// itself renders those as a PREPARE/SET/EXECUTE sequence), then "commit".
// Returns a nil Batch if every template in this session was local-only (no
// SQL to run).
func (w *Worker) buildMultiStatementBatch(txVars map[string]interface{}) (*multistmt.Batch, error) {
	type renderedStmt struct {
		sql          string
		args         []interface{}
		hasResultSet bool
	}
	var stmts []renderedStmt

	for i, tmpl := range w.templates {
		repeatTimes := tmpl.GetRepeat()
		for r := 0; r < repeatTimes; r++ {
			args := make([]interface{}, len(tmpl.Params))
			for j := range tmpl.Params {
				v, err := w.resolveArg(&tmpl.Params[j], w.generators[i][j], txVars)
				if err != nil {
					return nil, err
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
			isSelect := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(renderedSQL)), "SELECT")

			stmts = append(stmts, renderedStmt{sql: renderedSQL, args: finalArgs, hasResultSet: isSelect})
		}
	}

	if len(stmts) == 0 {
		return nil, nil
	}

	b := multistmt.New()
	b.Add("begin", nil, false, nil)
	for _, s := range stmts {
		b.Add(s.sql, s.args, s.hasResultSet, nil)
	}
	b.Add("commit", nil, false, nil)
	return b, nil
}
