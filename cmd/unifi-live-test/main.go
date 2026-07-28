package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/livetest"
)

type config struct {
	Binary    string
	ReportDir string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	runner := livetest.Runner{Binary: cfg.Binary, Executor: livetest.OSExecutor{}, Now: time.Now}
	report, runErr := runner.Run(context.Background())
	path, reportErr := livetest.WriteReport(cfg.ReportDir, report)
	if reportErr != nil {
		fmt.Fprintln(stderr, "could not write live test report")
		return 1
	}
	for _, result := range report.Results {
		fmt.Fprintf(stdout, "%s %s\n", statusLabel(result.Status), result.Command)
	}
	fmt.Fprintf(stdout, "report: %s\n", path)
	if runErr != nil {
		return 1
	}
	return 0
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("unifi-live-test", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg config
	flags.StringVar(&cfg.Binary, "binary", "", "path to the unifi binary")
	flags.StringVar(&cfg.ReportDir, "report-dir", "dist/test-reports", "directory for redacted reports")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.Binary == "" {
		return config{}, fmt.Errorf("--binary is required")
	}
	if len(flags.Args()) != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	return cfg, nil
}

func statusLabel(status livetest.Status) string {
	switch status {
	case livetest.Pass:
		return "PASS"
	case livetest.NotConfigured:
		return "NOT CONFIGURED"
	default:
		return "FAIL"
	}
}
