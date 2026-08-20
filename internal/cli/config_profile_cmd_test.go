package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/cli"
)

func TestConfigProfileCommandsAndGlobalFlagAreDiscoverable(t *testing.T) {
	root := cli.NewRoot()
	if root.PersistentFlags().Lookup("profile") == nil {
		t.Fatal("root is missing --profile")
	}
	for _, args := range [][]string{
		{"config", "profile", "list"},
		{"config", "profile", "show"},
		{"config", "profile", "select"},
	} {
		command, _, err := root.Find(args)
		if err != nil || command.Name() != args[len(args)-1] {
			t.Fatalf("command %v unavailable: command=%v err=%v", args, command, err)
		}
	}
}

func TestConfigProfileListReportsValidAndInvalidProfiles(t *testing.T) {
	home := prepareProfileHome(t)
	profilesDir := filepath.Join(home, ".config", "unifi-cli", "profiles")
	writeTestFile(t, filepath.Join(profilesDir, "home.yaml"), "host: 192.0.2.10\nca_cert: /tmp/lab-ca.pem\n")
	writeTestFile(t, filepath.Join(profilesDir, "broken.yaml"), "host: [\n")
	writeTestFile(t, filepath.Join(home, ".config", "unifi-cli", "current-profile"), "home\n")

	out, execErr := executeCLIWithStdout(t, "--json", "config", "profile", "list")
	if execErr != nil {
		t.Fatalf("profile list: %v\n%s", execErr, out)
	}
	var envelope struct {
		Data []struct {
			Name     string `json:"name"`
			Selected bool   `json:"selected"`
			Valid    bool   `json:"valid"`
			Error    string `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode profile list: %v\n%s", err, out)
	}
	if len(envelope.Data) != 2 || envelope.Data[0].Name != "broken" || envelope.Data[0].Valid || envelope.Data[0].Error == "" || envelope.Data[1].Name != "home" || !envelope.Data[1].Valid || !envelope.Data[1].Selected {
		t.Fatalf("unexpected profile list: %+v", envelope.Data)
	}
}

func TestConfigProfileShowSelectPathAndEffectiveConfig(t *testing.T) {
	home := prepareProfileHome(t)
	configHome := filepath.Join(home, ".config", "unifi-cli")
	profilePath := filepath.Join(configHome, "profiles", "home.yaml")
	writeTestFile(t, profilePath, "host: 192.0.2.10\nport: 8443\ninsecure: true\nsite: lab\nsafe_mode: true\ntimeout: 45s\n")

	selectOut, execErr := executeCLIWithStdout(t, "config", "profile", "select", "home")
	if execErr != nil {
		t.Fatalf("profile select: %v\n%s", execErr, selectOut)
	}
	marker, err := os.ReadFile(filepath.Join(configHome, "current-profile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != "home\n" {
		t.Fatalf("selected marker = %q", marker)
	}

	pathOut, execErr := executeCLIWithStdout(t, "config", "path")
	if execErr != nil || strings.TrimSpace(pathOut) != profilePath {
		t.Fatalf("config path: err=%v out=%q want=%q", execErr, pathOut, profilePath)
	}

	showOut, execErr := executeCLIWithStdout(t, "--profile", "home", "config", "show")
	if execErr != nil {
		t.Fatalf("config show: %v\n%s", execErr, showOut)
	}
	for _, expected := range []string{"profile: home", "path: " + profilePath, "host: 192.0.2.10", "ca_cert:"} {
		if !strings.Contains(showOut, expected) {
			t.Fatalf("config show missing %q:\n%s", expected, showOut)
		}
	}
	for _, forbidden := range []string{"api_key", "password", "username"} {
		if strings.Contains(showOut, forbidden) {
			t.Fatalf("config show contains secret field %q:\n%s", forbidden, showOut)
		}
	}

	profileOut, execErr := executeCLIWithStdout(t, "--json", "config", "profile", "show")
	if execErr != nil {
		t.Fatalf("profile show: %v\n%s", execErr, profileOut)
	}
	if !strings.Contains(profileOut, `"profile": "home"`) || !strings.Contains(profileOut, `"host": "192.0.2.10"`) || !strings.Contains(profileOut, `"valid": true`) || !strings.Contains(profileOut, `"selected": true`) {
		t.Fatalf("profile show missing effective data:\n%s", profileOut)
	}
}

func TestConfigAndProfileSelectorsFailClosed(t *testing.T) {
	home := prepareProfileHome(t)
	profilePath := filepath.Join(home, ".config", "unifi-cli", "profiles", "home.yaml")
	configPath := filepath.Join(home, "explicit.yaml")
	writeTestFile(t, profilePath, "host: 192.0.2.10\n")
	writeTestFile(t, configPath, "host: 192.0.2.11\n")

	out, execErr := executeCLIWithStdout(t, "--json", "--config", configPath, "--profile", "home", "config", "show")
	if execErr == nil {
		t.Fatalf("conflicting selectors unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, `"code": "validation_failed"`) || !strings.Contains(out, "choose only one") {
		t.Fatalf("unexpected conflict output:\n%s", out)
	}
}

func prepareProfileHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("UNIFI_CONFIG", "")
	t.Setenv("UNIFI_PROFILE", "")
	t.Setenv("UNIFI_HOST", "")
	t.Setenv("UNIFI_PORT", "")
	t.Setenv("UNIFI_INSECURE", "")
	t.Setenv("UNIFI_CA_CERT", "")
	t.Setenv("UNIFI_SITE", "")
	t.Setenv("UNIFI_SAFE_MODE", "")
	t.Setenv("UNIFI_TIMEOUT", "")
	t.Setenv("UNIFI_USERNAME", "")
	t.Setenv("UNIFI_PASSWORD", "")
	if err := os.MkdirAll(filepath.Join(home, ".config", "unifi-cli", "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func executeCLIWithStdout(t *testing.T, args ...string) (string, error) {
	t.Helper()
	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldOut })

	root := cli.NewRoot()
	var commandOutput bytes.Buffer
	root.SetOut(&commandOutput)
	root.SetErr(&commandOutput)
	root.SetArgs(args)
	execErr := root.Execute()
	_ = w.Close()
	os.Stdout = oldOut
	var runtimeOutput bytes.Buffer
	_, _ = runtimeOutput.ReadFrom(r)
	_ = r.Close()
	return runtimeOutput.String() + commandOutput.String(), execErr
}
