package cli

import (
	"os"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
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
