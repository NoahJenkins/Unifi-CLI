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
