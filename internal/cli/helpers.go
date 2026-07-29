package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
)

func loadRuntime(needClient bool) (*Runtime, error) {
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return baseRuntime(), err
	}
	if flagSite != "" {
		cfg.Site = flagSite
	}
	if flagTimeout != "" {
		d, err := time.ParseDuration(flagTimeout)
		if err != nil {
			return baseRuntime(), apperr.Newf(apperr.ValidationFailed, "invalid --timeout: %v", err)
		}
		cfg.Timeout = d
	}

	rt := baseRuntime()
	rt.Cfg = cfg
	rt.Site = cfg.Site

	if needClient {
		c, err := client.New(cfg)
		if err != nil {
			return rt, err
		}
		rt.Client = c
	}
	return rt, nil
}

func baseRuntime() *Runtime {
	return &Runtime{
		JSON:   flagJSON,
		Yes:    flagYes,
		DryRun: flagDryRun,
		Force:  flagForce,
		Quiet:  flagQuiet,
		Raw:    flagRaw,
		Site:   flagSite,
		Out:    os.Stdout,
		Err:    os.Stderr,
	}
}

func emitErr(resource, action string, err error) error {
	rt := baseRuntime()
	_ = rt.Emit(resource, action, nil, nil, err)
	return err
}

// exitCodeError carries a process exit code after Emit already wrote output.
// Execute maps it to the numeric code without re-printing.
type exitCodeError int

func (e exitCodeError) Error() string {
	return fmt.Sprintf("exit code %d", int(e))
}

// emittedExit returns nil for success, or an exitCodeError when Emit already
// displayed the failure (avoids collapsing validation=2 into generic exit 1).
func emittedExit(code int) error {
	if code == 0 {
		return nil
	}
	return exitCodeError(code)
}

// exitStatus maps a cobra RunE error to a process exit code.
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	if c, ok := err.(exitCodeError); ok {
		return int(c)
	}
	return render.ExitCode(err)
}

// RunMutation builds a plan, and only applies when rt.Applying() (Yes && !DryRun).
// Destructive ops also require !SafeMode or Force.
func RunMutation(
	rt *Runtime,
	resource, action string,
	destructive bool,
	build func() (plan.Plan, any, error),
	apply func() (any, error),
) int {
	p, _, err := build()
	if err != nil {
		return rt.Emit(resource, action, nil, nil, err)
	}
	if !rt.Applying() {
		return rt.Emit(resource, action, nil, &p, nil)
	}
	if destructive && rt.Cfg.SafeMode && !rt.Force {
		return rt.Emit(resource, action, nil, nil,
			apperr.New(apperr.SafeModeBlocked, "destructive operation blocked by safe_mode; pass --force --yes"))
	}
	result, err := apply()
	if err == nil && !rt.Quiet {
		fmt.Fprintf(rt.Err, "audit: applied %s %s\n", resource, action)
	}
	return rt.Emit(resource, action, result, nil, err)
}
