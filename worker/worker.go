package worker

import (
	"context"
	"database/sql"
	"database_workload/config"
	"database_workload/generator"

	"fmt"
	"log"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Worker executes workloads.
type Worker struct {
	id         int
	dbConnStr  string
	templates  []config.Template
	generators [][]generator.Generator
	useTX      bool
	rate       int
	db         *sql.DB
}

// New creates a new Worker.
func New(id int, cfg *config.Config) (*Worker, error) {
	gens := make([][]generator.Generator, len(cfg.Templates))
	for i, tmpl := range cfg.Templates {
		gens[i] = make([]generator.Generator, len(tmpl.Params))
		for j, param := range tmpl.Params {
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

	if cfg.ConnectionType == "short" {
		// Short-lived connections: force tcp-reuse and no idle connections.
		dsn := strings.Replace(cfg.DBConnStr, "tcp(", "tcp-reuse(", 1)
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			log.Printf("Worker %d: ERROR failed to open DB connection: %v", id, err)
			return nil, err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(0)
	} else {
		// Default to long-lived connections with a pool of 1.
		db, err = sql.Open("mysql", cfg.DBConnStr)
		if err != nil {
			log.Printf("Worker %d: ERROR failed to open DB connection: %v", id, err)
			return nil, err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(5 * time.Minute)
	}

	return &Worker{
		id:         id,
		dbConnStr:  cfg.DBConnStr,
		templates:  cfg.Templates,
		generators: gens,
		useTX:      cfg.UseTransaction,
		rate:       cfg.RatePerThread,
		db:         db,
	}, nil
}

// Run starts the worker's loop. It stops when the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
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
	conn, err := w.db.Conn(ctx)
	if err != nil {
		log.Printf("Worker %d: ERROR failed to get DB connection: %v", w.id, err)
		return
	}
	defer conn.Close()

	var tx *sql.Tx
	if w.useTX {
		tx, err = conn.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("Worker %d: ERROR failed to begin transaction: %v", w.id, err)
			return
		}
	}

	for i, tmpl := range w.templates {
		args := make([]interface{}, len(tmpl.Params))
		for j, gen := range w.generators[i] {
			args[j] = gen.Generate()
		}

		finalSQL, finalArgs := handleArrayParams(tmpl.SQL, args)

		isSelect := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(finalSQL)), "SELECT")

		if isSelect {
			var rows *sql.Rows
			if w.useTX {
				rows, err = tx.QueryContext(ctx, finalSQL, finalArgs...)
			} else {
				rows, err = conn.QueryContext(ctx, finalSQL, finalArgs...)
			}

			if err == nil {
				// read all result data for "connection reset by peer"
				for rows.Next() {
				}
				err = rows.Err() // 检查遍历过程中是否出错
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
			return
		}
	}

	if w.useTX {
		if err := tx.Commit(); err != nil {
			log.Printf("Worker %d: ERROR failed to commit transaction: %v", w.id, err)
		}
	}
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
