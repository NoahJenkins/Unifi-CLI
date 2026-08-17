package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type fixedIPCommandServer struct {
	server *httptest.Server
	puts   atomic.Int32
}

func newFixedIPCommandServer(t *testing.T, enabled bool, fixedIP string) *fixedIPCommandServer {
	t.Helper()
	fixture := &fixedIPCommandServer{}
	user := map[string]any{
		"_id": "legacy-client-1", "mac": "aa:bb:cc:dd:ee:02", "name": "Laptop",
		"network_id": "legacy-network-1", "use_fixedip": enabled,
	}
	if fixedIP != "" {
		user["fixed_ip"] = fixedIP
	}
	connected := []map[string]any{{
		"_id": "legacy-client-1", "mac": "aa:bb:cc:dd:ee:02", "name": "Laptop",
		"ip": "192.0.2.20", "network_id": "legacy-network-1",
	}}

	fixture.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != commandTestAPIKey {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		writeOfficialPage := func(data string) {
			_, _ = io.WriteString(w, `{"offset":0,"limit":100,"count":1,"totalCount":1,"data":`+data+`}`)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/integration/v1/sites":
			writeOfficialPage(`[{"id":"11111111-1111-4111-8111-111111111111","internalReference":"default","name":"Default"}]`)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/clients":
			writeOfficialPage(`[{"type":"WIRELESS","id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1","macAddress":"aa:bb:cc:dd:ee:02","name":"Laptop","ipAddress":"192.0.2.20","access":{"type":"DEFAULT"},"uplinkDeviceId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"}]`)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/stat/sta":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": connected})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/rest/user/legacy-client-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{user}})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/rest/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{user}})
			return
		case r.Method == http.MethodGet && r.URL.Path == "/proxy/network/api/s/default/rest/networkconf/legacy-network-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
				"_id": "legacy-network-1", "name": "LAN", "ip_subnet": "192.0.2.1/24", "dhcpd_enabled": true,
			}}})
			return
		case r.Method == http.MethodPut && r.URL.Path == "/proxy/network/api/s/default/rest/user/legacy-client-1":
			fixture.puts.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
				return
			}
			for key, value := range body {
				user[key] = value
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
		}
	}))
	return fixture
}

func TestClientFixedIPCommandsAreExposed(t *testing.T) {
	clientCmd := newClientCmd()
	for _, path := range [][]string{{"fixed-ip", "set"}, {"fixed-ip", "clear"}} {
		cmd, _, err := clientCmd.Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if cmd.Name() != path[len(path)-1] {
			t.Fatalf("find %v returned %q", path, cmd.Name())
		}
	}
}

func TestClientFixedIPSetRendersExactPlanAndApplyJSON(t *testing.T) {
	fixture := newFixedIPCommandServer(t, false, "")
	defer fixture.server.Close()
	useCommandTestRuntime(t, fixture.server, true)

	stdout, stderr, err := captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("set", commandClientID, "192.0.2.50")
	})
	if err != nil || stderr != "" {
		t.Fatalf("plan: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	wantPlan := `{"schema_version":"1","ok":true,"resource":"client","action":"fixed-ip set","data":null,"meta":{"site":"default","dry_run":true,"experimental":true},"plan":{"summary":"set fixed IP for client Laptop","changes":[{"op":"update","resource":"client","id":"legacy-client-1","name":"Laptop","before":{"client_id":"legacy-client-1","mac":"aabbccddee02","name":"Laptop","network_id":"legacy-network-1","fixed_ip_enabled":false,"fixed_ip":""},"after":{"client_id":"legacy-client-1","mac":"aabbccddee02","name":"Laptop","network_id":"legacy-network-1","fixed_ip_enabled":true,"fixed_ip":"192.0.2.50"}}]}}`
	assertDecodedJSONEqual(t, stdout, wantPlan)
	if strings.Contains(stdout+stderr, commandTestAPIKey) {
		t.Fatal("plan output leaked API key")
	}
	if fixture.puts.Load() != 0 {
		t.Fatalf("plan PUT count = %d, want 0", fixture.puts.Load())
	}

	flagYes = true
	flagExperimental = true
	flagForce = true
	stdout, stderr, err = captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("set", commandClientID, "192.0.2.50")
	})
	if err != nil {
		t.Fatalf("apply: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	wantApply := `{"schema_version":"1","ok":true,"resource":"client","action":"fixed-ip set","data":{"client_id":"legacy-client-1","mac":"aabbccddee02","name":"Laptop","network_id":"legacy-network-1","fixed_ip_enabled":true,"fixed_ip":"192.0.2.50"},"meta":{"site":"default","dry_run":false,"experimental":true}}`
	assertDecodedJSONEqual(t, stdout, wantApply)
	if strings.Contains(stdout+stderr, commandTestAPIKey) {
		t.Fatal("apply output leaked API key")
	}
	if stderr != "audit: applied client fixed-ip set\n" || fixture.puts.Load() != 1 {
		t.Fatalf("audit=%q PUT count=%d", stderr, fixture.puts.Load())
	}
}

func TestClientFixedIPDryRunWinsOverApplyFlags(t *testing.T) {
	fixture := newFixedIPCommandServer(t, false, "")
	defer fixture.server.Close()
	useCommandTestRuntime(t, fixture.server, true)
	flagYes = true
	flagDryRun = true
	flagExperimental = true
	flagForce = true

	stdout, stderr, err := captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("set", "Laptop", "192.0.2.50")
	})
	var envelope map[string]any
	decodeErr := json.Unmarshal([]byte(stdout), &envelope)
	meta, _ := envelope["meta"].(map[string]any)
	if err != nil || decodeErr != nil || stderr != "" || meta["dry_run"] != true {
		t.Fatalf("dry-run: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if fixture.puts.Load() != 0 {
		t.Fatalf("dry-run PUT count = %d, want 0", fixture.puts.Load())
	}
}

func TestClientFixedIPClearRendersVerifiedApplyJSON(t *testing.T) {
	fixture := newFixedIPCommandServer(t, true, "192.0.2.50")
	defer fixture.server.Close()
	useCommandTestRuntime(t, fixture.server, true)
	flagYes = true
	flagExperimental = true
	flagForce = true

	stdout, stderr, err := captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("clear", "Laptop", "")
	})
	if err != nil {
		t.Fatalf("apply: err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	want := `{"schema_version":"1","ok":true,"resource":"client","action":"fixed-ip clear","data":{"client_id":"legacy-client-1","mac":"aabbccddee02","name":"Laptop","network_id":"legacy-network-1","fixed_ip_enabled":false,"fixed_ip":""},"meta":{"site":"default","dry_run":false,"experimental":true}}`
	assertDecodedJSONEqual(t, stdout, want)
	if stderr != "audit: applied client fixed-ip clear\n" || fixture.puts.Load() != 1 {
		t.Fatalf("audit=%q PUT count=%d", stderr, fixture.puts.Load())
	}
}

func TestClientFixedIPApplyRequiresExperimentalAndForce(t *testing.T) {
	fixture := newFixedIPCommandServer(t, false, "")
	defer fixture.server.Close()
	useCommandTestRuntime(t, fixture.server, true)
	flagYes = true
	flagForce = true

	stdout, _, err := captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("set", "Laptop", "192.0.2.50")
	})
	if err == nil || !strings.Contains(stdout, "experimental mutation requires") {
		t.Fatalf("without experimental: err=%v stdout=%q", err, stdout)
	}

	flagExperimental = true
	flagForce = false
	stdout, _, err = captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("set", "Laptop", "192.0.2.50")
	})
	if err == nil || !strings.Contains(stdout, "safe_mode_blocked") {
		t.Fatalf("without force: err=%v stdout=%q", err, stdout)
	}
	if fixture.puts.Load() != 0 {
		t.Fatalf("blocked apply PUT count = %d, want 0", fixture.puts.Load())
	}
}

func TestClientFixedIPHumanOutputIsDeterministic(t *testing.T) {
	fixture := newFixedIPCommandServer(t, false, "")
	defer fixture.server.Close()
	useCommandTestRuntime(t, fixture.server, false)
	flagYes = true
	flagExperimental = true
	flagForce = true

	stdout, _, err := captureProcessOutput(t, func() error {
		return runClientFixedIPMutation("set", "Laptop", "192.0.2.50")
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "client_id: legacy-client-1\nmac: aabbccddee02\nname: Laptop\nnetwork_id: legacy-network-1\nfixed_ip_enabled: true\nfixed_ip: 192.0.2.50\n"
	if stdout != want {
		t.Fatalf("human output = %q, want %q", stdout, want)
	}
}
