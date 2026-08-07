package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/domain"
)

const commandTestAPIKey = "command-test-api-key-not-for-output"

func newCommandTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	responses := map[string]string{
		"/proxy/network/api/self/sites":                              `[{"_id":"site-1","name":"default","desc":"Primary","role":"admin"}]`,
		"/proxy/network/integration/v1/sites":                        `[{"id":"site-uuid","internalReference":"default","name":"Default"}]`,
		"/proxy/network/integration/v1/sites/site-uuid/dns/policies": `[{"id":"dns-1","type":"A_RECORD","domain":"router.example.test","ipv4Address":"192.0.2.1","enabled":true,"ttlSeconds":300,"metadata":{"origin":"USER"}}]`,
		"/proxy/network/api/s/default/stat/device":                   `[{"_id":"dev-1","mac":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","type":"ugw","state":1,"adopted":true,"ip":"192.0.2.1","version":"1.0","port_table":[{"port_idx":1,"name":"LAN 1","media":"GE","speed":1000,"poe_mode":"off","enable":true,"portconf_id":"profile-1"}]}]`,
		"/proxy/network/api/s/default/rest/device/dev-1":             `[{"_id":"dev-1","name":"Gateway","port_overrides":[]}]`,
		"/proxy/network/api/s/default/stat/sta":                      `[{"_id":"client-1","mac":"aa:bb:cc:dd:ee:02","hostname":"Laptop","name":"Laptop","ip":"192.0.2.2","essid":"Main","network":"LAN","is_wired":false,"blocked":false,"last_seen":"now"}]`,
		"/proxy/network/api/s/default/rest/networkconf":              `[{"_id":"network-1","name":"LAN","purpose":"corporate","vlan":10,"ip_subnet":"192.0.2.1/24","dhcpd_enabled":true}]`,
		"/proxy/network/api/s/default/rest/wlanconf":                 `[{"_id":"wlan-1","name":"Main","enabled":true,"security":"wpapsk","networkconf_id":"network-1","wlan_band":"both","is_guest":false}]`,
		"/proxy/network/api/s/default/rest/firewallrule":             `[{"_id":"rule-1","name":"Allow DNS","enabled":true,"action":"accept","ruleset":"LAN_IN","src_firewallgroup_ids":[],"dst_firewallgroup_ids":[],"protocol":"udp","rule_index":1}]`,
		"/proxy/network/api/s/default/stat/health":                   `[{"subsystem":"www","status":"ok"}]`,
		"/proxy/network/api/s/default/cmd/devmgr":                    `[]`,
		"/proxy/network/api/s/default/cmd/stamgr":                    `[]`,
		"/proxy/network/api/s/default/rest/networkconf/network-1":    `[]`,
		"/proxy/network/api/s/default/rest/wlanconf/wlan-1":          `[]`,
		"/proxy/network/api/s/default/rest/firewallrule/rule-1":      `[]`,
	}
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != commandTestAPIKey {
			t.Errorf("X-API-KEY = %q", got)
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/proxy/network/integration/v1/sites/site-uuid/dns/policies" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"id":"dns-new","type":"A_RECORD","domain":"service.example.test","ipv4Address":"192.0.2.20","enabled":true,"ttlSeconds":300,"metadata":{"origin":"USER"}}}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/proxy/network/integration/v1/sites" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"offset":0,"limit":100,"count":1,"totalCount":1,"data":`+responses[r.URL.Path]+`}`)
			return
		}
		data, ok := responses[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":`+data+`}`)
	}))
}

func useCommandTestRuntime(t *testing.T, srv *httptest.Server, jsonOutput bool) {
	t.Helper()
	useAuthCommandConfig(t, srv)
	t.Setenv("UNIFI_API_KEY", commandTestAPIKey)
	flagJSON = jsonOutput
	flagYes = false
	flagDryRun = false
	flagForce = false
	flagQuiet = false
	flagExperimental = false
	t.Cleanup(func() {
		flagYes = false
		flagDryRun = false
		flagForce = false
		flagQuiet = false
		flagExperimental = false
	})
}

func TestResourceReadCommandsRenderHumanAndJSONOutput(t *testing.T) {
	srv := newCommandTestServer(t)
	defer srv.Close()

	tests := []struct {
		name, resource, action, humanMarker string
		run                                 func() error
	}{
		{name: "site list", resource: "site", action: "list", humanMarker: "Primary", run: runSiteList},
		{name: "site get", resource: "site", action: "get", humanMarker: "id: site-1", run: func() error { return runSiteGet("site-1") }},
		{name: "device list", resource: "device", action: "list", humanMarker: "Gateway", run: runDeviceList},
		{name: "device get", resource: "device", action: "get", humanMarker: "id: dev-1", run: func() error { return runDeviceGet("dev-1") }},
		{name: "client list", resource: "client", action: "list", humanMarker: "Laptop", run: runClientList},
		{name: "client get", resource: "client", action: "get", humanMarker: "id: client-1", run: func() error { return runClientGet("client-1") }},
		{name: "network list", resource: "network", action: "list", humanMarker: "LAN", run: runNetworkList},
		{name: "network get", resource: "network", action: "get", humanMarker: "id: network-1", run: func() error { return runNetworkGet("network-1") }},
		{name: "wlan list", resource: "wlan", action: "list", humanMarker: "Main", run: runWlanList},
		{name: "wlan get", resource: "wlan", action: "get", humanMarker: "id: wlan-1", run: func() error { return runWlanGet("wlan-1") }},
		{name: "port list", resource: "port", action: "list", humanMarker: "LAN 1", run: func() error { return runPortList("") }},
		{name: "port get", resource: "port", action: "get", humanMarker: "port_idx: 1", run: func() error { return runPortGet("dev-1", 1) }},
		{name: "firewall list", resource: "firewall", action: "list", humanMarker: "Allow DNS", run: runFirewallList},
		{name: "firewall get", resource: "firewall", action: "get", humanMarker: "id: rule-1", run: func() error { return runFirewallGet("rule-1") }},
		{name: "dns list", resource: "dns", action: "list", humanMarker: "router.example.test", run: runDNSList},
		{name: "dns get", resource: "dns", action: "get", humanMarker: "id: dns-1", run: func() error { return runDNSGet("dns-1") }},
		{name: "dns resolvers", resource: "dns", action: "resolvers list", humanMarker: "LAN", run: runDNSResolversList},
		{name: "system health", resource: "system", action: "health", humanMarker: "status: ok", run: runSystemHealth},
	}

	for _, tt := range tests {
		t.Run(tt.name+" human", func(t *testing.T) {
			useCommandTestRuntime(t, srv, false)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			if !strings.Contains(stdout, tt.humanMarker) {
				t.Fatalf("stdout lacks %q: %q", tt.humanMarker, stdout)
			}
			if strings.Contains(stdout+stderr, commandTestAPIKey) {
				t.Fatal("output leaked API key")
			}
		})

		t.Run(tt.name+" json", func(t *testing.T) {
			useCommandTestRuntime(t, srv, true)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			var envelope struct {
				OK       bool   `json:"ok"`
				Resource string `json:"resource"`
				Action   string `json:"action"`
			}
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatalf("invalid JSON stdout: %v; stdout=%q", err, stdout)
			}
			if !envelope.OK || envelope.Resource != tt.resource || envelope.Action != tt.action {
				t.Fatalf("envelope = %+v", envelope)
			}
			if strings.Contains(stdout+stderr, commandTestAPIKey) {
				t.Fatal("output leaked API key")
			}
		})
	}
}

func TestResourceMutationCommandsRenderPlansAndApply(t *testing.T) {
	srv := newCommandTestServer(t)
	defer srv.Close()

	tests := []struct {
		name   string
		run    func() error
		secret string
	}{
		{name: "device rename", run: func() error { return runDeviceMutation("rename", "dev-1", false, "Gateway Renamed") }},
		{name: "client block", run: func() error { return runClientMutation("block", "client-1") }},
		{name: "network create", run: func() error { return runNetworkCreate(domain.NetworkInput{Name: "Guest", Purpose: "corporate"}) }},
		{name: "wlan create", secret: "wlan-plan-secret-not-for-output", run: func() error {
			return runWlanCreate(domain.WlanInput{Name: "Guest WiFi", Security: "wpapsk", Password: "wlan-plan-secret-not-for-output"})
		}},
		{name: "port update", run: func() error {
			return runPortUpdate("dev-1", 1, domain.PortInput{Name: "Uplink"})
		}},
		{name: "firewall create", run: func() error {
			return runFirewallCreate(domain.FirewallInput{Name: "Allow HTTPS", Action: "accept", Ruleset: "LAN_IN", Protocol: "tcp"})
		}},
		{name: "dns create", run: func() error {
			return runDNSCreate(domain.DNSInput{Name: "service.example.test", IP: "192.0.2.20"})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name+" plan", func(t *testing.T) {
			useCommandTestRuntime(t, srv, false)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("plan: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "DRY-RUN:") {
				t.Fatalf("plan output missing dry-run marker: %q", stdout)
			}
			for _, protected := range []string{commandTestAPIKey, tt.secret} {
				if protected != "" && strings.Contains(stdout+stderr, protected) {
					t.Fatalf("plan output leaked protected content: stdout=%q stderr=%q", stdout, stderr)
				}
			}
		})

		t.Run(tt.name+" apply", func(t *testing.T) {
			useCommandTestRuntime(t, srv, true)
			flagYes = true
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("apply: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			if !strings.Contains(stderr, "audit: applied") {
				t.Fatalf("apply output missing audit marker: stdout=%q stderr=%q", stdout, stderr)
			}
			for _, protected := range []string{commandTestAPIKey, tt.secret} {
				if protected != "" && strings.Contains(stdout+stderr, protected) {
					t.Fatalf("apply output leaked protected content: stdout=%q stderr=%q", stdout, stderr)
				}
			}
		})
	}
}
