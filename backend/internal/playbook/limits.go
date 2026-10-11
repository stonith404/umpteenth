package playbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// LimitChange is the limits a limits suggestion asks for, where a nil field stays as it is
// The cost limit is left out on purpose, since spend stays the owner's own call
type LimitChange struct {
	TimeoutSeconds *int     `json:"timeoutSeconds,omitempty" minimum:"30" maximum:"86400"`
	MaxTurns       *int     `json:"maxTurns,omitempty" minimum:"1" maximum:"1000"`
	CPUs           *float64 `json:"cpus,omitempty" minimum:"0.1" maximum:"64"`
	MemoryMB       *int     `json:"memoryMb,omitempty" minimum:"64" maximum:"262144"`
}

// limitBounds are the bounds of each limit, the same as the job form's
var limitBounds = []struct {
	name     string
	min, max float64
	value    func(LimitChange) *float64
}{
	{"memoryMb", 64, 262144, func(l LimitChange) *float64 { return intValue(l.MemoryMB) }},
	{"cpus", 0.1, 64, func(l LimitChange) *float64 { return l.CPUs }},
	{"timeoutSeconds", 30, 86400, func(l LimitChange) *float64 { return intValue(l.TimeoutSeconds) }},
	{"maxTurns", 1, 1000, func(l LimitChange) *float64 { return intValue(l.MaxTurns) }},
}

// ParseLimitChange reads the limits JSON of a limits suggestion, which must set at least one limit within its bounds
func ParseLimitChange(raw string) (LimitChange, error) {
	usage := errors.New(`content must be a JSON object such as {"memoryMb": 4096} with any of memoryMb, cpus, timeoutSeconds and maxTurns`)

	// Unknown fields are refused, so a misspelt or unsupported limit such as maxCostUsd never passes as an empty change
	var l LimitChange
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return LimitChange{}, usage
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return LimitChange{}, usage
	}

	// Every set limit must be one the job form accepts
	set := 0
	for _, b := range limitBounds {
		v := b.value(l)
		if v == nil {
			continue
		}
		set++
		if *v < b.min || *v > b.max {
			return LimitChange{}, fmt.Errorf("%s must be between %g and %g", b.name, b.min, b.max)
		}
	}
	if set == 0 {
		return LimitChange{}, usage
	}
	return l, nil
}

// Pick returns current's values for exactly the limits l sets, which are the values a suggestion changes
func (l LimitChange) Pick(current LimitChange) LimitChange {
	var out LimitChange
	if l.TimeoutSeconds != nil {
		out.TimeoutSeconds = current.TimeoutSeconds
	}
	if l.MaxTurns != nil {
		out.MaxTurns = current.MaxTurns
	}
	if l.CPUs != nil {
		out.CPUs = current.CPUs
	}
	if l.MemoryMB != nil {
		out.MemoryMB = current.MemoryMB
	}
	return out
}

// Unchanged names the limits l sets to the value current already has
func (l LimitChange) Unchanged(current LimitChange) []string {
	var names []string
	for _, b := range limitBounds {
		v, c := b.value(l), b.value(current)
		if v != nil && c != nil && *v == *c {
			names = append(names, b.name)
		}
	}
	return names
}

// describe lists the named limits with their values, e.g. "memoryMb 4096, cpus 2"
func (l LimitChange) describe(names []string) string {
	parts := make([]string, 0, len(names))
	for _, b := range limitBounds {
		if v := b.value(l); v != nil && slices.Contains(names, b.name) {
			parts = append(parts, fmt.Sprintf("%s %g", b.name, *v))
		}
	}
	return strings.Join(parts, ", ")
}

func intValue(v *int) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v)
	return &f
}
