package cli

import (
	"fmt"
	"io"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
)

type Runtime struct {
	Cfg          config.Config
	ConfigPath   string
	Profile      string
	Client       *client.Client
	JSON         bool
	Yes          bool
	DryRun       bool
	Force        bool
	Quiet        bool
	Experimental bool
	// CommandExperimental records the prepared mutation classification for
	// schema-v1 output independently of whether the operator passed the apply
	// opt-in flag.
	CommandExperimental bool
	Site                string
	Out                 io.Writer
	Err                 io.Writer
}

func (rt *Runtime) Applying() bool {
	return rt.Yes && !rt.DryRun
}

func (rt *Runtime) Emit(resource, action string, data any, p *plan.Plan, err error) int {
	site := rt.Site
	if site == "" {
		site = rt.Cfg.Site
	}
	if err != nil {
		if rt.JSON {
			env := render.Fail(resource, action, site, err)
			env.Meta.Experimental = rt.CommandExperimental
			_ = render.WriteJSON(rt.Out, env)
		} else {
			fmt.Fprintln(rt.Err, render.SafeText(err.Error()))
		}
		return render.ExitCode(err)
	}
	dry := p != nil && !rt.Applying()
	env := render.Success(resource, action, site, data, dry)
	env.Meta.Experimental = rt.CommandExperimental
	if p != nil && dry {
		env.Plan = p
		env.Data = nil
	}
	if rt.JSON {
		_ = render.WriteJSON(rt.Out, env)
	} else if p != nil && dry {
		fmt.Fprintf(rt.Out, "DRY-RUN: %s\n", render.SafeText(p.Summary))
		rows := make([][]string, 0, len(p.Changes))
		for _, c := range p.Changes {
			rows = append(rows, []string{c.Op, c.Resource, c.ID, c.Name})
		}
		_ = render.WriteTable(rt.Out, []string{"OP", "RESOURCE", "ID", "NAME"}, rows)
	} else if data != nil {
		printData(rt.Out, data)
	}
	return 0
}

func printData(w io.Writer, data any) {
	switch v := data.(type) {
	case domain.ClientFixedIPReservation:
		fmt.Fprintf(w, "client_id: %s\n", render.SafeText(v.ClientID))
		fmt.Fprintf(w, "mac: %s\n", render.SafeText(v.MAC))
		fmt.Fprintf(w, "name: %s\n", render.SafeText(v.Name))
		fmt.Fprintf(w, "network_id: %s\n", render.SafeText(v.NetworkID))
		fmt.Fprintf(w, "fixed_ip_enabled: %t\n", v.FixedIPEnabled)
		fmt.Fprintf(w, "fixed_ip: %s\n", render.SafeText(v.FixedIP))
	case DoctorResult:
		fmt.Fprintf(w, "version: %s\n", render.SafeText(v.Version))
		fmt.Fprintf(w, "commit: %s\n", render.SafeText(v.Commit))
		fmt.Fprintf(w, "config_path: %s\n", render.SafeText(v.ConfigPath))
		fmt.Fprintf(w, "profile: %s\n", render.SafeText(v.Profile))
		fmt.Fprintf(w, "host: %s\n", render.SafeText(v.Host))
		fmt.Fprintf(w, "site: %s\n", render.SafeText(v.Site))
		fmt.Fprintf(w, "tls_mode: %s\n", render.SafeText(v.TLSMode))
		fmt.Fprintf(w, "credential_source: %s\n", render.SafeText(v.CredentialSource))
		fmt.Fprintf(w, "ready: %t\n", v.Ready)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		// stable-ish order for common fields first
		order := []string{"profile", "path", "host", "site", "auth_method", "port", "insecure", "ca_cert", "safe_mode", "timeout", "selected", "valid"}
		seen := map[string]bool{}
		for _, k := range order {
			if val, ok := v[k]; ok {
				fmt.Fprintf(w, "%s: %s\n", k, render.SafeText(fmt.Sprint(val)))
				seen[k] = true
			}
		}
		for _, k := range keys {
			if !seen[k] {
				fmt.Fprintf(w, "%s: %s\n", k, render.SafeText(fmt.Sprint(v[k])))
			}
		}
	case map[string]string:
		seen := map[string]bool{}
		for _, k := range []string{"host", "site", "auth_method", "path"} {
			if val, ok := v[k]; ok {
				fmt.Fprintf(w, "%s: %s\n", k, render.SafeText(val))
				seen[k] = true
			}
		}
		for k, val := range v {
			if !seen[k] {
				fmt.Fprintf(w, "%s: %s\n", k, render.SafeText(val))
			}
		}
	default:
		fmt.Fprintln(w, render.SafeText(fmt.Sprint(data)))
	}
}
