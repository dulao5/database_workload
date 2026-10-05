package worker

import (
	"context"
	"database/sql"
	"log"
	"strings"
)

// runSessionFixedPrepared is the non-multi, use_transaction+use_prepared_statements
// path with config.Config.FixPreparedStatementReuse set: it gets the same
// "prepare once, execute many" behavior multi_statements already gets from
// PreparedCache, by avoiding (*sql.Tx).StmtContext entirely.
//
// The default non-multi path (runSession, below) wraps a cached *sql.Stmt
// (from getOrPrepareStmt, obtained via (*sql.Conn).PrepareContext) in
// tx.StmtContext(ctx, stmt) for every statement of every transaction. That
// hits an edge case documented in database/sql's own source: a *sql.Stmt
// whose cg field is already set (which a Conn-level PrepareContext always
// does) makes Tx.StmtContext re-PREPARE unconditionally — "we ignore this
// edge case and re-prepare the statement in this case. No need to add
// code-complexity for this." — and the resulting transaction-scoped Stmt has
// no parentStmt, so committing the transaction really DEALLOCATEs
// (COM_STMT_CLOSE) it. Net effect: stmtCache's caching is silently
// defeated whenever useTX is true, which is every config this tool ships
// with — every transaction PREPAREs and CLOSEs every statement fresh.
//
// The fix: never call tx.StmtContext. BEGIN/COMMIT/ROLLBACK are sent as
// plain text directly on conn (MySQL transactions are session-scoped, not
// tied to Go's *sql.Tx object), and the cached *sql.Stmt — which is already
// pinned to this exact physical connection via its cg field — is called
// directly via its own QueryContext/ExecContext. No *sql.Tx is created at
// all, so the re-PREPARE/auto-CLOSE edge case never triggers.
func (w *Worker) runSessionFixedPrepared(ctx context.Context) {
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
			w.stmtCache = make(map[string]*sql.Stmt)
		}
	}()

	if w.useTX {
		if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
			log.Printf("Worker %d: ERROR failed to begin transaction: %v", w.id, err)
			sessionFailed = true
			return
		}
	}

	rollback := func() {
		if w.useTX {
			if _, rerr := conn.ExecContext(ctx, "ROLLBACK"); rerr != nil {
				log.Printf("Worker %d: ERROR failed to rollback: %v", w.id, rerr)
			}
		}
	}

	// txVars holds "transaction"-scoped session variables, same as
	// runSession: fresh for every session/transaction.
	txVars := make(map[string]interface{})

	for i, tmpl := range w.templates {
		repeatTimes := tmpl.GetRepeat()
		for r := 0; r < repeatTimes; r++ {
			args := make([]interface{}, len(tmpl.Params))
			for j := range tmpl.Params {
				v, rerr := w.resolveArg(&tmpl.Params[j], w.generators[i][j], txVars)
				if rerr != nil {
					log.Printf("Worker %d: ERROR %v", w.id, rerr)
					rollback()
					sessionFailed = true
					return
				}
				args[j] = v
			}

			if strings.TrimSpace(tmpl.SQL) == "" {
				// Local-only template: it exists purely to generate/save
				// session variables, there's nothing to send to the DB.
				continue
			}

			literalSQL, bindArgs := splitLiteralAndBindArgs(tmpl.SQL, tmpl.Params, args)
			finalSQL, finalArgs := handleArrayParams(literalSQL, bindArgs)
			isSelect := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(finalSQL)), "SELECT")

			stmt, serr := w.getOrPrepareStmt(ctx, conn, finalSQL)
			if serr != nil {
				err = serr
			} else if isSelect {
				var rows *sql.Rows
				rows, err = stmt.QueryContext(ctx, finalArgs...)
				if err == nil {
					for rows.Next() {
					}
					err = rows.Err()
					rows.Close()
				}
			} else {
				_, err = stmt.ExecContext(ctx, finalArgs...)
			}

			if err != nil {
				log.Printf("Worker %d: ERROR failed to execute query or iterate rows: %v", w.id, err)
				rollback()
				sessionFailed = true
				return
			}
		}
	}

	if w.useTX {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			log.Printf("Worker %d: ERROR failed to commit transaction: %v", w.id, err)
			sessionFailed = true
		}
	}
}
