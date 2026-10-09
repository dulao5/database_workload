// Package worker's pipelined_binary.go drives binarymultistmt
// (github.com/dulao5/tidb-binary-multistmt) to render one transaction
// exactly like runSessionMultiStatement does (reusing
// buildMultiStatementBatch — same templates, same bound args), but over
// pipelined binary COM_STMT_EXECUTE instead of tidb-multistmt's
// single-COM_QUERY-blob text protocol. See that package's README for why
// this avoids the per-statement SET markers measured elsewhere to dominate
// multi_statements mode's extra CPU, and for the semantic gap it accepts
// (a mid-pipeline failure doesn't stop already-written EXECUTEs from
// running — mitigated by requiring pessimistic transactions).
//
// This file used to hand-roll the wire protocol directly against a
// captured net.Conn; that code has moved into binarymultistmt itself (same
// technique: a custom mysql.RegisterDialContext dial hook captures the
// real net.Conn alongside the driver's normal handshake/auth, then the
// *sql.Conn/*sql.DB pair is kept open only to hold the pool slot). This
// file is now just the worker-level glue: acquire a Conn from the pool,
// translate this workload's *multistmt.Batch into a *binarymultistmt.Batch,
// Execute, and decide COMMIT-vs-ROLLBACK bookkeeping (none needed — Execute
// itself commits on full success and leaves a failed batch open for an
// explicit Rollback).
package worker

import (
	"context"
	"errors"
	"log"

	"github.com/dulao5/tidb-binary-multistmt"
)

// acquireRawConn lazily acquires (dialing+authenticating on first use) a
// hijacked connection from w.rawDB and hands it back for reuse across
// sessions for as long as the worker lives — mirroring longConn's "one
// persistent connection" semantics, since prepared statements (and
// Execute's commit/rollback bookkeeping) are only valid on the specific
// connection they ran on. w.rawDB is capped at maxConns=1 (see New), so
// this worker only ever owns one physical connection at a time.
func (w *Worker) acquireRawConn(ctx context.Context) (*binarymultistmt.Conn, error) {
	if w.rawConn != nil {
		return w.rawConn, nil
	}
	conn, err := w.rawDB.AcquireConn(ctx)
	if err != nil {
		return nil, err
	}
	w.rawConn = conn
	return conn, nil
}

// dropRawConn discards the raw connection after any connection/protocol-
// level failure (Conn.Close destroys rather than re-idles it once
// Execute/Rollback has marked it broken — see binarymultistmt's doc
// comments), so the next session acquires a fresh one.
func (w *Worker) dropRawConn() {
	if w.rawConn != nil {
		w.rawConn.Close()
		w.rawConn = nil
	}
}

func (w *Worker) runSessionPipelinedBinary(ctx context.Context) {
	conn, err := w.acquireRawConn(ctx)
	if err != nil {
		log.Printf("Worker %d: ERROR pipelined-binary: acquire raw conn: %v", w.id, err)
		w.dropRawConn()
		return
	}

	txVars := make(map[string]interface{})
	batch, rerr := w.buildMultiStatementBatch(txVars)
	if rerr != nil {
		log.Printf("Worker %d: ERROR %v", w.id, rerr)
		return
	}
	if batch == nil {
		return
	}

	all := batch.Statements()
	if len(all) < 2 {
		log.Printf("Worker %d: ERROR pipelined-binary: expected begin+statements+commit, got %d", w.id, len(all))
		return
	}
	real := all[1 : len(all)-1] // drop the synthetic "begin"/"commit" buildMultiStatementBatch adds

	b := binarymultistmt.NewBatch()
	for i, s := range real {
		idx, sqlText := i, s.SQL
		b.Add(sqlText, s.Args, func(sr *binarymultistmt.StatementResult) {
			if sr.Err != nil {
				log.Printf("Worker %d: ERROR pipelined-binary: statement #%d (%s) failed: %v", w.id, idx, sqlText, sr.Err)
			}
			// Rows (if any) are left unconsumed on purpose — this workload
			// never needs the data back, and binarymultistmt auto-drains
			// whatever a Callback doesn't read, so skipping it here is both
			// correct and the cheaper path.
		})
	}

	res, err := conn.Execute(ctx, b)
	var commitErr *binarymultistmt.CommitError
	switch {
	case errors.As(err, &commitErr):
		// Every statement succeeded, but COMMIT itself was rejected (e.g. a
		// write conflict). TiDB already rolled back server-side — conn is
		// still healthy and reusable, and there's nothing to roll back, so
		// don't dropRawConn here.
		log.Printf("Worker %d: WARN pipelined-binary: commit rejected (already rolled back server-side): %v", w.id, commitErr)
	case err != nil:
		log.Printf("Worker %d: ERROR pipelined-binary: %v", w.id, err)
		w.dropRawConn()
	case !res.AllSucceeded:
		if err := conn.Rollback(ctx); err != nil {
			log.Printf("Worker %d: ERROR pipelined-binary: ROLLBACK failed: %v", w.id, err)
			w.dropRawConn()
		}
	}
}
