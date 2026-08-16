package domain_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

func TestFirewallMovePlansOneAtomicRelativeOrderingWrite(t *testing.T) {
	api := newModernFirewallAPI(t)
	api.orderingReads = []domain.FirewallOrdering{
		{BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}},
		{BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}},
		{BeforeSystemDefined: []string{blockWebPolicyID, allowDNSPolicyID}, AfterSystemDefined: []string{}},
	}
	svc := domain.NewFirewallService(api)
	in := domain.FirewallMove{Policy: "Block Web", Before: "Allow DNS"}
	p, binding, err := svc.PrepareMove(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if binding.PolicyID != blockWebPolicyID || binding.ReferencePolicyID != allowDNSPolicyID || !binding.Before {
		t.Fatalf("move binding = %#v", binding)
	}
	after := p.Changes[0].After.(map[string]any)
	if !reflect.DeepEqual(after["before_system_defined"], []string{blockWebPolicyID, allowDNSPolicyID}) || !reflect.DeepEqual(after["after_system_defined"], []string{}) {
		t.Fatalf("move plan after = %#v", after)
	}
	got, err := svc.ApplyMoveBound(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.BeforeSystemDefined, []string{blockWebPolicyID, allowDNSPolicyID}) || len(got.AfterSystemDefined) != 0 {
		t.Fatalf("observed move = %#v", got)
	}
	puts := firewallCalls(api.calls, http.MethodPut)
	wantBody := map[string]any{"orderedFirewallPolicyIds": map[string]any{
		"beforeSystemDefined": []string{blockWebPolicyID, allowDNSPolicyID}, "afterSystemDefined": []string{},
	}}
	if len(puts) != 1 || !reflect.DeepEqual(puts[0].body, wantBody) {
		t.Fatalf("move writes = %#v, want body %#v", puts, wantBody)
	}
}

func TestFirewallMoveValidatesRelativeTargetAndZonePair(t *testing.T) {
	tests := []struct {
		name string
		in   domain.FirewallMove
		code apperr.Code
	}{
		{name: "missing relation", in: domain.FirewallMove{Policy: "Allow DNS"}, code: apperr.ValidationFailed},
		{name: "both relations", in: domain.FirewallMove{Policy: "Allow DNS", Before: "Block Web", After: "Block Web"}, code: apperr.ValidationFailed},
		{name: "same policy", in: domain.FirewallMove{Policy: "Allow DNS", Before: "Allow DNS"}, code: apperr.ValidationFailed},
		{name: "system policy", in: domain.FirewallMove{Policy: systemGuardPolicyID, Before: "Allow DNS"}, code: apperr.ValidationFailed},
		{name: "missing reference", in: domain.FirewallMove{Policy: "Allow DNS", Before: "Missing"}, code: apperr.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newModernFirewallAPI(t)
			_, _, err := domain.NewFirewallService(api).PrepareMove(context.Background(), tt.in)
			if !apperr.Is(err, tt.code) {
				t.Fatalf("error = %v, want %s", err, tt.code)
			}
			if len(firewallMutationCalls(api.calls)) != 0 {
				t.Fatalf("invalid move wrote: %#v", api.calls)
			}
		})
	}

	t.Run("different zone pair", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		for _, policy := range api.policies {
			if policy["id"] == blockWebPolicyID {
				policy["destination"].(map[string]any)["zoneId"] = internalZoneID
			}
		}
		_, _, err := domain.NewFirewallService(api).PrepareMove(context.Background(), domain.FirewallMove{Policy: "Allow DNS", After: "Block Web"})
		if !apperr.Is(err, apperr.ValidationFailed) {
			t.Fatalf("zone mismatch error = %v", err)
		}
	})
}

func TestFirewallMoveRejectsSystemInjectionAndPreparedDrift(t *testing.T) {
	t.Run("ordering contains system policy", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.orderingReads = []domain.FirewallOrdering{{
			BeforeSystemDefined: []string{allowDNSPolicyID, systemGuardPolicyID}, AfterSystemDefined: []string{blockWebPolicyID},
		}}
		_, _, err := domain.NewFirewallService(api).PrepareMove(context.Background(), domain.FirewallMove{Policy: "Block Web", Before: "Allow DNS"})
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("system injection error = %v", err)
		}
		if len(firewallCalls(api.calls, http.MethodPut)) != 0 {
			t.Fatalf("system injection wrote: %#v", api.calls)
		}
	})

	t.Run("ordering changed after plan", func(t *testing.T) {
		api := newModernFirewallAPI(t)
		api.orderingReads = []domain.FirewallOrdering{
			{BeforeSystemDefined: []string{allowDNSPolicyID}, AfterSystemDefined: []string{blockWebPolicyID}},
			{BeforeSystemDefined: []string{blockWebPolicyID}, AfterSystemDefined: []string{allowDNSPolicyID}},
		}
		svc := domain.NewFirewallService(api)
		p, binding, err := svc.PrepareMove(context.Background(), domain.FirewallMove{Policy: "Block Web", Before: "Allow DNS"})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := plan.Targeted(p, "move", p.Changes, plan.HighImpact, true)
		if err != nil {
			t.Fatal(err)
		}
		target, _ := prepared.Target()
		_, err = svc.ApplyMovePrepared(context.Background(), target, binding)
		if !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("prepared drift error = %v", err)
		}
		if len(firewallCalls(api.calls, http.MethodPut)) != 0 {
			t.Fatalf("prepared drift wrote: %#v", api.calls)
		}
	})
}
