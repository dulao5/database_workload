package generator

import (
	"database_workload/config"
	"fmt"
	"math/rand"
	"time"
)

// Generator is the generic interface for all data generators.
type Generator interface {
	Generate() interface{}
}

// init runs once to seed the random number generator.
func init() {
	rand.Seed(time.Now().UnixNano())
}

// New is a factory function that creates a generator based on the param config.
func New(p *config.Param) (Generator, error) {
	switch p.Type {
	case "number":
		return NewNumberGenerator(p)
	case "string":
		return NewStringGenerator(p)
	case "date":
		// The 'date' type from the config produces a formatted string.
		return NewDateStringGenerator(p)
	case "array":
		return NewArrayGenerator(p)
	default:
		return nil, fmt.Errorf("unknown parameter type: %s", p.Type)
	}
}
