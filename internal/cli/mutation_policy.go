package cli

import "github.com/noahjenkins/unifi-cli/internal/plan"

// task7MutationRisk is the exhaustive approved risk table for the remaining
// controller mutations. Keys are the frozen resource/action envelope values.
var task7MutationRisk = map[string]plan.RiskClass{
	"device rename": plan.Routine, "device locate": plan.Routine, "device adopt": plan.Routine,
	"client reconnect": plan.Routine, "client block": plan.Routine, "client unblock": plan.Routine,
	"network create": plan.Routine, "wlan create": plan.Routine,
	"wlan update": plan.Routine, "wlan enable": plan.Routine, "wlan disable": plan.Routine,
	"device restart": plan.HighImpact, "device upgrade": plan.HighImpact,
	"network update": plan.HighImpact, "port update": plan.HighImpact, "dns resolvers set": plan.HighImpact,
	"device forget": plan.Destructive, "network delete": plan.Destructive, "wlan delete": plan.Destructive,
}

var task7ExperimentalMutations = func() map[string]bool {
	out := make(map[string]bool, len(task7MutationRisk))
	for mutation := range task7MutationRisk {
		out[mutation] = true
	}
	return out
}()

func task7MutationPolicy(resource, action string) (plan.RiskClass, bool) {
	key := resource + " " + action
	return task7MutationRisk[key], task7ExperimentalMutations[key]
}
