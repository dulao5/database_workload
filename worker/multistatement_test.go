package worker

import (
	"database_workload/config"
	"database_workload/generator"
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
		templates:  templates,
		generators: gens,
		connVars:   make(map[string]interface{}),
	}
}

func TestBuildMultiStatementBatch_WrapsTemplatesWithBeginCommit(t *testing.T) {
	w := newTestWorkerForMultiStatement([]config.Template{
		{
			SQL: "SELECT c FROM t WHERE id = ?",
			Params: []config.Param{
				numberParam(1, 2),
			},
		},
	})

	batch, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}
	if batch == nil {
		t.Fatalf("expected a non-nil batch")
	}

	stmts := batch.Statements()
	if len(stmts) != 3 {
		t.Fatalf("expected begin + 1 template + commit = 3 statements, got %d: %+v", len(stmts), stmts)
	}
	if stmts[0].SQL != "begin" {
		t.Fatalf("expected the first statement to be begin, got %q", stmts[0].SQL)
	}
	if stmts[len(stmts)-1].SQL != "commit" {
		t.Fatalf("expected the last statement to be commit, got %q", stmts[len(stmts)-1].SQL)
	}
	if stmts[1].SQL != "SELECT c FROM t WHERE id = ?" {
		t.Fatalf("expected the template's SQL unchanged (multistmt itself renders PREPARE/EXECUTE), got %q", stmts[1].SQL)
	}
	if !stmts[1].HasResultSet {
		t.Fatalf("expected a SELECT template to be marked HasResultSet")
	}
	if len(stmts[1].Args) != 1 {
		t.Fatalf("expected exactly one bound arg, got %v", stmts[1].Args)
	}
}

func TestBuildMultiStatementBatch_NonSelectHasNoResultSet(t *testing.T) {
	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "UPDATE t SET k = k + 1 WHERE id = ?", Params: []config.Param{numberParam(1, 2)}},
	})

	batch, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}
	stmts := batch.Statements()
	if stmts[1].HasResultSet {
		t.Fatalf("expected an UPDATE template to not be marked HasResultSet")
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

	stmts := batch.Statements()
	// stmts[0]=begin, [1]=SELECT, [2]=UPDATE, [3]=commit
	if len(stmts) != 4 {
		t.Fatalf("expected 4 statements, got %d: %+v", len(stmts), stmts)
	}
	selectArg := stmts[1].Args[0]
	updateArg := stmts[2].Args[0]
	if selectArg != updateArg {
		t.Fatalf("expected the ref to reuse the same value: select=%v update=%v", selectArg, updateArg)
	}
}

func TestBuildMultiStatementBatch_LiteralParamInlinedIntoSQL(t *testing.T) {
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

	stmts := batch.Statements()
	// stmts[0]=begin, [1]=the SELECT (the local-only template emits nothing)
	sql := stmts[1].SQL
	if sql == "SELECT c FROM sbtest? WHERE id = ?" {
		t.Fatalf("expected the table number inlined into the SQL text, got: %s", sql)
	}
	if len(stmts[1].Args) != 1 {
		t.Fatalf("expected the literal param to not count as a bind arg, got %v", stmts[1].Args)
	}
}

func TestBuildMultiStatementBatch_AllLocalOnlyProducesNilBatch(t *testing.T) {
	tableNum := numberParam(1, 3)
	tableNum.SaveAs = ptr("tableNum")

	w := newTestWorkerForMultiStatement([]config.Template{
		{SQL: "", Params: []config.Param{tableNum}},
	})

	batch, err := w.buildMultiStatementBatch(make(map[string]interface{}))
	if err != nil {
		t.Fatalf("buildMultiStatementBatch failed: %v", err)
	}
	if batch != nil {
		t.Fatalf("expected a nil batch when every template is local-only, got: %+v", batch.Statements())
	}
}
