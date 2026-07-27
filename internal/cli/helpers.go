package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/plan"
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
