package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type RiskClass string

const (
	Routine     RiskClass = "routine"
	HighImpact  RiskClass = "high_impact"
	Destructive RiskClass = "destructive"
)

func (r RiskClass) Valid() bool {
	switch r {
	case Routine, HighImpact, Destructive:
		return true
	default:
		return false
	}
}

func (r RiskClass) RequiresForce() bool {
	return r == HighImpact || r == Destructive
}

type Change struct {
	Op       string `json:"op"`
	Resource string `json:"resource"`
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
}

type Plan struct {
	Summary string   `json:"summary"`
	Changes []Change `json:"changes"`
}

// Target is the immutable identity and canonical snapshot captured while a
// mutation plan is prepared. Its fields are private so callers can consume,
// but cannot retarget, a validated mutation before apply.
type Target struct {
	id       string
	snapshot []byte
}

func (t Target) ID() string { return t.id }

func (t Target) Matches(current any) (bool, error) {
	b, err := json.Marshal(current)
	if err != nil {
		return false, fmt.Errorf("encode current target snapshot: %w", err)
	}
	return bytes.Equal(t.snapshot, b), nil
}

type PreparedMutation struct {
	plan         Plan
	target       Target
	hasTarget    bool
	risk         RiskClass
	experimental bool
}

func Targeted(p Plan, targetID string, snapshot any, risk RiskClass, experimental bool) (PreparedMutation, error) {
	if targetID == "" {
		return PreparedMutation{}, fmt.Errorf("prepared mutation target ID is required")
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return PreparedMutation{}, fmt.Errorf("encode prepared target snapshot: %w", err)
	}
	return PreparedMutation{
		plan:         p,
		target:       Target{id: targetID, snapshot: append([]byte(nil), b...)},
		hasTarget:    true,
		risk:         risk,
		experimental: experimental,
	}, nil
}

func Untargeted(p Plan, risk RiskClass, experimental bool) PreparedMutation {
	return PreparedMutation{plan: p, risk: risk, experimental: experimental}
}

func (m PreparedMutation) Plan() Plan             { return m.plan }
func (m PreparedMutation) Risk() RiskClass        { return m.risk }
func (m PreparedMutation) Experimental() bool     { return m.experimental }
func (m PreparedMutation) Target() (Target, bool) { return m.target, m.hasTarget }

func Update(resource, id, name, summary string, before, after any) Plan {
	return Plan{
		Summary: summary,
		Changes: []Change{{Op: "update", Resource: resource, ID: id, Name: name, Before: before, After: after}},
	}
}

func Create(resource, name, summary string, after any) Plan {
	return Plan{
		Summary: summary,
		Changes: []Change{{Op: "create", Resource: resource, Name: name, After: after}},
	}
}

func Delete(resource, id, name, summary string, before any) Plan {
	return Plan{
		Summary: summary,
		Changes: []Change{{Op: "delete", Resource: resource, ID: id, Name: name, Before: before}},
	}
}
