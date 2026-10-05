package worker

import (
	"context"
	"database/sql"
	"database_workload/config"
	"database_workload/generator"

	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/dulao5/tidb-multistmt"
	_ "github.com/go-sql-driver/mysql"
)

// Worker executes workloads.
type Worker struct {
	id              int
	dbConnStr       string
	templates       []config.Template
	generators      [][]generator.Generator
	useTX           bool
	usePrepared     bool
	multiStatements bool
	// multiStatementsRaw selects multi-statement mode's "raw" rendering
	// (literal args inlined into SQL text, no PREPARE/EXECUTE at all)
	// instead of the default PreparedCache-backed one. See
	// config.Config.MultiStatementsMode.
	multiStatementsRaw bool
	isShortConn        bool
	rate               int
	db                 *sql.DB

	// preparedCache gives multi-statement mode the same per-connection
	// PREPARE reuse stmtCache gives the binary-protocol path, but keyed by
	// the underlying physical connection (via tidb-multistmt's
	// PreparedCache) rather than by this Worker object — needed because
	// connection_type "short" hands out a fresh *sql.Conn from the pool
	// every session, and a prepared statement is only valid on the
	// specific physical connection it was PREPAREd on.
	preparedCache *multistmt.PreparedCache

	// longConn is the one persistent *sql.Conn reused across every
	// session/transaction when connection_type isn't "short". Prepared
	// statements are only valid for the specific *sql.Conn they were
	// prepared on, so this worker must keep reusing the same Conn object
	// (not just the same pooled DB) for stmtCache to actually save any
	// COM_STMT_PREPARE round trips across transactions.
	longConn *sql.Conn

	// connVars holds "connection"-scoped session variables: generated once
	// (the first time their defining param runs) and reused for the rest of
	// this worker's lifetime, e.g. picking one random table per connection
	// the way sysbench's oltp-read-write does.
	connVars map[string]interface{}

	// stmtCache holds prepared statements keyed by their fully-rendered SQL
	// text (after literal substitutions and array expansion), so a
	// template is only ever prepared once per underlying connection and
	// reused (via tx.StmtContext) for as long as that connection lives.
	stmtCache map[string]*sql.Stmt
}

// New creates a new Worker.
func New(id int, cfg *config.Config) (*Worker, error) {
	gens := make([][]generator.Generator, len(cfg.Templates))
	for i, tmpl := range cfg.Templates {
		gens[i] = make([]generator.Generator, len(tmpl.Params))
		for j, param := range tmpl.Params {
			if param.Type == "ref" {
				// A "ref" param doesn't generate anything itself; it looks
				// up a value saved by an earlier param at runtime.
				continue
			}
			// Make a copy of the param to avoid issues with pointers
			p := param
			g, err := generator.New(&p)
			if err != nil {
				return nil, err
			}
			gens[i][j] = g
		}
	}

	var db *sql.DB
	var err error

	dbConnStr := cfg.DBConnStr
	if cfg.MultiStatements {
		// Required client capability flag for the driver to send a
		// semicolon-separated batch as a single request instead of
		// rejecting or splitting it.
		if strings.Contains(dbConnStr, "?") {
			dbConnStr += "&multiStatements=true"
		} else {
			dbConnStr += "?multiStatements=true"
		}
	}

	if cfg.ConnectionType == "short" {
		// Short-lived connections: force tcp-reuse and no idle connections.
		dsn := strings.Replace(dbConnStr, "tcp(", "tcp-reuse(", 1)
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			log.Printf("Worker %d: ERROR failed to open DB connection: %v", id, err)
			return nil, err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(0)
	} else {
		// Default to long-lived connections with a pool of 1.
		db, err = sql.Open("mysql", dbConnStr)
		if err != nil {
			log.Printf("Worker %d: ERROR failed to open DB connection: %v", id, err)
			return nil, err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(5 * time.Minute)
	}

	if cfg.MultiStatements && (!cfg.UseTransaction || !cfg.UsePreparedStatements) {
		return nil, fmt.Errorf("multi_statements requires use_transaction and use_prepared_statements to both be true")
	}

	multiStatementsRaw := false
	switch cfg.MultiStatementsMode {
	case "", "prepared_cache":
		// default
	case "raw":
		multiStatementsRaw = true
	default:
		return nil, fmt.Errorf("multi_statements_mode must be %q or %q, got %q", "prepared_cache", "raw", cfg.MultiStatementsMode)
	}

	return &Worker{
		id:                 id,
		dbConnStr:          cfg.DBConnStr,
		templates:          cfg.Templates,
		generators:         gens,
		useTX:              cfg.UseTransaction,
		usePrepared:        cfg.UsePreparedStatements,
		multiStatements:    cfg.MultiStatements,
		multiStatementsRaw: multiStatementsRaw,
		isShortConn:        cfg.ConnectionType == "short",
		rate:               cfg.RatePerThread,
		db:                 db,
		connVars:           make(map[string]interface{}),
		stmtCache:          make(map[string]*sql.Stmt),
		preparedCache:      multistmt.NewPreparedCache(0, 0),
	}, nil
}

// getOrPrepareStmt returns a cached prepared statement for the given SQL
// text, preparing it (once, on the given conn) on first use. Callers must
// pass the exact *sql.Conn the statement will be run against: a prepared
// statement is only valid on the connection it was prepared on.
func (w *Worker) getOrPrepareStmt(ctx context.Context, conn *sql.Conn, sqlText string) (*sql.Stmt, error) {
	if stmt, ok := w.stmtCache[sqlText]; ok {
		return stmt, nil
	}
	stmt, err := conn.PrepareContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	w.stmtCache[sqlText] = stmt
	return stmt, nil
}

// splitLiteralAndBindArgs rewrites sqlText's "?" placeholders: params marked
// Literal are substituted directly into the SQL text (needed for
// identifier-position values like a table name suffix, which MySQL cannot
// bind as a query parameter), while the rest stay as "?" and are returned as
// bind args in order.
func splitLiteralAndBindArgs(sqlText string, params []config.Param, args []interface{}) (string, []interface{}) {
	sqlParts := strings.Split(sqlText, "?")
	if len(sqlParts)-1 != len(args) {
		return sqlText, args
	}

	var b strings.Builder
	bindArgs := make([]interface{}, 0, len(args))
	for i, arg := range args {
		b.WriteString(sqlParts[i])
		if params[i].IsLiteral() {
			b.WriteString(fmt.Sprintf("%v", arg))
		} else {
			b.WriteString("?")
			bindArgs = append(bindArgs, arg)
		}
	}
	b.WriteString(sqlParts[len(sqlParts)-1])
	return b.String(), bindArgs
}

// resolveArg produces the value to bind for a single param: either a fresh
// (or reused, for "ref") value looked up from session variables, or a
// freshly generated one — saving it into the right scope's variable store
// if the param has SaveAs set.
func (w *Worker) resolveArg(p *config.Param, gen generator.Generator, txVars map[string]interface{}) (interface{}, error) {
	var val interface{}

	if p.Type == "ref" {
		name := ""
		if p.RefName != nil {
			name = *p.RefName
		}
		v, ok := txVars[name]
		if !ok {
			v, ok = w.connVars[name]
		}
		if !ok {
			return nil, fmt.Errorf("session variable %q referenced before it was saved", name)
		}
		val = v
	} else {
		val = gen.Generate()

		if p.SaveAs != nil {
			name := *p.SaveAs
			if p.ScopeOrDefault() == "connection" {
				if existing, ok := w.connVars[name]; ok {
					// Already generated earlier in this worker's lifetime;
					// reuse it instead of the value just generated above.
					val = existing
				} else {
					w.connVars[name] = val
				}
			} else {
				txVars[name] = val
			}
		}
	}

	if p.Offset != nil {
		n, ok := val.(int64)
		if !ok {
			return nil, fmt.Errorf("offset is only supported for int64 values, got %T", val)
		}
		val = n + *p.Offset
	}

	return val, nil
}

// Run starts the worker's loop. It stops when the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	wait := time.Millisecond * time.Duration(100*rand.Float64())
	if w.rate > 0 {
		wait = time.Second / time.Duration(w.rate)
		wait = time.Duration(float64(wait) * (rand.Float64()))
	}
	time.Sleep(wait)
	var rateLimiter *time.Ticker
	rateExplain := "no limit"
	if w.rate > 0 {
		rateLimiter = time.NewTicker(time.Second / time.Duration(w.rate))
		defer rateLimiter.Stop()
		rateExplain = fmt.Sprintf("%d TPS", w.rate)
	}

	log.Printf("Worker %d started, rate: %s", w.id, rateExplain)
	for {
		select {
		case <-ctx.Done():
			log.Printf("Worker %d stopping", w.id)
			return
		default:
			w.runSession(ctx)
			if rateLimiter != nil {
				<-rateLimiter.C
			}
		}
	}
}

func (w *Worker) runSession(ctx context.Context) {
	if w.multiStatements {
		if w.multiStatementsRaw {
			w.runSessionMultiStatementRaw(ctx)
		} else {
			w.runSessionMultiStatement(ctx)
		}
		return
	}

	conn, ownConn, err := w.acquireConn(ctx)
	if err != nil {
		log.Printf("Worker %d: ERROR failed to get DB connection: %v", w.id, err)
		return
	}
	if ownConn {
		defer conn.Close()
	}

	// If anything below fails, drop cached state tied to this connection:
	// a broken connection also invalidates any prepared statements and
	// (if this session owns a long-lived conn) the conn itself, so the
	// next session starts clean instead of reusing something stale.
	sessionFailed := false
	defer func() {
		if sessionFailed && !w.isShortConn {
			w.longConn = nil
			w.stmtCache = make(map[string]*sql.Stmt)
		}
	}()

	var tx *sql.Tx
	if w.useTX {
		tx, err = conn.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("Worker %d: ERROR failed to begin transaction: %v", w.id, err)
			sessionFailed = true
			return
		}
	}

	// txVars holds "transaction"-scoped session variables: fresh for every
	// session/transaction, so e.g. a random id picked by the first
	// statement can be reused by later update/delete statements in the
	// same transaction.
	txVars := make(map[string]interface{})

	for i, tmpl := range w.templates {
		repeatTimes := tmpl.GetRepeat()
		for r := 0; r < repeatTimes; r++ {

			args := make([]interface{}, len(tmpl.Params))
			for j := range tmpl.Params {
				v, rerr := w.resolveArg(&tmpl.Params[j], w.generators[i][j], txVars)
				if rerr != nil {
					log.Printf("Worker %d: ERROR %v", w.id, rerr)
					if w.useTX {
						_ = tx.Rollback()
					}
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

			if w.usePrepared {
				var stmt *sql.Stmt
				stmt, err = w.getOrPrepareStmt(ctx, conn, finalSQL)
				if err == nil {
					runStmt := stmt
					if w.useTX {
						runStmt = tx.StmtContext(ctx, stmt)
					}
					if isSelect {
						var rows *sql.Rows
						rows, err = runStmt.QueryContext(ctx, finalArgs...)
						if err == nil {
							for rows.Next() {
							}
							err = rows.Err()
							rows.Close()
						}
					} else {
						_, err = runStmt.ExecContext(ctx, finalArgs...)
					}
				}
			} else if isSelect {
				var rows *sql.Rows
				if w.useTX {
					rows, err = tx.QueryContext(ctx, finalSQL, finalArgs...)
				} else {
					rows, err = conn.QueryContext(ctx, finalSQL, finalArgs...)
				}

				if err == nil {
					// read all result data to fix "connection reset by peer" error
					for rows.Next() {
					}
					err = rows.Err()
					rows.Close()
				}
			} else {
				if w.useTX {
					_, err = tx.ExecContext(ctx, finalSQL, finalArgs...)
				} else {
					_, err = conn.ExecContext(ctx, finalSQL, finalArgs...)
				}
			}
			if err != nil {
				log.Printf("Worker %d: ERROR failed to execute query or iterate rows: %v", w.id, err)
				if w.useTX {
					_ = tx.Rollback()
				}
				sessionFailed = true
				return
			}
		}
	}

	if w.useTX {
		if err := tx.Commit(); err != nil {
			log.Printf("Worker %d: ERROR failed to commit transaction: %v", w.id, err)
			sessionFailed = true
		}
	}
}

// acquireConn returns the *sql.Conn to use for this session, and whether
// this call is responsible for closing it once the session is done.
//
// For connection_type "short" it always opens (and later closes) a fresh
// connection, matching that mode's purpose. Otherwise it lazily acquires
// one persistent *sql.Conn and keeps reusing it across every session, since
// prepared statements are only valid on the specific *sql.Conn they were
// prepared on.
func (w *Worker) acquireConn(ctx context.Context) (*sql.Conn, bool, error) {
	if w.isShortConn {
		conn, err := w.db.Conn(ctx)
		return conn, true, err
	}
	if w.longConn == nil {
		conn, err := w.db.Conn(ctx)
		if err != nil {
			return nil, false, err
		}
		w.longConn = conn
	}
	return w.longConn, false, nil
}

func handleArrayParams(sql string, args []interface{}) (string, []interface{}) {
	finalSQL := ""
	sqlParts := strings.Split(sql, "?")

	if len(sqlParts)-1 != len(args) {
		return sql, args
	}

	newArgs := make([]interface{}, 0, len(args))
	for i, arg := range args {
		finalSQL += sqlParts[i]
		arr, ok := arg.([]interface{})
		if ok {
			if len(arr) == 0 {
				// Handle empty array case, maybe return an error or a specific SQL syntax
				// For now, we just add a single NULL placeholder to avoid syntax errors.
				finalSQL += "?"
				newArgs = append(newArgs, nil)
				continue
			}
			placeholders := strings.Repeat("?,", len(arr))
			placeholders = strings.TrimSuffix(placeholders, ",")
			finalSQL += placeholders
			newArgs = append(newArgs, arr...)
		} else {
			finalSQL += "?"
			newArgs = append(newArgs, arg)
		}
	}
	finalSQL += sqlParts[len(sqlParts)-1]

	// fmt.Println(finalSQL, newArgs) // Debug print
	return finalSQL, newArgs
}
