package cli

import (
	"fmt"
	"io"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/render"
)

type Runtime struct {
	Cfg    config.Config
	Client *client.Client
	JSON   bool
	Yes    bool
	DryRun bool
	Force  bool
	Quiet  bool
	Raw    bool
	Site   string
	Out    io.Writer
	Err    io.Writer
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
			_ = render.WriteJSON(rt.Out, render.Fail(resource, action, site, err))
		} else {
			fmt.Fprintln(rt.Err, render.SafeText(err.Error()))
		}
		return render.ExitCode(err)
	}
	dry := p != nil && !rt.Applying()
	env := render.Success(resource, action, site, data, dry)
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
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		// stable-ish order for common fields first
		order := []string{"host", "site", "auth_method", "path", "port", "insecure", "safe_mode", "timeout"}
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
