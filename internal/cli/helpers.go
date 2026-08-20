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

var (
	newRuntimeClient = client.New
	newProfileStore  = func() *config.ProfileStore {
		return config.NewProfileStore(config.ProfileOptions{})
	}
)

func loadRuntime(needClient bool) (*Runtime, error) {
	store := newProfileStore()
	selection, err := config.ResolveSelection(flagConfig, flagProfile, store)
	if err != nil {
		return baseRuntime(), apperr.Newf(apperr.ValidationFailed, "%v", err)
	}
	var cfg config.Config
	if selection.Profile != "" {
		_, cfg, err = store.Show(selection.Profile)
	} else {
		cfg, err = config.Load(selection.Path)
	}
	if err != nil {
		if selection.Profile != "" {
			return baseRuntime(), apperr.WithCause(
				apperr.WithHint(
					apperr.Newf(apperr.ValidationFailed, "profile %q is invalid at %s", selection.Profile, selection.Path),
					"run 'unifi config profile list' and fix or select a valid profile",
				),
				err,
			)
		}
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
	rt.ConfigPath = selection.Path
	rt.Profile = selection.Profile
	rt.Site = cfg.Site

	if needClient {
		c, err := newRuntimeClient(cfg)
		if err != nil {
			return rt, err
		}
		rt.Client = c
	}
	return rt, nil
}

func baseRuntime() *Runtime {
	return &Runtime{
		JSON:         flagJSON,
		Yes:          flagYes,
		DryRun:       flagDryRun,
		Force:        flagForce,
		Quiet:        flagQuiet,
		Experimental: flagExperimental,
		Site:         flagSite,
		Out:          os.Stdout,
		Err:          os.Stderr,
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

// RunPreparedMutation prepares a mutation once, preserving the immutable
// target identity and snapshot through revalidation and apply. Targeted
// mutations must provide observe; apply always receives the prepared target.
func RunPreparedMutation(
	rt *Runtime,
	resource, action string,
	prepare func() (plan.PreparedMutation, error),
	observe func(plan.Target) (any, error),
	apply func(plan.Target) (any, error),
) int {
	prepared, err := prepare()
	if err != nil {
		return rt.Emit(resource, action, nil, nil, err)
	}
	rt.CommandExperimental = prepared.Experimental()
	p := prepared.Plan()
	if !rt.Applying() {
		return rt.Emit(resource, action, nil, &p, nil)
	}
	if prepared.Experimental() && !rt.Experimental {
		return rt.Emit(resource, action, nil, nil,
			apperr.New(apperr.ValidationFailed, "experimental mutation requires --experimental --yes"))
	}
	if !prepared.Risk().Valid() {
		return rt.Emit(resource, action, nil, nil,
			apperr.Newf(apperr.ValidationFailed, "invalid mutation risk class %q", prepared.Risk()))
	}
	if prepared.Risk().RequiresForce() && rt.Cfg.SafeMode && !rt.Force {
		return rt.Emit(resource, action, nil, nil,
			apperr.Newf(apperr.SafeModeBlocked, "%s operation blocked by safe_mode; pass --force --yes", prepared.Risk()))
	}

	target, targeted := prepared.Target()
	if targeted {
		if observe == nil {
			return rt.Emit(resource, action, nil, nil,
				apperr.New(apperr.Internal, "targeted mutation is missing pre-apply revalidation"))
		}
		current, err := observe(target)
		if err != nil {
			return rt.Emit(resource, action, nil, nil, err)
		}
		matches, err := target.Matches(current)
		if err != nil {
			return rt.Emit(resource, action, nil, nil, apperr.WithCause(
				apperr.New(apperr.Internal, "could not compare prepared target state"), err))
		}
		if !matches {
			return rt.Emit(resource, action, nil, nil, apperr.WithHint(
				apperr.New(apperr.Conflict, "target changed after the mutation plan was prepared"),
				"rerun the command to inspect a fresh plan"))
		}
	}
	if apply == nil {
		return rt.Emit(resource, action, nil, nil,
			apperr.New(apperr.Internal, "prepared mutation is missing apply"))
	}
	result, err := apply(target)
	if err == nil && !rt.Quiet {
		fmt.Fprintf(rt.Err, "audit: applied %s %s\n", resource, action)
	}
	return rt.Emit(resource, action, result, nil, err)
}
