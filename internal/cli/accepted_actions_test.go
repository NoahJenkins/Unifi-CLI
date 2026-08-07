package cli

import (
	"strings"
	"testing"
)

func TestAcceptedDeviceActionsRenderExactSchemaV1JSON(t *testing.T) {
	tests := []struct {
		name, action string
		force        bool
		run          func() error
	}{
		{name: "official restart", action: "restart", force: true, run: func() error {
			return runDeviceMutation("restart", commandDeviceID, "")
		}},
		{name: "official adopt", action: "adopt", run: func() error {
			return runDeviceMutation("adopt", "aa:bb:cc:dd:ee:99", "")
		}},
		{name: "official forget", action: "forget", force: true, run: func() error {
			return runDeviceMutation("forget", commandDeviceID, "")
		}},
		{name: "legacy locate", action: "locate", run: func() error {
			return runDeviceMutation("locate", "dev-1", "")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCommandTestServer(t)
			defer srv.Close()
			useCommandTestRuntime(t, srv, true)
			flagYes = true
			flagExperimental = true
			flagForce = tt.force

			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("action failed: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			want := `{"schema_version":"1","ok":true,"resource":"device","action":"` + tt.action + `","data":{"accepted":true},"meta":{"site":"default","dry_run":false}}`
			assertDecodedJSONEqual(t, stdout, want)
			if stderr != "audit: applied device "+tt.action+"\n" {
				t.Fatalf("audit stderr = %q", stderr)
			}
			if strings.Contains(stdout+stderr, commandTestAPIKey) {
				t.Fatal("action output leaked API key")
			}
		})
	}
}
