package plan_test

import (
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/plan"
)

func TestUpdate(t *testing.T) {
	before := map[string]string{"name": "old"}
	after := map[string]string{"name": "new"}
	p := plan.Update("device", "id1", "ap1", "rename device", before, after)
	if p.Summary != "rename device" {
		t.Fatalf("summary = %q", p.Summary)
	}
	if len(p.Changes) != 1 {
		t.Fatalf("len(changes) = %d", len(p.Changes))
	}
	c := p.Changes[0]
	if c.Op != "update" || c.Resource != "device" || c.ID != "id1" || c.Name != "ap1" {
		t.Fatalf("change = %+v", c)
	}
	if c.Before == nil || c.After == nil {
		t.Fatal("before/after should be set")
	}
}

func TestCreate(t *testing.T) {
	after := map[string]string{"ssid": "guest"}
	p := plan.Create("wlan", "guest", "create wlan", after)
	if p.Summary != "create wlan" {
		t.Fatalf("summary = %q", p.Summary)
	}
	if len(p.Changes) != 1 {
		t.Fatalf("len(changes) = %d", len(p.Changes))
	}
	c := p.Changes[0]
	if c.Op != "create" || c.Resource != "wlan" || c.Name != "guest" || c.ID != "" {
		t.Fatalf("change = %+v", c)
	}
	if c.After == nil || c.Before != nil {
		t.Fatal("create should have after only")
	}
}

func TestDelete(t *testing.T) {
	before := map[string]string{"name": "ap1"}
	p := plan.Delete("device", "id1", "ap1", "delete device", before)
	if p.Summary != "delete device" {
		t.Fatalf("summary = %q", p.Summary)
	}
	if len(p.Changes) != 1 {
		t.Fatalf("len(changes) = %d", len(p.Changes))
	}
	c := p.Changes[0]
	if c.Op != "delete" || c.Resource != "device" || c.ID != "id1" || c.Name != "ap1" {
		t.Fatalf("change = %+v", c)
	}
	if c.Before == nil || c.After != nil {
		t.Fatal("delete should have before only")
	}
}

func TestRiskClassRequiresForce(t *testing.T) {
	tests := []struct {
		risk plan.RiskClass
		want bool
	}{
		{risk: plan.Routine, want: false},
		{risk: plan.HighImpact, want: true},
		{risk: plan.Destructive, want: true},
	}
	for _, tt := range tests {
		if !tt.risk.Valid() {
			t.Errorf("%s must be a valid risk class", tt.risk)
		}
		if got := tt.risk.RequiresForce(); got != tt.want {
			t.Errorf("%s RequiresForce() = %v, want %v", tt.risk, got, tt.want)
		}
	}
	if plan.RiskClass("invalid").Valid() {
		t.Fatal("unknown risk class must be invalid")
	}
}

func TestPreparedMutationBindsImmutableTargetAndSnapshot(t *testing.T) {
	snapshot := map[string]any{"id": "device-1", "name": "Office AP"}
	p := plan.Update("device", "device-1", "Office AP", "rename device", snapshot, map[string]any{"name": "Lobby AP"})
	prepared, err := plan.Targeted(p, "device-1", snapshot, plan.HighImpact, true)
	if err != nil {
		t.Fatal(err)
	}

	// Mutating the preparation source must not mutate the bound target snapshot.
	snapshot["name"] = "Retargeted AP"
	target, ok := prepared.Target()
	if !ok {
		t.Fatal("targeted mutation lost its prepared target")
	}
	if target.ID() != "device-1" {
		t.Fatalf("target ID = %q, want device-1", target.ID())
	}
	matchesOriginal, err := target.Matches(map[string]any{"id": "device-1", "name": "Office AP"})
	if err != nil {
		t.Fatal(err)
	}
	if !matchesOriginal {
		t.Fatal("prepared snapshot changed when its source map was mutated")
	}
	matchesRetargeted, err := target.Matches(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if matchesRetargeted {
		t.Fatal("prepared snapshot unexpectedly matches later source mutation")
	}
	if prepared.Risk() != plan.HighImpact || !prepared.Experimental() {
		t.Fatalf("prepared contract risk=%s experimental=%v", prepared.Risk(), prepared.Experimental())
	}
}

func TestTargetedMutationRejectsEmptyTargetID(t *testing.T) {
	_, err := plan.Targeted(plan.Plan{}, "", map[string]any{"name": "AP"}, plan.Routine, false)
	if err == nil {
		t.Fatal("targeted mutation accepted an empty immutable target ID")
	}
}
