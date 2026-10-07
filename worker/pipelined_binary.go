// Package worker's pipelined_binary.go is a throwaway experiment, not
// production code: it bypasses go-sql-driver/mysql entirely for a
// transaction's real statements and hand-writes binary MySQL protocol
// packets directly on the underlying net.Conn.
//
// Why: go-sql-driver's (and database/sql's) call convention is
// write-command-then-synchronously-read-its-full-response, repeated once per
// statement — so a transaction with N real statements always pays N network
// round trips. multi_statements (text protocol, tidb-multistmt) avoids that
// by packing everything into one COM_QUERY blob, at the cost of the
// per-statement SET markers measured elsewhere to dominate its extra CPU.
// This mode tries a third approach: binary COM_STMT_EXECUTE, pipelined —
// write all N EXECUTE packets back-to-back without waiting for any response,
// then read all N responses afterward. MySQL command packets are
// self-delimited (length-prefixed), so nothing in the wire protocol or
// TiDB's connection read loop requires a round trip between commands; the
// reason this isn't normally possible is that no mainstream client
// (including go-sql-driver/mysql) exposes an API to write ahead of reading.
//
// How the net.Conn is obtained without forking the driver: a custom dial
// function registered via the driver's own public mysql.RegisterDialContext
// hook (same mechanism dialer_base.go already uses for socket options) dials
// the real TCP connection and also pushes it into a channel this package
// reads from. go-sql-driver does its normal handshake/auth over that
// connection; once db.Conn(ctx) returns successfully, authentication is
// done and this file takes over all reads/writes directly, outside
// database/sql. From that point the *sql.Conn/*sql.DB pair is kept open
// only to hold the pool slot — they must never be used through the driver
// API again, because this file's raw writes permanently desync the driver's
// internal per-connection sequence-number bookkeeping.
//
// Semantic gap this accepts: an EXECUTE packet is an independent command to
// the server, so a mid-pipeline failure does not stop already-written
// EXECUTEs from running (unlike multi_statements' single-blob semantics,
// where the server itself halts on first error). This is mitigated, not
// eliminated: only pessimistic transactions are supported (row locks are
// held as each statement runs, not deferred to commit), BEGIN is sent before
// the pipelined batch, and COMMIT/ROLLBACK is decided only after every
// response in the batch has been read and inspected — so a failure anywhere
// in the batch still rolls back the whole transaction, at the cost of
// wasted work on the statements that ran after the failure.
package worker

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
)

const (
	comQuery       = 0x03
	comStmtPrepare = 0x16
	comStmtExecute = 0x17

	fieldLongLong = 0x08
	fieldNULL     = 0x06
	fieldString   = 0xFE
)

// rawPreparedStmt is what ensurePrepared caches per (connection, SQL text).
type rawPreparedStmt struct {
	id           uint32
	paramCount   int
	hasResultSet bool
}

// sqlErrResponse distinguishes a server-reported SQL-level failure (ERR
// packet for one statement — the pipeline keeps draining the rest) from a
// connection/protocol-level failure (abort immediately, drop the raw conn).
type sqlErrResponse struct{ msg string }

func (e *sqlErrResponse) Error() string { return e.msg }

var rawCaptureChannels sync.Map // workerID(int) -> chan net.Conn

func rawDialNetworkName(workerID int) string {
	return fmt.Sprintf("pbraw%d", workerID)
}

// registerRawDialer installs this worker's net.Conn-capturing dial function
// under a worker-unique network name, so concurrent workers' captures can
// never race on a shared channel.
func registerRawDialer(workerID int) {
	ch := make(chan net.Conn, 1)
	rawCaptureChannels.Store(workerID, ch)
	mysql.RegisterDialContext(rawDialNetworkName(workerID), func(ctx context.Context, addr string) (net.Conn, error) {
		d := net.Dialer{}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		ch <- c
		return c, nil
	})
}

func rawCaptureChannel(workerID int) chan net.Conn {
	v, _ := rawCaptureChannels.Load(workerID)
	return v.(chan net.Conn)
}

// acquireRawConn lazily dials+authenticates via the driver (through the
// captured-net.Conn dial hook) and hands back the same physical connection
// for direct raw use, reusing it across sessions for as long as the worker
// lives — mirroring longConn's "one persistent connection" semantics, since
// prepared statements (and this mode's commit/rollback bookkeeping) are only
// valid on the specific connection they ran on.
func (w *Worker) acquireRawConn(ctx context.Context) (net.Conn, error) {
	if w.rawConn != nil {
		return w.rawConn, nil
	}

	netName := rawDialNetworkName(w.id)
	dsn := strings.Replace(w.dbConnStr, "tcp(", netName+"(", 1)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	pc, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}

	var nc net.Conn
	select {
	case nc = <-rawCaptureChannel(w.id):
	case <-ctx.Done():
		db.Close()
		return nil, ctx.Err()
	}
	// The driver's handshake sets an absolute SetReadDeadline/SetWriteDeadline
	// (per the DSN's readTimeout/writeTimeout) on this same net.Conn; left in
	// place, it expires on its own schedule regardless of when this file
	// actually reads next, causing a spurious i/o timeout. Clear it — this
	// raw path manages its own read/write timing from here on.
	nc.SetDeadline(time.Time{})

	w.rawDB = db
	w.rawPoolConn = pc
	w.rawConn = nc
	w.rawStmtCache = make(map[string]rawPreparedStmt)
	return nc, nil
}

// dropRawConn discards the raw connection (and everything tied to it —
// cached prepared statement IDs are only valid on that specific
// connection) after any protocol-level failure, so the next session dials
// a fresh one.
func (w *Worker) dropRawConn() {
	if w.rawConn != nil {
		w.rawConn.Close()
		w.rawConn = nil
	}
	if w.rawDB != nil {
		w.rawDB.Close()
		w.rawDB = nil
	}
	w.rawPoolConn = nil
	w.rawStmtCache = nil
}

func putUint24(b []byte, n int) {
	b[0] = byte(n)
	b[1] = byte(n >> 8)
	b[2] = byte(n >> 16)
}

// writePacket writes one MySQL packet. seq is always 0 here: every call site
// in this file starts a brand new command (never a >16MB multi-packet
// command), and each new command resets the sequence number.
func writePacket(w io.Writer, payload []byte) error {
	hdr := make([]byte, 4+len(payload))
	putUint24(hdr[:3], len(payload))
	hdr[3] = 0
	copy(hdr[4:], payload)
	_, err := w.Write(hdr)
	return err
}

func readPacket(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func errPacketText(p []byte) string {
	if len(p) < 9 {
		return fmt.Sprintf("short ERR packet: % x", p)
	}
	code := binary.LittleEndian.Uint16(p[1:3])
	msg := string(p[9:])
	return fmt.Sprintf("code=%d msg=%s", code, msg)
}

// readLenEncInt decodes a MySQL length-encoded integer from the start of b.
func readLenEncInt(b []byte) uint64 {
	if len(b) == 0 {
		return 0
	}
	switch {
	case b[0] < 0xfb:
		return uint64(b[0])
	case b[0] == 0xfc && len(b) >= 3:
		return uint64(b[1]) | uint64(b[2])<<8
	case b[0] == 0xfd && len(b) >= 4:
		return uint64(b[1]) | uint64(b[2])<<8 | uint64(b[3])<<16
	case b[0] == 0xfe && len(b) >= 9:
		return binary.LittleEndian.Uint64(b[1:9])
	}
	return 0
}

func writeComQuery(conn net.Conn, text string) error {
	return writePacket(conn, append([]byte{comQuery}, text...))
}

func readOKorErr(conn net.Conn) error {
	resp, err := readPacket(conn)
	if err != nil {
		return err
	}
	if resp[0] == 0xFF {
		return fmt.Errorf("ERR: %s", errPacketText(resp))
	}
	if resp[0] != 0x00 {
		return fmt.Errorf("expected OK, got first byte 0x%02x", resp[0])
	}
	return nil
}

// prepareRaw sends COM_STMT_PREPARE and drains the param-definition/
// column-definition/EOF packets that follow the OK, for a statement with any
// number of params and (unlike the earlier DO-?-only toy version) any number
// of result columns.
func prepareRaw(conn net.Conn, query string) (rawPreparedStmt, error) {
	if err := writePacket(conn, append([]byte{comStmtPrepare}, query...)); err != nil {
		return rawPreparedStmt{}, err
	}
	resp, err := readPacket(conn)
	if err != nil {
		return rawPreparedStmt{}, err
	}
	if resp[0] == 0xFF {
		return rawPreparedStmt{}, fmt.Errorf("prepare %q failed: %s", query, errPacketText(resp))
	}
	if resp[0] != 0x00 {
		return rawPreparedStmt{}, fmt.Errorf("prepare %q: unexpected first byte 0x%02x", query, resp[0])
	}
	id := binary.LittleEndian.Uint32(resp[1:5])
	columnCount := int(binary.LittleEndian.Uint16(resp[5:7]))
	paramCount := int(binary.LittleEndian.Uint16(resp[7:9]))

	for i := 0; i < paramCount; i++ {
		if _, err := readPacket(conn); err != nil {
			return rawPreparedStmt{}, err
		}
	}
	if paramCount > 0 {
		if _, err := readPacket(conn); err != nil { // EOF after param defs
			return rawPreparedStmt{}, err
		}
	}
	for i := 0; i < columnCount; i++ {
		if _, err := readPacket(conn); err != nil {
			return rawPreparedStmt{}, err
		}
	}
	if columnCount > 0 {
		if _, err := readPacket(conn); err != nil { // EOF after column defs
			return rawPreparedStmt{}, err
		}
	}
	return rawPreparedStmt{id: id, paramCount: paramCount}, nil
}

func (w *Worker) ensurePrepared(conn net.Conn, sqlText string, hasResultSet bool) (rawPreparedStmt, error) {
	if ps, ok := w.rawStmtCache[sqlText]; ok {
		return ps, nil
	}
	ps, err := prepareRaw(conn, sqlText)
	if err != nil {
		return rawPreparedStmt{}, err
	}
	ps.hasResultSet = hasResultSet
	w.rawStmtCache[sqlText] = ps
	return ps, nil
}

// buildExecutePayload encodes a COM_STMT_EXECUTE for stmtID, binding args in
// order. Supports exactly the Go types this workload's generators/resolveArg
// produce: int64 (number params), string (random_string params), and nil.
func buildExecutePayload(stmtID uint32, args []any) ([]byte, error) {
	head := make([]byte, 0, 9)
	var b4 [4]byte
	head = append(head, comStmtExecute)
	binary.LittleEndian.PutUint32(b4[:], stmtID)
	head = append(head, b4[:]...)
	head = append(head, 0x00) // flags: CURSOR_TYPE_NO_CURSOR
	binary.LittleEndian.PutUint32(b4[:], 1)
	head = append(head, b4[:]...) // iteration_count = 1

	if len(args) == 0 {
		return head, nil
	}

	nullMask := make([]byte, (len(args)+7)/8)
	types := make([]byte, 0, len(args)*2)
	values := make([]byte, 0, 64)

	for i, a := range args {
		switch v := a.(type) {
		case int64:
			types = append(types, fieldLongLong, 0x00)
			var b8 [8]byte
			binary.LittleEndian.PutUint64(b8[:], uint64(v))
			values = append(values, b8[:]...)
		case int:
			types = append(types, fieldLongLong, 0x00)
			var b8 [8]byte
			binary.LittleEndian.PutUint64(b8[:], uint64(int64(v)))
			values = append(values, b8[:]...)
		case string:
			if len(v) >= 251 {
				return nil, fmt.Errorf("arg %d: string length %d too long for this dirty client's 1-byte length-encoding", i, len(v))
			}
			types = append(types, fieldString, 0x00)
			values = append(values, byte(len(v)))
			values = append(values, v...)
		case nil:
			nullMask[i/8] |= 1 << uint(i%8)
			types = append(types, fieldNULL, 0x00)
		default:
			return nil, fmt.Errorf("arg %d: unsupported type %T for this dirty client", i, a)
		}
	}

	payload := make([]byte, 0, len(head)+len(nullMask)+1+len(types)+len(values))
	payload = append(payload, head...)
	payload = append(payload, nullMask...)
	payload = append(payload, 0x01) // new_params_bound_flag
	payload = append(payload, types...)
	payload = append(payload, values...)
	return payload, nil
}

// drainExecuteResponse reads exactly one EXECUTE's response off the wire —
// a single OK/ERR packet for a non-row-returning statement, or a binary
// result set (column-count/column-defs/EOF, then rows until a terminal
// EOF) for one that returns rows. Row *values* are not decoded, only
// skipped: packets are self-delimited by their length header, so correctly
// advancing past them (to stay in sync for the next statement's response)
// never requires understanding their binary-encoded contents.
func drainExecuteResponse(conn net.Conn, hasResultSet bool) error {
	resp, err := readPacket(conn)
	if err != nil {
		return err
	}
	if resp[0] == 0xFF {
		return &sqlErrResponse{msg: errPacketText(resp)}
	}
	if !hasResultSet {
		if resp[0] != 0x00 {
			return fmt.Errorf("expected OK, got first byte 0x%02x", resp[0])
		}
		return nil
	}

	columnCount := readLenEncInt(resp)
	for i := uint64(0); i < columnCount; i++ {
		if _, err := readPacket(conn); err != nil {
			return err
		}
	}
	if columnCount > 0 {
		if _, err := readPacket(conn); err != nil { // EOF after column defs
			return err
		}
	}
	for {
		row, err := readPacket(conn)
		if err != nil {
			return err
		}
		if row[0] == 0xFF {
			return &sqlErrResponse{msg: errPacketText(row)}
		}
		if row[0] == 0xFE && len(row) < 9 {
			return nil // terminal EOF
		}
		// else: a binary row packet (leading 0x00) — discard, keep reading.
	}
}

// runSessionPipelinedBinary renders one transaction exactly like
// runSessionMultiStatement does (reusing buildMultiStatementBatch — same
// templates, same bound args), but instead of a tidb-multistmt text-protocol
// batch, sends BEGIN, then all real statements' EXECUTE packets pipelined,
// then COMMIT or ROLLBACK depending on whether every statement in the batch
// succeeded. See this file's package doc comment for the semantic gap this
// accepts.
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

	stmtIDs := make([]uint32, len(real))
	for i, s := range real {
		ps, err := w.ensurePrepared(conn, s.SQL, s.HasResultSet)
		if err != nil {
			log.Printf("Worker %d: ERROR pipelined-binary: prepare %q: %v", w.id, s.SQL, err)
			w.dropRawConn()
			return
		}
		stmtIDs[i] = ps.id
	}

	if err := writeComQuery(conn, "BEGIN"); err != nil {
		log.Printf("Worker %d: ERROR pipelined-binary: write BEGIN: %v", w.id, err)
		w.dropRawConn()
		return
	}
	if err := readOKorErr(conn); err != nil {
		log.Printf("Worker %d: ERROR pipelined-binary: BEGIN failed: %v", w.id, err)
		w.dropRawConn()
		return
	}

	for i, s := range real {
		payload, err := buildExecutePayload(stmtIDs[i], s.Args)
		if err != nil {
			log.Printf("Worker %d: ERROR pipelined-binary: encode exec #%d (%s): %v", w.id, i, s.SQL, err)
			w.dropRawConn()
			return
		}
		if err := writePacket(conn, payload); err != nil {
			log.Printf("Worker %d: ERROR pipelined-binary: write exec #%d: %v", w.id, i, err)
			w.dropRawConn()
			return
		}
	}

	sawFailure := false
	for i, s := range real {
		if err := drainExecuteResponse(conn, s.HasResultSet); err != nil {
			var sqlErr *sqlErrResponse
			if errors.As(err, &sqlErr) {
				log.Printf("Worker %d: ERROR pipelined-binary: statement #%d (%s) failed: %v", w.id, i, s.SQL, err)
				sawFailure = true
				continue
			}
			log.Printf("Worker %d: ERROR pipelined-binary: reading response #%d: %v", w.id, i, err)
			w.dropRawConn()
			return
		}
	}

	final := "COMMIT"
	if sawFailure {
		final = "ROLLBACK"
	}
	if err := writeComQuery(conn, final); err != nil {
		log.Printf("Worker %d: ERROR pipelined-binary: write %s: %v", w.id, final, err)
		w.dropRawConn()
		return
	}
	if err := readOKorErr(conn); err != nil {
		log.Printf("Worker %d: ERROR pipelined-binary: %s failed: %v", w.id, final, err)
		w.dropRawConn()
		return
	}
}
