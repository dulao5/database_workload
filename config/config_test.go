package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleConfig = `{
  "concurrency": 100,
  "db_conn_str": "mysql://user:password@tcp(host:port)/dbname",
  "templates": [
    {
      "sql": "INSERT INTO table (id, name, value, created_at) VALUES (?, ?, ?, ?)",
      "params": [
        {
          "type": "number",
          "random_mode": "uniform",
          "min": 1,
          "max": 1000
        },
        {
          "type": "string",
          "random_mode": "number_format",
          "format": "user_%d",
          "number_config": {
            "random_mode": "power_law",
            "min": 10000000000,
            "max": 14400000000,
            "exponent": 2.5
          }
        },
        {
          "type": "number",
          "random_mode": "power_law",
          "min": 1,
          "max": 1000,
          "exponent": 1.5
        },
        {
          "type": "date",
          "random_mode": "timestamp_range",
          "start_time": "2023-01-01T00:00:00Z",
          "end_time": "2023-12-31T23:59:59Z",
          "format": "2006-01-02 15:04:05"
        }
      ]
    },
    {
      "sql": "SELECT * FROM table WHERE category = ? AND id IN (?)",
      "params": [
        {
          "type": "string",
          "random_mode": "set",
          "set_mode": "weighted",
          "values": {
            "cat1": 0.6,
            "cat2": 0.3,
            "cat3": 0.1
          }
        },
        {
          "type": "array",
          "array_size": 5,
          "element_type": "number",
          "element_config": {
            "random_mode": "uniform",
            "min": 1,
            "max": 1000
          }
        }
      ]
    }
  ]
}`

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	err := os.WriteFile(configPath, []byte(sampleConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Concurrency != 100 {
		t.Errorf("Expected Concurrency 100, got %d", cfg.Concurrency)
	}

	if cfg.DBConnStr != "mysql://user:password@tcp(host:port)/dbname" {
		t.Errorf("Unexpected DBConnStr: %s", cfg.DBConnStr)
	}

	if len(cfg.Templates) != 2 {
		t.Fatalf("Expected 2 templates, got %d", len(cfg.Templates))
	}

	// A few more checks on nested data
	if cfg.Templates[0].SQL != "INSERT INTO table (id, name, value, created_at) VALUES (?, ?, ?, ?)" {
		t.Errorf("Unexpected SQL in first template: %s", cfg.Templates[0].SQL)
	}

	if len(cfg.Templates[0].Params) != 4 {
		t.Errorf("Expected 4 params in first template, got %d", len(cfg.Templates[0].Params))
	}

	if cfg.Templates[1].Params[1].Type != "array" {
		t.Errorf("Expected second param of second template to be array, got %s", cfg.Templates[1].Params[1].Type)
	}
}
