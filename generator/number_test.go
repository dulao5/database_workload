package generator

import (
	"database_workload/config"
	"testing"
)

func TestUniformGenerator(t *testing.T) {
	min, max := int64(10), int64(20)
	param := &config.Param{Type: "number", RandomMode: "uniform", Min: &min, Max: &max}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}
	for i := 0; i < 1000; i++ {
		val := gen.Generate().(int64)
		if val < 10 || val > 20 {
			t.Fatalf("generated value %d is out of range [10, 20]", val)
		}
	}
}

func TestUniformGenerator_SingleValue(t *testing.T) {
	min, max := int64(15), int64(15)
	param := &config.Param{Type: "number", RandomMode: "uniform", Min: &min, Max: &max}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}
	val := gen.Generate().(int64)
	if val != 15 {
		t.Errorf("expected 15, got %d", val)
	}
}

func TestPowerLawGenerator(t *testing.T) {
	min, max, exp := int64(1), int64(1000), 2.0
	param := &config.Param{Type: "number", RandomMode: "power_law", Min: &min, Max: &max, Exponent: &exp}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}
	counts := make(map[int64]int)
	for i := 0; i < 20000; i++ {
		val := gen.Generate().(int64)
		if val < min || val > max {
			t.Fatalf("generated value %d is out of range [%d, %d]", val, min, max)
		}
		counts[val]++
	}

	if counts[1] < counts[100] && counts[100] > 0 {
		t.Errorf("power law distribution seems incorrect, counts[1]=%d, counts[100]=%d", counts[1], counts[100])
	}
	if counts[1] == 0 {
		t.Errorf("power law distribution seems incorrect, counts[1] is zero")
	}
}

func TestPartitionedPowerLawGenerator(t *testing.T) {
	min, max, part, exp := int64(1), int64(10000), int64(10), 2.0
	param := &config.Param{Type: "number", RandomMode: "partition_power_law", Min: &min, Max: &max, Partition: &part, Exponent: &exp}
	gen, err := New(param)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}
	for i := 0; i < 10000; i++ {
		val := gen.Generate().(int64)
		if val < min || val > max {
			t.Fatalf("generated value %d is out of range [%d, %d]", val, min, max)
		}
	}
}