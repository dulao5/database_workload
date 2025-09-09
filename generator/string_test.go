package generator

import (
	"database_workload/config"
	"testing"
)

func TestStringNumberFormatGenerator(t *testing.T) {
	min, max := int64(100), int64(100)
	format := "user_%d"
	param := &config.Param{
		Type:       "string",
		RandomMode: "number_format",
		Format:     &format,
		NumberConfig: &config.Param{
			RandomMode: "uniform",
			Min:        &min,
			Max:        &max,
		},
	}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("factory failed for number_format: %v", err)
	}

	str := gen.Generate().(string)
	expected := "user_100"
	if str != expected {
		t.Errorf("expected %s, got %s", expected, str)
	}
}

func TestWeightedStringSetGenerator(t *testing.T) {
	setMode := "weighted"
	values := map[string]interface{}{
		"cat1": 0.6,
		"cat2": 0.3,
		"cat3": 0.1,
	}
	param := &config.Param{Type: "string", RandomMode: "set", SetMode: &setMode, Values: values}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}

	counts := make(map[string]int)
	for i := 0; i < 10000; i++ {
		counts[gen.Generate().(string)]++
	}

	if counts["cat1"] < 5500 || counts["cat1"] > 6500 {
		t.Errorf("unexpected count for cat1: %d", counts["cat1"])
	}
	if counts["cat2"] < 2500 || counts["cat2"] > 3500 {
		t.Errorf("unexpected count for cat2: %d", counts["cat2"])
	}
	if counts["cat3"] < 500 || counts["cat3"] > 1500 {
		t.Errorf("unexpected count for cat3: %d", counts["cat3"])
	}
}

func TestUniformStringSetGenerator(t *testing.T) {
	setMode := "uniform"
	values := []interface{}{"a", "b", "c"}
	param := &config.Param{Type: "string", RandomMode: "set", SetMode: &setMode, Values: values}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}
	for i := 0; i < 100; i++ {
		val := gen.Generate().(string)
		if val != "a" && val != "b" && val != "c" {
			t.Errorf("unexpected value: %s", val)
		}
	}
}