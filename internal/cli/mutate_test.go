package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/cli"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
)

func preparedTarget(t *testing.T, risk plan.RiskClass, experimental bool) plan.PreparedMutation {
	t.Helper()
	snapshot := map[string]any{"id": "ap1", "name": "AP-Office"}
	p := plan.Update("device", "ap1", "AP-Office", "restart device AP-Office",
		snapshot, map[string]any{"action": "restart"})
	prepared, err := plan.Targeted(p, "ap1", snapshot, risk, experimental)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func mutationRuntime() (*cli.Runtime, *bytes.Buffer, *bytes.Buffer) {
	out, errBuf := new(bytes.Buffer), new(bytes.Buffer)
	return &cli.Runtime{
		JSON: true,
		Out:  out,
		Err:  errBuf,
		Site: "default",
		Cfg:  config.Config{Site: "default", SafeMode: true},
	}, out, errBuf
}

func TestRunPreparedMutationRequiresYes(t *testing.T) {
	rt, out, errBuf := mutationRuntime()
	observed, applied := false, false
	code := cli.RunPreparedMutation(rt, "device", "restart",
		func() (plan.PreparedMutation, error) { return preparedTarget(t, plan.Routine, false), nil },
		func(target plan.Target) (any, error) {
			observed = true
			return nil, nil
		},
		func(target plan.Target) (any, error) {
			applied = true
			return nil, nil
		},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if observed || applied {
		t.Fatalf("plan inspection observed=%v applied=%v; neither may run without --yes", observed, applied)
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Plan == nil || !env.Meta.DryRun || env.Data != nil {
		t.Fatalf("expected plan envelope with data present as null: %+v", env)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("no audit without apply: %q", errBuf.String())
	}
}

func TestRunPreparedMutationDryRunWinsOverAllApplyGates(t *testing.T) {
	rt, out, _ := mutationRuntime()
	rt.Yes = true
	rt.DryRun = true
	observed, applied := false, false
	code := cli.RunPreparedMutation(rt, "device", "forget",
		func() (plan.PreparedMutation, error) { return preparedTarget(t, plan.Destructive, true), nil },
		func(target plan.Target) (any, error) {
			observed = true
			return nil, nil
		},
		func(target plan.Target) (any, error) {
			applied = true
			return nil, nil
		},
	)
	if code != 0 {
		t.Fatalf("exit = %d output=%s", code, out.String())
	}
	if observed || applied {
		t.Fatalf("--dry-run must win before experimental, force, observe, and apply; observed=%v applied=%v", observed, applied)
	}
}

func TestRunPreparedMutationRiskGates(t *testing.T) {
	tests := []struct {
		name        string
		risk        plan.RiskClass
		force       bool
		wantApplied bool
		wantCode    apperr.Code
	}{
		{name: "routine", risk: plan.Routine, wantApplied: true},
		{name: "high impact blocked", risk: plan.HighImpact, wantCode: apperr.SafeModeBlocked},
		{name: "high impact forced", risk: plan.HighImpact, force: true, wantApplied: true},
		{name: "destructive blocked", risk: plan.Destructive, wantCode: apperr.SafeModeBlocked},
		{name: "destructive forced", risk: plan.Destructive, force: true, wantApplied: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, out, _ := mutationRuntime()
			rt.Yes = true
			rt.Force = tt.force
			applied := false
			code := cli.RunPreparedMutation(rt, "device", "restart",
				func() (plan.PreparedMutation, error) { return preparedTarget(t, tt.risk, false), nil },
				func(target plan.Target) (any, error) {
					return map[string]any{"id": "ap1", "name": "AP-Office"}, nil
				},
				func(target plan.Target) (any, error) {
					applied = true
					return map[string]any{"id": target.ID()}, nil
				},
			)
			if applied != tt.wantApplied {
				t.Fatalf("applied = %v, want %v", applied, tt.wantApplied)
			}
			var env render.Envelope
			if err := json.Unmarshal(out.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if tt.wantCode == "" {
				if code != 0 || !env.OK {
					t.Fatalf("exit=%d envelope=%+v", code, env)
				}
			} else if code == 0 || env.Error == nil || env.Error.Code != string(tt.wantCode) {
				t.Fatalf("exit=%d error=%+v, want %s", code, env.Error, tt.wantCode)
			}
		})
	}
}

func TestRunPreparedMutationExperimentalGateOnlyBlocksApply(t *testing.T) {
	rt, out, _ := mutationRuntime()
	rt.Yes = true
	applied := false
	code := cli.RunPreparedMutation(rt, "device", "rename",
		func() (plan.PreparedMutation, error) { return preparedTarget(t, plan.Routine, true), nil },
		func(target plan.Target) (any, error) {
			t.Fatal("experimental gate must run before revalidation")
			return nil, nil
		},
		func(target plan.Target) (any, error) {
			applied = true
			return nil, nil
		},
	)
	if code == 0 || applied {
		t.Fatalf("experimental apply without --experimental: exit=%d applied=%v", code, applied)
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != string(apperr.ValidationFailed) {
		t.Fatalf("error = %+v", env.Error)
	}

	rt, _, _ = mutationRuntime()
	rt.Yes = true
	rt.Experimental = true
	code = cli.RunPreparedMutation(rt, "device", "rename",
		func() (plan.PreparedMutation, error) { return preparedTarget(t, plan.Routine, true), nil },
		func(target plan.Target) (any, error) {
			return map[string]any{"id": "ap1", "name": "AP-Office"}, nil
		},
		func(target plan.Target) (any, error) {
			applied = true
			return "done", nil
		},
	)
	if code != 0 || !applied {
		t.Fatalf("experimental apply with opt-in: exit=%d applied=%v", code, applied)
	}
}

func TestRunPreparedMutationRejectsChangedSnapshot(t *testing.T) {
	rt, out, _ := mutationRuntime()
	rt.Yes = true
	applied := false
	code := cli.RunPreparedMutation(rt, "device", "rename",
		func() (plan.PreparedMutation, error) { return preparedTarget(t, plan.Routine, false), nil },
		func(target plan.Target) (any, error) {
			return map[string]any{"id": "ap1", "name": "Lobby AP"}, nil
		},
		func(target plan.Target) (any, error) {
			applied = true
			return nil, nil
		},
	)
	if code == 0 || applied {
		t.Fatalf("changed target snapshot: exit=%d applied=%v", code, applied)
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != string(apperr.Conflict) {
		t.Fatalf("error = %+v, want conflict", env.Error)
	}
}

func TestRunPreparedMutationApplyConsumesPreparedTargetAfterNameRace(t *testing.T) {
	rt, _, _ := mutationRuntime()
	rt.Yes = true
	resolvedID := "device-1"
	var appliedID string
	code := cli.RunPreparedMutation(rt, "device", "rename",
		func() (plan.PreparedMutation, error) {
			snapshot := map[string]any{"id": resolvedID, "name": "Office AP"}
			p := plan.Update("device", resolvedID, "Office AP", "rename", snapshot, map[string]any{"name": "Lobby AP"})
			prepared, err := plan.Targeted(p, resolvedID, snapshot, plan.Routine, false)
			resolvedID = "device-2" // the same name now resolves elsewhere before apply
			return prepared, err
		},
		func(target plan.Target) (any, error) {
			if target.ID() != "device-1" {
				t.Fatalf("revalidation target = %q, want prepared device-1", target.ID())
			}
			return map[string]any{"id": "device-1", "name": "Office AP"}, nil
		},
		func(target plan.Target) (any, error) {
			appliedID = target.ID()
			return map[string]any{"id": target.ID()}, nil
		},
	)
	if code != 0 || appliedID != "device-1" {
		t.Fatalf("exit=%d applied target=%q, want immutable device-1", code, appliedID)
	}
}

func TestRunPreparedMutationQuietSkipsAudit(t *testing.T) {
	rt, _, errBuf := mutationRuntime()
	rt.Yes = true
	rt.Quiet = true
	code := cli.RunPreparedMutation(rt, "device", "restart",
		func() (plan.PreparedMutation, error) { return preparedTarget(t, plan.Routine, false), nil },
		func(target plan.Target) (any, error) {
			return map[string]any{"id": "ap1", "name": "AP-Office"}, nil
		},
		func(target plan.Target) (any, error) { return "ok", nil },
	)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("quiet must suppress audit: %q", errBuf.String())
	}
}

func TestRunPreparedMutationRejectsInvalidRisk(t *testing.T) {
	rt, out, _ := mutationRuntime()
	rt.Yes = true
	code := cli.RunPreparedMutation(rt, "device", "rename",
		func() (plan.PreparedMutation, error) {
			return preparedTarget(t, plan.RiskClass("unknown"), false), nil
		},
		nil,
		func(target plan.Target) (any, error) { return nil, nil },
	)
	if code == 0 {
		t.Fatal("invalid risk class was allowed to apply")
	}
	if !strings.Contains(out.String(), "validation_failed") {
		t.Fatalf("output = %q", out.String())
	}
}
