package worker

import (
	"database_workload/config"
	"database_workload/generator"
	"regexp"
	"strings"
	"testing"
)

func newTestWorkerForMultiStatement(templates []config.Template) *Worker {
	gens := make([][]generator.Generator, len(templates))
	for i, tmpl := range templates {
		gens[i] = make([]generator.Generator, len(tmpl.Params))
		for j, param := range tmpl.Params {
			if param.Type == "ref" {
				continue
			}
			p := param
			g, err := generator.New(&p)
			if err != nil {
				panic(err)
			}
			gens[i][j] = g
		}
	}
	return &Worker{
		templates:      templates,
		generators:     gens,
		connVars:       make(map[string]interface{}),
		multiStmtNames: make(map[string]string),
	}
}

func TestBuildMultiStatementBatch_PreparesOnceReusesAcrossCalls(t *testing.T) {
	w := newTestWorkerForMultiStatement([]config.Template{
		{
			SQL: "SELECT c FROM t WHERE id = ?",
			Params: []config.Param{
				numberParam(1, 2),
			},
		},
	})

	batch1, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}
	if strings.Count(batch1, "PREPARE dw_ps_1 FROM") != 1 {
		t.Fatalf("expected exactly one PREPARE in the first batch, got: %s", batch1)
	}

	batch2, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}
	if strings.Contains(batch2, "PREPARE") {
		t.Fatalf("expected no PREPARE on the second batch (already registered), got: %s", batch2)
	}
	if !strings.Contains(batch2, "EXECUTE dw_ps_1") {
		t.Fatalf("expected the second batch to reuse dw_ps_1, got: %s", batch2)
	}
}

func TestBuildMultiStatementBatch_TransactionScopedRefIsReused(t *testing.T) {
	orderID := numberParam(1, 1000)
	orderID.SaveAs = ptr("orderID")

	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "SELECT c FROM t WHERE id = ?", Params: []config.Param{orderID}},
		{SQL: "UPDATE t SET k = k + 1 WHERE id = ?", Params: []config.Param{
			{Type: "ref", RefName: ptr("orderID")},
		}},
	})

	batch, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}

	// Both "SET @mv_...=<value>;" assignments must carry the exact same
	// value, since the second one is a ref to the first template's saved id.
	matches := regexp.MustCompile(`SET @mv_\w+=(\d+);`).FindAllStringSubmatch(batch, -1)
	if len(matches) != 2 {
		t.Fatalf("expected exactly 2 SET assignments in the batch, got %d: %s", len(matches), batch)
	}
	if matches[0][1] != matches[1][1] {
		t.Fatalf("expected the ref to reuse the same value: first=%s second=%s in batch %s", matches[0][1], matches[1][1], batch)
	}
}

func TestBuildMultiStatementBatch_LiteralSkipsPrepareParam(t *testing.T) {
	tableNum := numberParam(1, 3)
	tableNum.SaveAs = ptr("tableNum")
	tableNum.Scope = ptr("connection")

	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "", Params: []config.Param{tableNum}},
		{SQL: "SELECT c FROM sbtest? WHERE id = ?", Params: []config.Param{
			{Type: "ref", RefName: ptr("tableNum"), Literal: ptr(true)},
			numberParam(1, 2),
		}},
	})

	batch, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}

	if !strings.Contains(batch, "PREPARE dw_ps_1 FROM 'SELECT c FROM sbtest") {
		t.Fatalf("expected the table number inlined into the prepared SQL text, got: %s", batch)
	}
	if strings.Contains(batch, "sbtest?") {
		t.Fatalf("literal param leaked through as a bind placeholder: %s", batch)
	}
}

func TestBuildMultiStatementBatch_AllLocalOnlyProducesEmptyBatch(t *testing.T) {
	tableNum := numberParam(1, 3)
	tableNum.SaveAs = ptr("tableNum")

	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "", Params: []config.Param{tableNum}},
	})

	batch, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}
	if batch != "" {
		t.Fatalf("expected an empty batch when every template is local-only, got: %s", batch)
	}
}
