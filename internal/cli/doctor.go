package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/buildinfo"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

type DoctorResult struct {
	Version          string `json:"version"`
	Commit           string `json:"commit"`
	ConfigPath       string `json:"config_path"`
	Profile          string `json:"profile"`
	Host             string `json:"host"`
	Site             string `json:"site"`
	TLSMode          string `json:"tls_mode"`
	CredentialSource string `json:"credential_source"`
	Ready            bool   `json:"ready"`
}

func newDoctorCmd(info buildinfo.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check local configuration and credential readiness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := baseRuntime()
			rt.Out = cmd.OutOrStdout()
			rt.Err = cmd.ErrOrStderr()

			store := newProfileStore()
			selection, err := config.ResolveSelection(flagConfig, flagProfile, store)
			if err != nil {
				return doctorError(rt, nil, apperr.WithHint(
					apperr.New(apperr.ValidationFailed, "configuration selection is invalid"),
					"run 'unifi config profile list' or choose one config selector",
				))
			}
			var cfg config.Config
			if selection.Profile != "" {
				_, cfg, err = store.Show(selection.Profile)
			} else {
				cfg, err = config.Load(selection.Path)
			}
			if err != nil {
				return doctorError(rt, nil, apperr.WithCause(
					apperr.WithHint(
						apperr.Newf(apperr.ValidationFailed, "configuration is invalid at %s", selection.Path),
						"fix the config file, then rerun 'unifi doctor'",
					),
					err,
				))
			}

			result := DoctorResult{
				Version:    info.Version,
				Commit:     info.Commit,
				ConfigPath: selection.Path,
				Profile:    selection.Profile,
				Host:       cfg.Host,
				Site:       cfg.Site,
				TLSMode:    doctorTLSMode(cfg),
			}
			rt.Cfg = cfg
			rt.Site = cfg.Site

			switch {
			case os.Getenv("UNIFI_API_KEY") != "":
				result.CredentialSource = "environment_api_key"
			default:
				_, found, loadErr := newAuthStore().Load(cfg.BaseURL())
				switch {
				case loadErr == nil && found:
					result.CredentialSource = "saved_api_key"
				case loadErr == nil:
					result.CredentialSource = "missing"
					return doctorError(rt, &result, apperr.WithHint(
						apperr.New(apperr.NotAuthenticated, "no API key is available for the selected controller"),
						"run 'unifi login'",
					))
				case errors.Is(loadErr, authstore.ErrKeyringUnavailable):
					result.CredentialSource = "keyring_unavailable"
					return doctorError(rt, &result, apperr.WithCause(
						apperr.WithHint(
							apperr.New(apperr.Internal, "native credential store is unavailable"),
							"unlock the credential store or run 'unifi login --file-fallback'",
						),
						loadErr,
					))
				default:
					result.CredentialSource = "keyring_unavailable"
					return doctorError(rt, &result, apperr.WithCause(
						apperr.WithHint(
							apperr.New(apperr.Internal, "credential store check failed"),
							"check local credential storage, then rerun 'unifi doctor'",
						),
						loadErr,
					))
				}
			}

			if info.Version == "" || info.Commit == "" {
				return doctorError(rt, &result, apperr.New(apperr.Internal, "version metadata is incomplete"))
			}
			result.Ready = true
			return emittedExit(rt.Emit("doctor", "doctor", result, nil, nil))
		},
	}
}

func doctorTLSMode(cfg config.Config) string {
	if cfg.Insecure {
		return "insecure"
	}
	if cfg.CACert != "" {
		return "custom_ca"
	}
	return "system_roots"
}

func doctorError(rt *Runtime, result *DoctorResult, err error) error {
	if result == nil {
		return emittedExit(rt.Emit("doctor", "doctor", nil, nil, err))
	}
	if rt.JSON {
		site := rt.Site
		if site == "" {
			site = rt.Cfg.Site
		}
		envelope := render.Fail("doctor", "doctor", site, err)
		envelope.Data = *result
		_ = render.WriteJSON(rt.Out, envelope)
	} else {
		printData(rt.Out, *result)
		fmt.Fprintln(rt.Err, render.SafeText(err.Error()))
	}
	return emittedExit(render.ExitCode(err))
}
