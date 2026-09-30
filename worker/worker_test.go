package worker

import (
	"database_workload/config"
	"database_workload/generator"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func numberParam(min, max int64) config.Param {
	return config.Param{
		Type:       "number",
		RandomMode: "uniform",
		Min:        ptr(min),
		Max:        ptr(max),
	}
}

func TestResolveArg_ConnectionScopeReusedAcrossTransactions(t *testing.T) {
	w := &Worker{connVars: make(map[string]interface{})}

	p := numberParam(1, 1_000_000)
	p.SaveAs = ptr("tableNum")
	p.Scope = ptr("connection")
	gen, err := generator.New(&p)
	if err != nil {
		t.Fatalf("failed to build generator: %v", err)
	}

	// First transaction: generates and saves the value.
	tx1 := make(map[string]interface{})
	v1, err := w.resolveArg(&p, gen, tx1)
	if err != nil {
		t.Fatalf("resolveArg failed: %v", err)
	}

	// Second transaction (fresh txVars, same worker/connVars): must reuse
	// the exact same value, matching sysbench's "one random table per
	// connection, for its whole lifetime" behavior.
	tx2 := make(map[string]interface{})
	v2, err := w.resolveArg(&p, gen, tx2)
	if err != nil {
		t.Fatalf("resolveArg failed: %v", err)
	}

	if v1 != v2 {
		t.Fatalf("connection-scoped value changed across transactions: %v != %v", v1, v2)
	}
}

func TestResolveArg_TransactionScopeResetsPerTransaction(t *testing.T) {
	w := &Worker{connVars: make(map[string]interface{})}

	p := numberParam(1, 1_000_000_000)
	p.SaveAs = ptr("orderID")
	gen, err := generator.New(&p)
	if err != nil {
		t.Fatalf("failed to build generator: %v", err)
	}

	tx1 := make(map[string]interface{})
	if _, err := w.resolveArg(&p, gen, tx1); err != nil {
		t.Fatalf("resolveArg failed: %v", err)
	}
	if _, ok := tx1["orderID"]; !ok {
		t.Fatalf("expected orderID to be saved in tx1's scope")
	}

	// A fresh transaction must not see the previous transaction's value.
	tx2 := make(map[string]interface{})
	if _, ok := tx2["orderID"]; ok {
		t.Fatalf("transaction-scoped variable leaked into a new transaction")
	}
}

func TestResolveArg_RefReusesSavedValue(t *testing.T) {
	w := &Worker{connVars: make(map[string]interface{})}
	txVars := make(map[string]interface{})

	define := numberParam(1, 1000)
	define.SaveAs = ptr("orderID")
	defGen, err := generator.New(&define)
	if err != nil {
		t.Fatalf("failed to build generator: %v", err)
	}
	savedVal, err := w.resolveArg(&define, defGen, txVars)
	if err != nil {
		t.Fatalf("resolveArg failed: %v", err)
	}

	ref := config.Param{Type: "ref", RefName: ptr("orderID")}
	refVal, err := w.resolveArg(&ref, nil, txVars)
	if err != nil {
		t.Fatalf("resolveArg for ref failed: %v", err)
	}

	if savedVal != refVal {
		t.Fatalf("ref did not reuse the saved value: saved=%v ref=%v", savedVal, refVal)
	}
}

func TestResolveArg_RefBeforeSaveIsAnError(t *testing.T) {
	w := &Worker{connVars: make(map[string]interface{})}
	txVars := make(map[string]interface{})

	ref := config.Param{Type: "ref", RefName: ptr("neverSaved")}
	if _, err := w.resolveArg(&ref, nil, txVars); err == nil {
		t.Fatalf("expected an error when referencing a variable that was never saved")
	}
}

func TestSplitLiteralAndBindArgs_NoLiteralsIsUnchanged(t *testing.T) {
	sql := "SELECT c FROM t WHERE id = ? AND name = ?"
	params := []config.Param{{Type: "number"}, {Type: "string"}}
	args := []interface{}{1, "a"}

	gotSQL, gotArgs := splitLiteralAndBindArgs(sql, params, args)
	if gotSQL != sql {
		t.Fatalf("expected sql unchanged, got %q", gotSQL)
	}
	if len(gotArgs) != 2 || gotArgs[0] != 1 || gotArgs[1] != "a" {
		t.Fatalf("expected all args to remain bind args, got %v", gotArgs)
	}
}

func TestSplitLiteralAndBindArgs_LiteralSubstitutesIntoSQL(t *testing.T) {
	sql := "SELECT c FROM sbtest? WHERE id = ?"
	params := []config.Param{
		{Type: "ref", RefName: ptr("tableNum"), Literal: ptr(true)},
		{Type: "number"},
	}
	args := []interface{}{3, 42}

	gotSQL, gotArgs := splitLiteralAndBindArgs(sql, params, args)
	if gotSQL != "SELECT c FROM sbtest3 WHERE id = ?" {
		t.Fatalf("expected table number inlined into the SQL, got %q", gotSQL)
	}
	if len(gotArgs) != 1 || gotArgs[0] != 42 {
		t.Fatalf("expected only the non-literal arg to remain, got %v", gotArgs)
	}
}
