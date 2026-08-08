package cli

import (
	"fmt"

	"github.com/noahjenkins/unifi-cli/internal/buildinfo"
	"github.com/noahjenkins/unifi-cli/internal/render"
	"github.com/spf13/cobra"
)

const unofficialProjectDisclaimer = "**Unofficial project.** unifi-cli is an independent community tool and is not affiliated with, endorsed by, or sponsored by Ubiquiti Inc. UniFi is a trademark of Ubiquiti Inc."

var (
	flagConfig       string
	flagJSON         bool
	flagYes          bool
	flagDryRun       bool
	flagForce        bool
	flagQuiet        bool
	flagExperimental bool
	flagSite         string
	flagTimeout      string
)

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "unifi",
		Short:         "Manage a UniFi network via the local controller API",
		Long:          "Manage a UniFi network via the local controller API.\n\n" + unofficialProjectDisclaimer,
		Version:       buildinfo.Version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("unifi version {{.Version}}\n")
	root.PersistentFlags().StringVar(&flagConfig, "config", "", "config file path")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON output")
	root.PersistentFlags().BoolVar(&flagYes, "yes", false, "apply mutations")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "plan only; never apply")
	root.PersistentFlags().BoolVar(&flagForce, "force", false, "override safe_mode blocks")
	root.PersistentFlags().BoolVar(&flagQuiet, "quiet", false, "suppress audit stderr")
	root.PersistentFlags().BoolVar(&flagExperimental, "experimental", false, "allow applying experimental mutations")
	root.PersistentFlags().StringVar(&flagSite, "site", "", "site override")
	root.PersistentFlags().StringVar(&flagTimeout, "timeout", "", "per-command timeout")
	root.AddCommand(
		newAuthCmd(),
		newLoginCmd(),
		newLogoutCmd(),
		newConfigCmd(),
		newDeviceCmd(),
		newClientCmd(),
		newSiteCmd(),
		newNetworkCmd(),
		newWlanCmd(),
		newPortCmd(),
		newFirewallCmd(),
		newDNSCmd(),
		newSystemCmd(),
		newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version and build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := buildinfo.Current()
			if flagJSON {
				return render.WriteJSON(cmd.OutOrStdout(), render.Success("version", "show", "", info, false))
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "version: %s\ncommit: %s\nbuild_date: %s\ngo_version: %s\n",
				info.Version, info.Commit, info.BuildDate, info.GoVersion)
			return err
		},
	}
}

func Execute() int {
	root := NewRoot()
	err := root.Execute()
	return exitStatus(err)
}
