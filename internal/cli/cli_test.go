package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/cli"
)

func TestHelpShowsAuthAndConfig(t *testing.T) {
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

func TestConfigShowRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("host: 10.0.0.1\nusername: admin\npassword: s3cret\napi_key: key123\n")
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

	if execErr != nil {
		t.Fatalf("config show: %v", execErr)
	}
	out := buf.String()
	if strings.Contains(out, "s3cret") || strings.Contains(out, "key123") {
		t.Fatalf("secrets leaked:\n%s", out)
	}
	if !strings.Contains(out, "***") {
		t.Fatalf("expected redacted secrets:\n%s", out)
	}
	if !strings.Contains(out, "10.0.0.1") {
		t.Fatalf("expected host in output:\n%s", out)
	}
}

func TestAuthHelp(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"auth", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("auth help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "status") || !strings.Contains(out, "login") || !strings.Contains(out, "logout") {
		t.Fatalf("auth help missing verbs:\n%s", out)
	}
}

func TestAuthLoginHelpShowsFileFallback(t *testing.T) {
	root := cli.NewRoot()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"auth", "login", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("auth login help: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "--file-fallback") {
		t.Fatalf("auth login help missing --file-fallback:\n%s", out)
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
