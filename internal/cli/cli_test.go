package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/buildinfo"
	"github.com/noahjenkins/unifi-cli/internal/cli"
)

func TestRootHelpShowsPublicCommandsOnly(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "auth") {
		t.Fatalf("help missing auth:\n%s", out)
	}
	if !strings.Contains(out, "login") || !strings.Contains(out, "logout") {
		t.Fatalf("help missing top-level auth commands:\n%s", out)
	}
	if !strings.Contains(out, "config") {
		t.Fatalf("help missing config:\n%s", out)
	}
	if !strings.Contains(out, "device") {
		t.Fatalf("help missing device:\n%s", out)
	}
	if !strings.Contains(out, "client") {
		t.Fatalf("help missing client:\n%s", out)
	}
	if !strings.Contains(out, "site") {
		t.Fatalf("help missing site:\n%s", out)
	}
	if !strings.Contains(out, "system") {
		t.Fatalf("help missing system:\n%s", out)
	}
	if strings.Contains(out, "--no-session-write") {
		t.Fatalf("help retained obsolete session flag:\n%s", out)
	}
}

func TestRootHelpShowsExactUnofficialProjectDisclaimer(t *testing.T) {
	const disclaimer = "**Unofficial project.** unifi-cli is an independent community tool and is not affiliated with, endorsed by, or sponsored by Ubiquiti Inc. UniFi is a trademark of Ubiquiti Inc."

	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := buf.String()
	if strings.Count(out, disclaimer) != 1 {
		t.Fatalf("root help must contain the exact disclaimer once:\n%s", out)
	}
}

func TestRootFlagsRemoveRawAndExposeExperimental(t *testing.T) {
	root := cli.NewRoot()
	if root.PersistentFlags().Lookup("raw") != nil {
		t.Fatal("root must not expose the removed --raw flag")
	}
	if root.PersistentFlags().Lookup("experimental") == nil {
		t.Fatal("root must expose --experimental")
	}
}

func TestRootVersionFlag(t *testing.T) {
	originalVersion := buildinfo.Version
	buildinfo.Version = "v1.2.3-test"
	t.Cleanup(func() { buildinfo.Version = originalVersion })
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "unifi version v1.2.3-test" {
		t.Fatalf("--version output = %q, want %q", got, "unifi version v1.2.3-test")
	}
}

func TestVersionCommandJSON(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate
	buildinfo.Version = "v1.2.3-test"
	buildinfo.Commit = "abc123"
	buildinfo.BuildDate = "2026-08-07T12:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate = originalVersion, originalCommit, originalBuildDate
	})
	root := cli.NewRoot()
	t.Cleanup(func() { _ = root.PersistentFlags().Set("json", "false") })
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"version", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("version JSON: %v; output=%q", err, buf.String())
	}
	if got["schema_version"] != "1" || got["ok"] != true || got["resource"] != "version" || got["action"] != "show" {
		t.Fatalf("version envelope = %#v", got)
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("version data = %#v", got["data"])
	}
	wantFields := []string{"version", "commit", "build_date", "go_version"}
	if len(data) != len(wantFields) {
		t.Fatalf("version data fields = %#v, want exactly %v", data, wantFields)
	}
	for _, field := range wantFields {
		if value, exists := data[field]; !exists || value == "" {
			t.Fatalf("version data missing non-empty %q: %#v", field, data)
		}
	}
	if data["version"] != "v1.2.3-test" || data["commit"] != "abc123" || data["build_date"] != "2026-08-07T12:00:00Z" {
		t.Fatalf("version linker fields = %#v", data)
	}
}

func TestDeviceHelp(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"device", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("device list help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "List devices") {
		t.Fatalf("device list help:\n%s", out)
	}
	cmd, _, err := root.Find([]string{"device", "get"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Name() != "get" {
		t.Fatalf("got %q", cmd.Name())
	}
}

func TestSystemExposesOnlySupportedHealthCommand(t *testing.T) {
	root := cli.NewRoot()
	system, _, err := root.Find([]string{"system"})
	if err != nil {
		t.Fatal(err)
	}
	subcommands := system.Commands()
	if len(subcommands) != 1 || subcommands[0].Name() != "health" {
		names := make([]string, 0, len(subcommands))
		for _, command := range subcommands {
			names = append(names, command.Name())
		}
		t.Fatalf("system subcommands = %v, want only health", names)
	}
}

func TestConfigPath(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"config", "path"})
	// Capture via replacing is hard; Execute uses os.Stdout in commands.
	// Run through a subprocess-free path: just ensure command exists.
	cmd, _, err := root.Find([]string{"config", "path"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Name() != "path" {
		t.Fatalf("got %q", cmd.Name())
	}
}

func TestConfigShowRejectsLegacyCredentialsWithoutLeakingValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("host: 10.0.0.1\nusername: legacy-username\npassword: legacy-password\napi_key: legacy-api-key\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	// Isolate env so Load doesn't pick up caller secrets.
	t.Setenv("UNIFI_HOST", "")
	t.Setenv("UNIFI_PASSWORD", "")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USERNAME", "")
	t.Setenv("UNIFI_CONFIG", "")

	root := cli.NewRoot()
	// Commands write to os.Stdout; swap temporarily.
	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	root.SetArgs([]string{"--config", path, "config", "show"})
	execErr := root.Execute()

	_ = w.Close()
	os.Stdout = oldOut
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()

	if execErr == nil {
		t.Fatal("config show accepted legacy credentials")
	}
	out := buf.String()
	for _, secret := range []string{"legacy-username", "legacy-password", "legacy-api-key"} {
		if strings.Contains(execErr.Error(), secret) || strings.Contains(out, secret) {
			t.Fatalf("legacy credential leaked: %q", secret)
		}
	}
	if !strings.Contains(execErr.Error(), "no longer supported") || !strings.Contains(execErr.Error(), "unifi login") {
		t.Fatalf("config show error did not provide safe migration guidance: %v", execErr)
	}
}

func TestConfigShowPrintsOnlyNonSecretFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("host: 10.0.0.1\nport: 8443\ninsecure: true\nsite: default\nsafe_mode: true\ntimeout: 45s\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("UNIFI_HOST", "")
	t.Setenv("UNIFI_PASSWORD", "")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USERNAME", "")
	t.Setenv("UNIFI_CONFIG", "")

	root := cli.NewRoot()
	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	root.SetArgs([]string{"--config", path, "config", "show"})
	execErr := root.Execute()
	_ = w.Close()
	os.Stdout = oldOut
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	if execErr != nil {
		t.Fatalf("config show: %v", execErr)
	}

	out := buf.String()
	for _, required := range []string{"host:", "port:", "insecure:", "site:", "safe_mode:", "timeout:"} {
		if !strings.Contains(out, required) {
			t.Fatalf("config show missing %q:\n%s", required, out)
		}
	}
	for _, forbidden := range []string{"username", "password", "api_key", "--no-session-write"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("obsolete auth surface %q in output:\n%s", forbidden, out)
		}
	}
}

func TestAuthHelpOnlyShowsReadOnlyStatus(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"auth", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("auth help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "status") {
		t.Fatalf("auth help missing status:\n%s", out)
	}
	if strings.Contains(out, "login") || strings.Contains(out, "logout") {
		t.Fatalf("auth help retained mutating commands:\n%s", out)
	}
}

func TestLoginHelpShowsOnlyFileFallbackAuthOption(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"login", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("login help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "--file-fallback") {
		t.Fatalf("login help missing file fallback:\n%s", out)
	}
	if strings.Contains(out, "--api-key") {
		t.Fatalf("login help retained API-key flag:\n%s", out)
	}
}

func TestRuntimeApplying(t *testing.T) {
	rt := &cli.Runtime{Yes: true, DryRun: false}
	if !rt.Applying() {
		t.Fatal("expected applying")
	}
	rt.DryRun = true
	if rt.Applying() {
		t.Fatal("dry-run must win over yes")
	}
	rt.Yes = false
	rt.DryRun = false
	if rt.Applying() {
		t.Fatal("yes required")
	}
}
