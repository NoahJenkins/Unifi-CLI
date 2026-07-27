package cli

import (
	"github.com/spf13/cobra"
)

var (
	flagConfig  string
	flagJSON    bool
	flagYes     bool
	flagDryRun  bool
	flagForce   bool
	flagQuiet   bool
	flagRaw     bool
	flagSite    string
	flagTimeout string
)

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "unifi",
		Short:         "Manage a UniFi network via the local controller API",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&flagConfig, "config", "", "config file path")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON output")
	root.PersistentFlags().BoolVar(&flagYes, "yes", false, "apply mutations")
	root.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "plan only; never apply")
	root.PersistentFlags().BoolVar(&flagForce, "force", false, "override safe_mode blocks")
	root.PersistentFlags().BoolVar(&flagQuiet, "quiet", false, "suppress audit stderr")
	root.PersistentFlags().BoolVar(&flagRaw, "raw", false, "include raw controller payload in JSON")
	root.PersistentFlags().StringVar(&flagSite, "site", "", "site override")
	root.PersistentFlags().StringVar(&flagTimeout, "timeout", "", "per-command timeout")
	root.AddCommand(
		newAuthCmd(),
		newConfigCmd(),
		newDeviceCmd(),
		newClientCmd(),
		newSiteCmd(),
		newNetworkCmd(),
		newWlanCmd(),
		newSystemCmd(),
	)
	return root
}

func Execute() int {
	root := NewRoot()
	err := root.Execute()
	return exitStatus(err)
}
