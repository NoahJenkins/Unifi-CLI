package cli

import (
	"reflect"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/plan"
)

func TestTask7MutationRiskTableIsExactAndExperimental(t *testing.T) {
	want := map[string]plan.RiskClass{
		"device rename": plan.Routine, "device locate": plan.Routine, "device adopt": plan.Routine,
		"client reconnect": plan.Routine, "client block": plan.Routine, "client unblock": plan.Routine,
		"network create": plan.Routine, "wlan create": plan.Routine,
		"wlan update": plan.Routine, "wlan enable": plan.Routine, "wlan disable": plan.Routine,
		"device restart": plan.HighImpact, "device upgrade": plan.HighImpact,
		"client fixed-ip set": plan.HighImpact, "client fixed-ip clear": plan.HighImpact,
		"network update": plan.HighImpact, "port update": plan.HighImpact, "dns resolvers set": plan.HighImpact,
		"device forget": plan.Destructive, "network delete": plan.Destructive, "wlan delete": plan.Destructive,
	}
	if !reflect.DeepEqual(task7MutationRisk, want) {
		t.Fatalf("risk table = %#v, want %#v", task7MutationRisk, want)
	}
	if len(task7ExperimentalMutations) != len(want) {
		t.Fatalf("experimental table entries = %d, want %d", len(task7ExperimentalMutations), len(want))
	}
	for mutation := range want {
		if !task7ExperimentalMutations[mutation] {
			t.Errorf("%s is not experimental", mutation)
		}
	}
}
