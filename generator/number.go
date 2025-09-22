package generator

import (
	"database_workload/config"
	"fmt"
	"math"
	"math/rand"
)

// NewNumberGenerator is a factory for creating number generators from config.
func NewNumberGenerator(p *config.Param) (Generator, error) {
	switch p.RandomMode {
	case "uniform":
		if p.Min == nil || p.Max == nil {
			return nil, fmt.Errorf("uniform mode requires min and max")
		}
		return newUniformGenerator(*p.Min, *p.Max)
	case "power_law":
		if p.Min == nil || p.Max == nil || p.Exponent == nil {
			return nil, fmt.Errorf("power_law mode requires min, max, and exponent")
		}
		return newPowerLawGenerator(*p.Min, *p.Max, *p.Exponent)
	case "partition_power_law":
		if p.Min == nil || p.Max == nil || p.Exponent == nil || p.Partition == nil {
			return nil, fmt.Errorf("partition_power_law mode requires min, max, exponent, and partition")
		}
		return newPartitionedPowerLawGenerator(*p.Min, *p.Max, *p.Partition, *p.Exponent)
	default:
		return nil, fmt.Errorf("unknown number random_mode: %s", p.RandomMode)
	}
}

// UniformGenerator generates a number uniformly in a given range.
type UniformGenerator struct {
	min int64
	max int64
}

func newUniformGenerator(min, max int64) (*UniformGenerator, error) {
	if min > max {
		return nil, fmt.Errorf("min (%d) cannot be greater than max (%d)", min, max)
	}
	return &UniformGenerator{min: min, max: max}, nil
}

func (g *UniformGenerator) Generate() interface{} {
	if g.min == g.max {
		return g.min
	}
	return g.min + rand.Int63n(g.max-g.min+1)
}

// PowerLawGenerator generates a number according to a power law distribution.
type PowerLawGenerator struct {
	min      int64
	max      int64
	exponent float64
	c1       float64
	c2       float64
	c3       float64
}

func newPowerLawGenerator(min, max int64, exponent float64) (*PowerLawGenerator, error) {
	if min <= 0 || max <= 0 || min > max {
		return nil, fmt.Errorf("invalid min/max for power law: min=%d, max=%d (must be > 0, min <= max)", min, max)
	}
	if exponent == 1.0 {
		return nil, fmt.Errorf("exponent cannot be 1.0 for power law")
	}
	oneMinusAlpha := 1.0 - exponent
	maxF := float64(max)
	return &PowerLawGenerator{
		min:      min,
		max:      max,
		exponent: exponent,
		c1:       math.Pow(1, oneMinusAlpha),
		c2:       math.Pow(maxF, oneMinusAlpha) - math.Pow(1, oneMinusAlpha),
		c3:       1.0 / oneMinusAlpha,
	}, nil
}

func (g *PowerLawGenerator) Generate() interface{} {
	y := rand.Float64()
	val := math.Pow(y*g.c2+g.c1, g.c3)
	result := int64(math.Round(val)) + g.min - 1
	if result < g.min {
		return g.min
	}
	if result > g.max {
		return g.max
	}
	return result
}

// PartitionedPowerLawGenerator generates a number using partitioned power law.
type PartitionedPowerLawGenerator struct {
	min       int64
	max       int64
	partition int64
	exponent  float64
}

func newPartitionedPowerLawGenerator(min, max, partition int64, exponent float64) (*PartitionedPowerLawGenerator, error) {
	if min > max || partition <= 0 {
		return nil, fmt.Errorf("invalid args for partitioned power law: min=%d, max=%d, partition=%d", min, max, partition)
	}
	return &PartitionedPowerLawGenerator{
		min:       min,
		max:       max,
		partition: partition,
		exponent:  exponent,
	}, nil
}

func (g *PartitionedPowerLawGenerator) Generate() interface{} {
	partitionSize := (g.max - g.min + 1) / g.partition
	if partitionSize == 0 {
		partitionSize = 1
	}
	selectedPartition := rand.Int63n(g.partition)

	partMin := g.min + selectedPartition*partitionSize
	partMax := partMin + partitionSize - 1
	if partMax > g.max || selectedPartition == g.partition-1 {
		partMax = g.max
	}

	if partMin > partMax {
		partMin = partMax
	}
	fmt.Println("min, max", partMin, partMax)
	gen, err := newPowerLawGenerator(partMin, partMax, g.exponent)
	if err != nil {
		// Fallback to uniform on error
		return partMin + rand.Int63n(partMax-partMin+1)
	}
	return gen.Generate()
}
