package generator

import (
	"testing"
	"time"
)

func TestTimestampRangeGenerator(t *testing.T) {
	start := "2024-01-01T00:00:00Z"
	end := "2024-01-01T00:00:10Z"
	gen, err := newTimestampRangeGenerator(start, end)
	if err != nil {
		t.Fatalf("failed to create generator: %v", err)
	}
	startTime, _ := time.Parse(time.RFC3339, start)
	endTime, _ := time.Parse(time.RFC3339, end)

	for i := 0; i < 100; i++ {
		val := gen.Time()
		if val.Before(startTime) || val.After(endTime) {
			t.Fatalf("generated time %v is out of range", val)
		}
	}
}

func TestDateFormatGenerator(t *testing.T) {
	start := "2024-01-01T14:00:00Z"
	end := "2024-01-01T14:00:00Z"
	timeGen, _ := newTimestampRangeGenerator(start, end)

	format := "2006-01-02 15:04:05"
	gen := &DateFormatGenerator{timeGen: timeGen, format: format}

	val := gen.Generate().(string)
	expected := "2024-01-01 14:00:00"
	if val != expected {
		t.Errorf("expected formatted date '%s', got '%s'", expected, val)
	}
}
