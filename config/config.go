package config

import (
	"encoding/json"
	"os"
)

// Config is the main configuration structure
type Config struct {
	Concurrency           int    `json:"concurrency"`
	RatePerThread         int    `json:"rate_per_thread"`
	DBConnStr             string `json:"db_conn_str"`
	ConnectionType        string `json:"connection_type,omitempty"`
	UseTransaction        bool   `json:"use_transaction"`
	UsePreparedStatements bool   `json:"use_prepared_statements,omitempty"`
	// MultiStatements, when true, sends one whole transaction (every
	// template/repeat's statement) as a single multi-statement round trip
	// instead of one round trip per statement. Requires use_transaction and
	// use_prepared_statements to both be true.
	MultiStatements bool `json:"multi_statements,omitempty"`
	// MultiStatementsMode selects how a multi-statement batch is rendered,
	// only meaningful together with multi_statements:
	//   - "prepared_cache" (default): every statement goes through
	//     PREPARE/EXECUTE, and a statement already PREPAREd earlier on this
	//     connection is reused instead of PREPAREd again (tidb-multistmt's
	//     PreparedCache).
	//   - "raw": no PREPARE/EXECUTE at all — every statement's args are
	//     substituted directly into the SQL text as literals, and the whole
	//     transaction is sent as one semicolon-joined COM_QUERY.
	//   - "set_only": every template/repeat is still rendered with real bound
	//     args, same as "prepared_cache", but only the resulting
	//     SET @_multistmt_statement_num=... marker sequence is sent — no
	//     PREPARE/EXECUTE/begin/commit/real query reaches the server at all
	//     (tidb-multistmt's BuildSetOnlySQL/ExecuteSetOnly). A throwaway A/B
	//     baseline for isolating the SET-dispatch cost at matching
	//     throughput, not a real workload: no table is read or written.
	MultiStatementsMode string `json:"multi_statements_mode,omitempty"`
	// FixPreparedStatementReuse, when true (only meaningful for the non-multi,
	// use_transaction+use_prepared_statements path), avoids a database/sql
	// stdlib edge case that silently defeats stmtCache's "prepare once,
	// execute many" intent: wrapping a *sql.Stmt obtained from
	// (*sql.Conn).PrepareContext in (*sql.Tx).StmtContext always re-PREPAREs
	// (see database/sql's own comment on stmt.cg != nil in Tx.StmtContext)
	// and really DEALLOCATEs on commit, because that Stmt's cg field is the
	// *sql.Conn, not nil. With this on, BEGIN/COMMIT are sent as plain text
	// on the same connection (no *sql.Tx at all) and the cached *sql.Stmt is
	// called directly, so a statement is PREPAREd once and EXECUTEd many
	// times for as long as the connection lives, same as the multi_statements
	// path already gets via PreparedCache.
	FixPreparedStatementReuse bool       `json:"fix_prepared_statement_reuse,omitempty"`
	Templates                 []Template `json:"templates"`
}

// Template represents a single SQL query template
type Template struct {
	SQL    string  `json:"sql"`
	Params []Param `json:"params"`
	Repeat int     `json:"repeat,omitempty"`
}

func (t *Template) GetRepeat() int {
	if t.Repeat <= 0 {
		return 1
	}
	return t.Repeat
}

// Param represents a parameter for a SQL query
type Param struct {
	Type       string `json:"type"`
	RandomMode string `json:"random_mode"`

	// Number
	Min       *int64   `json:"min,omitempty"`
	Max       *int64   `json:"max,omitempty"`
	Exponent  *float64 `json:"exponent,omitempty"`
	Partition *int64   `json:"partition,omitempty"`

	// String
	Format       *string `json:"format,omitempty"`
	NumberConfig *Param  `json:"number_config,omitempty"`

	// random_string: draws Length characters from Charset (defaults to
	// mixed-case letters and digits if omitted).
	Length  *int    `json:"length,omitempty"`
	Charset *string `json:"charset,omitempty"`

	// Set
	SetMode *string     `json:"set_mode,omitempty"`
	Values  interface{} `json:"values,omitempty"` // map[string]float64 or []string

	// Date
	StartTime *string `json:"start_time,omitempty"`
	EndTime   *string `json:"end_time,omitempty"`

	// Array
	ArraySize     *int    `json:"array_size,omitempty"`
	ElementType   *string `json:"element_type,omitempty"`
	ElementConfig *Param  `json:"element_config,omitempty"`

	// Session variables: let a value generated once be reused by later
	// params instead of generating a fresh independent random value.
	//
	// SaveAs, if set, stores this param's generated value under that name
	// once it has been generated. Scope controls where it is stored:
	//   - "transaction" (default when SaveAs is set): reset at the start of
	//     every session/transaction (config.UseTransaction's unit of work).
	//   - "connection": generated once per worker and kept for the whole
	//     lifetime of that worker's underlying connection.
	//
	// A param with Type "ref" doesn't generate anything; it looks up a
	// value previously stored under RefName (transaction scope is checked
	// first, then connection scope) and reuses it as-is.
	SaveAs  *string `json:"save_as,omitempty"`
	Scope   *string `json:"scope,omitempty"`
	RefName *string `json:"ref_name,omitempty"`

	// Offset, if set, is added to this param's int64 value (fresh or
	// looked up via ref) before it's used, e.g. a range query's upper
	// bound = a saved lower bound + a fixed range size.
	Offset *int64 `json:"offset,omitempty"`

	// Literal marks this param's value to be substituted directly into the
	// SQL text instead of bound as a "?" placeholder. Needed for
	// identifier-position values (e.g. a table name suffix) once real
	// server-side prepared statements are used: MySQL cannot bind a
	// placeholder to a table/column name, only to a value position.
	Literal *bool `json:"literal,omitempty"`
}

// IsLiteral reports whether this param should be substituted directly into
// the SQL text rather than bound as a query parameter.
func (p *Param) IsLiteral() bool {
	return p.Literal != nil && *p.Literal
}

// ScopeOrDefault returns the effective scope for a param whose SaveAs is set.
func (p *Param) ScopeOrDefault() string {
	if p.Scope != nil && *p.Scope != "" {
		return *p.Scope
	}
	return "transaction"
}

// LoadConfig reads a configuration file and returns a Config struct
func LoadConfig(path string) (*Config, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	err = json.Unmarshal(file, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}
