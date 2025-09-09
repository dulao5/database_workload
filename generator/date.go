package generator

import (
	"database_workload/config"
	"fmt"
	"math/rand"
	"time"
)

// DateFormatGenerator is a generator that produces a formatted string from a time object.
// It implements the main Generator interface.
type DateFormatGenerator struct {
	timeGen *TimestampRangeGenerator
	format  string
}

// Generate returns a formatted date string.
func (g *DateFormatGenerator) Generate() interface{} {
	t := g.timeGen.Time()
	return t.Format(g.format)
}

// NewDateStringGenerator is a factory for creating date-based generators that produce a string.
func NewDateStringGenerator(p *config.Param) (Generator, error) {
	if p.RandomMode != "timestamp_range" {
		return nil, fmt.Errorf("unsupported random_mode for date: %s", p.RandomMode)
	}
	if p.StartTime == nil || p.EndTime == nil {
		return nil, fmt.Errorf("timestamp_range requires start_time and end_time")
	}
	if p.Format == nil {
		return nil, fmt.Errorf("date type requires a format string")
	}

	timeGen, err := newTimestampRangeGenerator(*p.StartTime, *p.EndTime)
	if err != nil {
		return nil, err
	}

	return &DateFormatGenerator{
		timeGen: timeGen,
		format:  *p.Format,
	}, nil
}

// TimestampRangeGenerator generates a time.Time within a given range.
type TimestampRangeGenerator struct {
	start int64 // Unix timestamp
	end   int64 // Unix timestamp
}

// newTimestampRangeGenerator creates a new TimestampRangeGenerator.
func newTimestampRangeGenerator(start, end string) (*TimestampRangeGenerator, error) {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, fmt.Errorf("invalid start_time format (expected RFC3339): %w", err)
	}
	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return nil, fmt.Errorf("invalid end_time format (expected RFC3339): %w", err)
	}
	if startTime.After(endTime) {
		return nil, fmt.Errorf("start_time cannot be after end_time")
	}
	return &TimestampRangeGenerator{
		start: startTime.Unix(),
		end:   endTime.Unix(),
	}, nil
}

// Time generates a random time.Time object in UTC.
func (g *TimestampRangeGenerator) Time() time.Time {
	delta := g.end - g.start
	if delta <= 0 {
		return time.Unix(g.start, 0).UTC()
	}
	sec := rand.Int63n(delta) + g.start
	return time.Unix(sec, 0).UTC()
}
