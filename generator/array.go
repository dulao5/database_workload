package generator

import (
	"database_workload/config"
	"fmt"
)

// ArrayGenerator generates an array of values.
type ArrayGenerator struct {
	size       int
	elementGen Generator
}

// NewArrayGenerator creates a new ArrayGenerator.
func NewArrayGenerator(p *config.Param) (Generator, error) {
	if p.ArraySize == nil || p.ElementType == nil || p.ElementConfig == nil {
		return nil, fmt.Errorf("array type requires array_size, element_type, and element_config")
	}
	if *p.ArraySize <= 0 {
		return nil, fmt.Errorf("array_size must be positive")
	}

	// The ElementConfig needs its 'type' field set for the factory to work.
	p.ElementConfig.Type = *p.ElementType

	elementGen, err := New(p.ElementConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create element generator for array: %w", err)
	}

	return &ArrayGenerator{
		size:       *p.ArraySize,
		elementGen: elementGen,
	}, nil
}

// Generate creates an array of random values.
func (g *ArrayGenerator) Generate() interface{} {
	arr := make([]interface{}, g.size)
	for i := 0; i < g.size; i++ {
		arr[i] = g.elementGen.Generate()
	}
	return arr
}
