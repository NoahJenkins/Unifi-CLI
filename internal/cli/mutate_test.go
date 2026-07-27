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

func TestRunMutationRequiresYes(t *testing.T) {
	var out, errBuf bytes.Buffer
	rt := &cli.Runtime{
		JSON: true,
		Yes:  false,
		Out:  &out,
		Err:  &errBuf,
		Site: "default",
		Cfg:  config.Config{Site: "default", SafeMode: true},
	}
	applied := false
	code := cli.RunMutation(rt, "device", "restart", false,
		func() (plan.Plan, any, error) {
			return plan.Update("device", "ap1", "AP-Office", "restart device AP-Office",
				map[string]any{"action": "none"},
				map[string]any{"action": "restart"},
			), map[string]any{"id": "ap1"}, nil
		},
		func() (any, error) {
			applied = true
			return map[string]any{"ok": true}, nil
		},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if applied {
		t.Fatal("apply must not run without --yes")
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.OK || env.Plan == nil {
		t.Fatalf("expected plan envelope: %+v", env)
	}
	if !env.Meta.DryRun {
		t.Fatal("expected dry_run true")
	}
	if !strings.Contains(env.Plan.Summary, "restart") {
		t.Fatalf("plan summary: %q", env.Plan.Summary)
	}
	if errBuf.Len() != 0 {
		t.Fatalf("no audit without apply: %q", errBuf.String())
	}
}

func TestRunMutationDryRunWinsOverYes(t *testing.T) {
	var out, errBuf bytes.Buffer
	rt := &cli.Runtime{
		JSON:   true,
		Yes:    true,
		DryRun: true,
		Out:    &out,
		Err:    &errBuf,
		Site:   "default",
		Cfg:    config.Config{Site: "default"},
	}
	applied := false
	code := cli.RunMutation(rt, "device", "restart", false,
		func() (plan.Plan, any, error) {
			return plan.Update("device", "ap1", "AP", "restart", nil, nil), nil, nil
		},
		func() (any, error) {
			applied = true
			return "done", nil
		},
	)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if applied {
		t.Fatal("--dry-run must win over --yes")
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Plan == nil || !env.Meta.DryRun {
		t.Fatalf("expected dry-run plan: %+v", env)
	}
}

func TestRunMutationAppliesWithYes(t *testing.T) {
	var out, errBuf bytes.Buffer
	rt := &cli.Runtime{
		JSON: true,
		Yes:  true,
		Out:  &out,
		Err:  &errBuf,
		Site: "default",
		Cfg:  config.Config{Site: "default", SafeMode: true},
	}
	applied := false
	code := cli.RunMutation(rt, "device", "restart", false,
		func() (plan.Plan, any, error) {
			return plan.Update("device", "ap1", "AP", "restart", nil, nil), map[string]any{"id": "ap1"}, nil
		},
		func() (any, error) {
			applied = true
			return map[string]any{"id": "ap1", "status": "restarted"}, nil
		},
	)
	if code != 0 {
		t.Fatalf("exit = %d out=%s", code, out.String())
	}
	if !applied {
		t.Fatal("apply should run")
	}
	if !strings.Contains(errBuf.String(), "audit: applied device restart") {
		t.Fatalf("expected audit on stderr: %q", errBuf.String())
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Plan != nil {
		t.Fatal("plan should be nil when applied")
	}
	if env.Meta.DryRun {
		t.Fatal("dry_run should be false when applied")
	}
}

func TestRunMutationQuietSkipsAudit(t *testing.T) {
	var out, errBuf bytes.Buffer
	rt := &cli.Runtime{
		JSON:  true,
		Yes:   true,
		Quiet: true,
		Out:   &out,
		Err:   &errBuf,
		Site:  "default",
		Cfg:   config.Config{Site: "default"},
	}
	_ = cli.RunMutation(rt, "device", "restart", false,
		func() (plan.Plan, any, error) {
			return plan.Update("device", "ap1", "AP", "restart", nil, nil), nil, nil
		},
		func() (any, error) { return "ok", nil },
	)
	if errBuf.Len() != 0 {
		t.Fatalf("quiet must suppress audit: %q", errBuf.String())
	}
}

func TestForgetSafeMode(t *testing.T) {
	var out bytes.Buffer
	rt := &cli.Runtime{
		JSON:  true,
		Yes:   true,
		Force: false,
		Out:   &out,
		Err:   new(bytes.Buffer),
		Site:  "default",
		Cfg:   config.Config{Site: "default", SafeMode: true},
	}
	applied := false
	code := cli.RunMutation(rt, "device", "forget", true,
		func() (plan.Plan, any, error) {
			return plan.Delete("device", "ap1", "AP", "forget device AP", map[string]any{"id": "ap1"}), nil, nil
		},
		func() (any, error) {
			applied = true
			return nil, nil
		},
	)
	if code == 0 {
		t.Fatal("expected non-zero exit for safe_mode_blocked")
	}
	if applied {
		t.Fatal("apply must not run when safe_mode blocks")
	}
	var env render.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.OK {
		t.Fatal("expected failure envelope")
	}
	if env.Error == nil || env.Error.Code != string(apperr.SafeModeBlocked) {
		t.Fatalf("error = %+v", env.Error)
	}
}

func TestForgetSafeModeWithForce(t *testing.T) {
	var out, errBuf bytes.Buffer
	rt := &cli.Runtime{
		JSON:  true,
		Yes:   true,
		Force: true,
		Out:   &out,
		Err:   &errBuf,
		Site:  "default",
		Cfg:   config.Config{Site: "default", SafeMode: true},
	}
	applied := false
	code := cli.RunMutation(rt, "device", "forget", true,
		func() (plan.Plan, any, error) {
			return plan.Delete("device", "ap1", "AP", "forget", nil), nil, nil
		},
		func() (any, error) {
			applied = true
			return map[string]any{"forgotten": true}, nil
		},
	)
	if code != 0 {
		t.Fatalf("exit = %d %s", code, out.String())
	}
	if !applied {
		t.Fatal("force+yes should apply forget")
	}
}
