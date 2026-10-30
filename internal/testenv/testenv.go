// Package testenv isolates unit tests from operator controller settings.
package testenv

import "os"

// controllerVariables select, authenticate, or reach a controller. Test-only
// switches such as UNIFI_RACE and UNIFI_NATIVE_KEYRING_IT are not included.
var controllerVariables = []string{
	"UNIFI_IT",
	"UNIFI_HOST",
	"UNIFI_PORT",
	"UNIFI_SITE",
	"UNIFI_PROFILE",
	"UNIFI_CONFIG",
	"UNIFI_INSECURE",
	"UNIFI_CA_CERT",
	"UNIFI_SAFE_MODE",
	"UNIFI_TIMEOUT",
	"UNIFI_API_KEY",
	"UNIFI_USERNAME",
	"UNIFI_PASSWORD",
}

// ClearController unsets every controller setting, so unit tests behave the
// same when an operator exports a live configuration, such as for
// UNIFI_IT=1 ./scripts/smoke.sh. Call it from TestMain before m.Run.
func ClearController() {
	for _, name := range controllerVariables {
		_ = os.Unsetenv(name)
	}
}
