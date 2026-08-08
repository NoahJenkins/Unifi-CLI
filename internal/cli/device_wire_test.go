package cli

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/plan"
)

func TestEmittedExitPreservesValidationCode(t *testing.T) {
	// runDeviceMutation used to return fmt.Errorf → Execute → exit 1 always.
	var out bytes.Buffer
	rt := &Runtime{
		JSON: true,
		Yes:  true,
		Out:  &out,
		Err:  new(bytes.Buffer),
		Site: "default",
		Cfg:  config.Config{Site: "default"},
	}
	code := RunPreparedMutation(rt, "device", "rename",
		func() (plan.PreparedMutation, error) {
			return plan.PreparedMutation{}, apperr.New(apperr.ValidationFailed, "name required")
		},
		nil,
		func(target plan.Target) (any, error) {
			t.Fatal("apply must not run on build error")
			return nil, nil
		},
	)
	if code != 2 {
		t.Fatalf("RunPreparedMutation exit = %d, want 2", code)
	}
	if got := exitStatus(emittedExit(code)); got != 2 {
		t.Fatalf("emittedExit→exitStatus = %d, want 2", got)
	}
	// Collapsed generic error (old bug) maps to 1:
	if got := exitStatus(fmt.Errorf("device rename failed")); got != 1 {
		t.Fatalf("plain error exitStatus = %d, want 1", got)
	}
	if exitStatus(emittedExit(0)) != 0 {
		t.Fatal("success must stay 0")
	}
}
