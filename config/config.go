package config

import (
	"encoding/json"
	"os"
)

// Config is the main configuration structure
type Config struct {
	Concurrency    int        `json:"concurrency"`
	RatePerThread  int        `json:"rate_per_thread"`
	DBConnStr      string     `json:"db_conn_str"`
	ConnectionType string     `json:"connection_type,omitempty"`
	UseTransaction bool       `json:"use_transaction"`
	Templates      []Template `json:"templates"`
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
