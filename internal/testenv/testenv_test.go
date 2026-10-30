package testenv_test

import (
	"os"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/testenv"
)

func TestClearControllerKeepsTestSwitches(t *testing.T) {
	t.Setenv("UNIFI_HOST", "controller.invalid")
	t.Setenv("UNIFI_API_KEY", "synthetic")
	t.Setenv("UNIFI_IT", "1")
	t.Setenv("UNIFI_NATIVE_KEYRING_IT", "1")
	t.Setenv("UNIFI_RACE", "1")

	testenv.ClearController()

	for _, name := range []string{"UNIFI_HOST", "UNIFI_API_KEY", "UNIFI_IT"} {
		if value, ok := os.LookupEnv(name); ok {
			t.Fatalf("%s = %q after ClearController", name, value)
		}
	}
	for _, name := range []string{"UNIFI_NATIVE_KEYRING_IT", "UNIFI_RACE"} {
		if os.Getenv(name) != "1" {
			t.Fatalf("%s was cleared; test switches must stay", name)
		}
	}
}
