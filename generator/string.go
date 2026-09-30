package generator

import (
	"database_workload/config"
	"fmt"
	"math/rand"
)

const defaultRandomStringCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// NewStringGenerator is a factory for creating string generators.
func NewStringGenerator(p *config.Param) (Generator, error) {
	switch p.RandomMode {
	case "random_string":
		if p.Length == nil || *p.Length <= 0 {
			return nil, fmt.Errorf("random_string requires a positive length")
		}
		charset := defaultRandomStringCharset
		if p.Charset != nil && *p.Charset != "" {
			charset = *p.Charset
		}
		return newRandomStringGenerator(*p.Length, charset)
	case "number_format":
		if p.Format == nil || p.NumberConfig == nil {
			return nil, fmt.Errorf("number_format requires format and number_config")
		}
		// Important: number_config needs a type to be processed by the main factory
		p.NumberConfig.Type = "number"
		numGen, err := New(p.NumberConfig)
		if err != nil {
			return nil, err
		}
		return newNumberFormatGenerator(*p.Format, numGen)
	case "set":
		if p.SetMode == nil || p.Values == nil {
			return nil, fmt.Errorf("set mode requires set_mode and values")
		}
		switch *p.SetMode {
		case "weighted":
			valueMap, ok := p.Values.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("weighted set values must be a map, got %T", p.Values)
			}
			return newWeightedStringSetGenerator(valueMap)
		case "uniform":
			valueSlice, ok := p.Values.([]interface{})
			if !ok {
				return nil, fmt.Errorf("uniform set values must be an array, got %T", p.Values)
			}
			return newUniformStringSetGenerator(valueSlice)
		default:
			return nil, fmt.Errorf("unknown set_mode: %s", *p.SetMode)
		}
	default:
		return nil, fmt.Errorf("unknown string random_mode: %s", p.RandomMode)
	}
}

// RandomStringGenerator generates a fixed-length string drawn from a charset.
type RandomStringGenerator struct {
	length  int
	charset []rune
}

func newRandomStringGenerator(length int, charset string) (*RandomStringGenerator, error) {
	runes := []rune(charset)
	if len(runes) == 0 {
		return nil, fmt.Errorf("random_string charset cannot be empty")
	}
	return &RandomStringGenerator{length: length, charset: runes}, nil
}

func (g *RandomStringGenerator) Generate() interface{} {
	b := make([]rune, g.length)
	for i := range b {
		b[i] = g.charset[rand.Intn(len(g.charset))]
	}
	return string(b)
}

// NumberFormatGenerator generates a string by formatting a number.
type NumberFormatGenerator struct {
	format    string
	numberGen Generator
}

func newNumberFormatGenerator(format string, numGen Generator) (*NumberFormatGenerator, error) {
	return &NumberFormatGenerator{
		format:    format,
		numberGen: numGen,
	}, nil
}

func (g *NumberFormatGenerator) Generate() interface{} {
	num := g.numberGen.Generate().(int64)
	return fmt.Sprintf(g.format, num)
}

// WeightedStringSetGenerator generates a string from a weighted set.
type WeightedStringSetGenerator struct {
	values  []string
	weights []float64 // cumulative weights
	total   float64
}

func newWeightedStringSetGenerator(valueMap map[string]interface{}) (*WeightedStringSetGenerator, error) {
	var values []string
	var weights []float64
	var total float64

	for v, wRaw := range valueMap {
		w, ok := wRaw.(float64)
		if !ok {
			return nil, fmt.Errorf("invalid weight for value %s: not a float", v)
		}
		values = append(values, v)
		total += w
		weights = append(weights, total)
	}

	return &WeightedStringSetGenerator{
		values:  values,
		weights: weights,
		total:   total,
	}, nil
}

func (g *WeightedStringSetGenerator) Generate() interface{} {
	if len(g.values) == 0 {
		return ""
	}
	p := rand.Float64() * g.total
	for i, w := range g.weights {
		if p < w {
			return g.values[i]
		}
	}
	return g.values[len(g.values)-1]
}

// UniformStringSetGenerator generates a string from a uniform set.
type UniformStringSetGenerator struct {
	values []string
}

func newUniformStringSetGenerator(valueSlice []interface{}) (*UniformStringSetGenerator, error) {
	var values []string
	for _, vRaw := range valueSlice {
		v, ok := vRaw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid value in set: not a string")
		}
		values = append(values, v)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("uniform set cannot be empty")
	}
	return &UniformStringSetGenerator{values: values}, nil
}

func (g *UniformStringSetGenerator) Generate() interface{} {
	return g.values[rand.Intn(len(g.values))]
}
