package releasepipeline_test

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// The documented live smoke run exports controller settings before its unit
// test stage (UNIFI_IT=1 ./scripts/smoke.sh). Unit tests must not inherit them.
func TestUnitTestsIgnoreOperatorControllerEnvironment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1",
		"-skip", "^TestUnitTestsIgnoreOperatorControllerEnvironment$",
		".", "./internal/cli", "./internal/client", "./internal/config")
	cmd.Env = append(os.Environ(),
		"UNIFI_IT=1",
		"UNIFI_HOST=controller.invalid",
		"UNIFI_PORT=8443",
		"UNIFI_SITE=operator-site",
		"UNIFI_PROFILE=operator-profile",
		"UNIFI_CONFIG="+t.TempDir()+"/operator.yaml",
		"UNIFI_INSECURE=1",
		"UNIFI_CA_CERT="+t.TempDir()+"/operator-ca.pem",
		"UNIFI_SAFE_MODE=false",
		"UNIFI_TIMEOUT=1s",
		"UNIFI_API_KEY=synthetic-operator-key",
		"UNIFI_USERNAME=operator",
		"UNIFI_PASSWORD=synthetic-operator-password",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unit tests inherited operator controller settings: %v\n%s", err, output)
	}
}
