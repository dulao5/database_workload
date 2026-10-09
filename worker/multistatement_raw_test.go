package worker

import (
	"database_workload/config"
	"regexp"
	"strings"
	"testing"
)

func TestBuildMultiStatementRawBatch_InlinesLiteralsNoPlaceholders(t *testing.T) {
	w := newTestWorkerForMultiStatement([]config.Template{
		{
			SQL: "SELECT c FROM t WHERE id = ?",
			Params: []config.Param{
				numberParam(1, 2),
			},
		},
	})

	batch, hasStatement, err := w.buildMultiStatementRawBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementRawBatch failed: %v", err)
	}
	if !hasStatement {
		t.Fatalf("expected hasStatement=true")
	}
	if strings.Contains(batch, "PREPARE") || strings.Contains(batch, "EXECUTE") || strings.Contains(batch, "?") {
		t.Fatalf("raw batch must have no PREPARE/EXECUTE/placeholders, got: %s", batch)
	}
	if !regexp.MustCompile(`^begin;SELECT c FROM t WHERE id = \d+;commit;$`).MatchString(batch) {
		t.Fatalf("unexpected raw batch shape: %s", batch)
	}
}

func TestBuildMultiStatementRawBatch_TransactionScopedRefIsReused(t *testing.T) {
	orderID := numberParam(1, 1000)
	orderID.SaveAs = ptr("orderID")

	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "SELECT c FROM t WHERE id = ?", Params: []config.Param{orderID}},
		{SQL: "UPDATE t SET k = k + 1 WHERE id = ?", Params: []config.Param{
			{Type: "ref", RefName: ptr("orderID")},
		}},
	})

	batch, _, err := w.buildMultiStatementRawBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementRawBatch failed: %v", err)
	}

	matches := regexp.MustCompile(`id = (\d+)`).FindAllStringSubmatch(batch, -1)
	if len(matches) != 2 {
		t.Fatalf("expected exactly 2 inlined ids in the batch, got %d: %s", len(matches), batch)
	}
	if matches[0][1] != matches[1][1] {
		t.Fatalf("expected the ref to reuse the same value: first=%s second=%s in batch %s", matches[0][1], matches[1][1], batch)
	}
}

func TestBuildMultiStatementRawBatch_LiteralParamInlined(t *testing.T) {
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

	batch, _, err := w.buildMultiStatementRawBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementRawBatch failed: %v", err)
	}
	if strings.Contains(batch, "sbtest?") {
		t.Fatalf("literal param leaked through as a placeholder: %s", batch)
	}
	if !regexp.MustCompile(`sbtest\d`).MatchString(batch) {
		t.Fatalf("expected the table number inlined into the table name, got: %s", batch)
	}
}

func TestBuildMultiStatementRawBatch_AllLocalOnlyProducesNoStatement(t *testing.T) {
	tableNum := numberParam(1, 3)
	tableNum.SaveAs = ptr("tableNum")

	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "", Params: []config.Param{tableNum}},
	})

	batch, hasStatement, err := w.buildMultiStatementRawBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementRawBatch failed: %v", err)
	}
	if hasStatement || batch != "" {
		t.Fatalf("expected no statement when every template is local-only, got hasStatement=%v batch=%q", hasStatement, batch)
	}
}
