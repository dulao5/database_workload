package generator

import (
	"database_workload/config"
	"reflect"
	"testing"
)

func TestArrayGenerator(t *testing.T) {
	min, max := int64(1), int64(10)
	arraySize := 5
	elementType := "number"

	param := &config.Param{
		Type:      "array",
		ArraySize: &arraySize,
		ElementType: &elementType,
		ElementConfig: &config.Param{
			RandomMode: "uniform",
			Min:        &min,
			Max:        &max,
		},
	}

	gen, err := New(param)
	if err != nil {
		t.Fatalf("Failed to create array generator: %v", err)
	}

	val := gen.Generate()
	arr, ok := val.([]interface{})
	if !ok {
		t.Fatalf("Generator did not return a slice, got %T", val)
	}

	if len(arr) != arraySize {
		t.Fatalf("Expected array of size %d, got %d", arraySize, len(arr))
	}

	for _, item := range arr {
		num, ok := item.(int64)
		if !ok {
			t.Fatalf("Array element is not int64, got %T", item)
		}
		if num < min || num > max {
			t.Errorf("Array element %d is out of range [%d, %d]", num, min, max)
		}
	}
}

func TestArrayGenerator_StringElements(t *testing.T) {
	arraySize := 3
	elementType := "string"
	format := "item_%d"
	min, max := int64(1), int64(1)

	param := &config.Param{
		Type:        "array",
		ArraySize:   &arraySize,
		ElementType: &elementType,
		ElementConfig: &config.Param{
			RandomMode: "number_format",
			Format:     &format,
			NumberConfig: &config.Param{
				RandomMode: "uniform",
				Min:        &min,
				Max:        &max,
			},
		},
	}

	gen, err := New(param)
	if err != nil {
		t.Fatalf("Failed to create array generator: %v", err)
	}

	val := gen.Generate()
	arr, ok := val.([]interface{})
	if !ok {
		t.Fatalf("Generator did not return a slice, got %T", val)
	}

	if len(arr) != 3 {
		t.Fatalf("Expected array of size 3, got %d", len(arr))
	}

	expected := []interface{}{"item_1", "item_1", "item_1"}
	if !reflect.DeepEqual(arr, expected) {
		t.Errorf("Expected %v, got %v", expected, arr)
	}
}
