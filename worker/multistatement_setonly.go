package worker

import (
	"context"
	"log"
)

// runSessionMultiStatementSetOnly is multi_statements_mode "set_only": it
// renders one whole transaction into a *multistmt.Batch exactly the way
// runSessionMultiStatement does (same templates, same bound args, same
// begin/real-statements/commit shape), but sends it through
// Batch.ExecuteSetOnly instead of Batch.Execute — only the batch's
// SET-marker sequence reaches the server, so this generates the same SET
// volume/shape as production multi-statement traffic at matching throughput,
// without touching any table. A throwaway A/B comparison baseline, not a
// real workload: no PREPARE/EXECUTE/begin/commit/real query is ever sent, so
// there is no transaction to roll back on failure.
func (w *Worker) runSessionMultiStatementSetOnly(ctx context.Context) {
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

	txVars := make(map[string]interface{})

	batch, rerr := w.buildMultiStatementBatch(txVars)
	if rerr != nil {
		log.Printf("Worker %d: ERROR %v", w.id, rerr)
		sessionFailed = true
		return
	}
	if batch == nil {
		return
	}

	if err := batch.ExecuteSetOnly(ctx, conn); err != nil {
		log.Printf("Worker %d: ERROR set-only batch failed: %v", w.id, err)
		sessionFailed = true
	}
}
