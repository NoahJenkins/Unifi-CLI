package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/domain"
)

const commandTestAPIKey = "command-test-api-key-not-for-output"

func newCommandTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dnsPolicies := []map[string]any{{
		"id": "dns-1", "type": "A_RECORD", "domain": "router.example.test",
		"ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": float64(300),
	}}
	responses := map[string]string{
		"/proxy/network/api/self/sites":                           `[{"_id":"site-1","name":"default","desc":"Primary","role":"admin"}]`,
		"/proxy/network/api/s/default/stat/device":                `[{"_id":"dev-1","mac":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","type":"ugw","state":1,"adopted":true,"ip":"192.0.2.1","version":"1.0","port_table":[{"port_idx":1,"name":"LAN 1","media":"GE","speed":1000,"poe_mode":"off","enable":true,"portconf_id":"profile-1"}]}]`,
		"/proxy/network/api/s/default/rest/device/dev-1":          `[{"_id":"dev-1","name":"Gateway","port_overrides":[]}]`,
		"/proxy/network/api/s/default/stat/sta":                   `[{"_id":"client-1","mac":"aa:bb:cc:dd:ee:02","hostname":"Laptop","name":"Laptop","ip":"192.0.2.2","essid":"Main","network":"LAN","is_wired":false,"blocked":false,"last_seen":"now"}]`,
		"/proxy/network/api/s/default/rest/networkconf":           `[{"_id":"network-1","name":"LAN","purpose":"corporate","vlan":10,"ip_subnet":"192.0.2.1/24","dhcpd_enabled":true}]`,
		"/proxy/network/api/s/default/rest/wlanconf":              `[{"_id":"wlan-1","name":"Main","enabled":true,"security":"wpapsk","networkconf_id":"network-1","wlan_band":"both","is_guest":false}]`,
		"/proxy/network/api/s/default/rest/firewallrule":          `[{"_id":"rule-1","name":"Allow DNS","enabled":true,"action":"accept","ruleset":"LAN_IN","src_firewallgroup_ids":[],"dst_firewallgroup_ids":[],"protocol":"udp","rule_index":1}]`,
		"/proxy/network/api/s/default/stat/health":                `[{"subsystem":"www","status":"ok"}]`,
		"/proxy/network/api/s/default/cmd/devmgr":                 `[]`,
		"/proxy/network/api/s/default/cmd/stamgr":                 `[]`,
		"/proxy/network/api/s/default/rest/networkconf/network-1": `[]`,
		"/proxy/network/api/s/default/rest/wlanconf/wlan-1":       `[]`,
		"/proxy/network/api/s/default/rest/firewallrule/rule-1":   `[]`,
	}
	officialCollections := map[string]string{
		"/proxy/network/integration/v1/sites":                             `[{"id":"site-uuid","internalReference":"default","name":"Default"}]`,
		"/proxy/network/integration/v1/sites/site-uuid/devices":           `[{"id":"dev-1","macAddress":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","state":"ONLINE","ipAddress":"192.0.2.1","firmwareVersion":"1.0","features":["gateway","switching"],"interfaces":["ports"],"firmwareUpdatable":false,"supported":true}]`,
		"/proxy/network/integration/v1/sites/site-uuid/clients":           `[{"type":"WIRELESS","id":"client-1","macAddress":"aa:bb:cc:dd:ee:02","name":"Laptop","ipAddress":"192.0.2.2","connectedAt":"2026-08-07T12:00:00Z","access":{"type":"DEFAULT"},"uplinkDeviceId":"dev-1"}]`,
		"/proxy/network/integration/v1/sites/site-uuid/networks":          `[{"id":"network-1","name":"LAN","enabled":true,"default":true,"management":"GATEWAY","vlanId":10,"metadata":{"origin":"USER"}}]`,
		"/proxy/network/integration/v1/sites/site-uuid/wifi/broadcasts":   `[{"type":"STANDARD","id":"wlan-1","name":"Main","enabled":true,"metadata":{"origin":"USER"},"network":{"type":"SPECIFIC","networkId":"network-1"},"securityConfiguration":{"type":"WPA2_PERSONAL"},"broadcastingFrequenciesGHz":[2.4,5]}]`,
		"/proxy/network/integration/v1/sites/site-uuid/firewall/policies": `[{"id":"rule-1","name":"Allow DNS","description":"Permit DNS","enabled":true,"action":{"type":"ALLOW"},"source":{"zoneId":"zone-internal"},"destination":{"zoneId":"zone-gateway"},"ipProtocolScope":{"type":"IPV4","protocolFilter":{"protocolPreset":"UDP"}},"loggingEnabled":false,"index":1,"metadata":{"origin":"USER"}}]`,
	}
	officialDetails := map[string]string{
		"/proxy/network/integration/v1/sites/site-uuid/devices/dev-1":      `{"id":"dev-1","configurationId":"config-1","macAddress":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","state":"ONLINE","ipAddress":"192.0.2.1","firmwareVersion":"1.0","features":{"gateway":{},"switching":{}},"interfaces":{"ports":[{"idx":1,"connector":"RJ45","maxSpeedMbps":1000,"speedMbps":1000,"state":"UP","poe":{"enabled":false,"standard":"802.3at","state":"DOWN","type":2}}]},"firmwareUpdatable":false,"supported":true}`,
		"/proxy/network/integration/v1/sites/site-uuid/networks/network-1": `{"id":"network-1","name":"LAN","enabled":true,"default":true,"management":"GATEWAY","vlanId":10,"metadata":{"origin":"USER"},"ipv4Configuration":{"hostIpAddress":"192.0.2.1","prefixLength":24,"autoScaleEnabled":false,"dhcpConfiguration":{"mode":"SERVER","domainName":"example.test","dnsServerIpAddressesOverride":["192.0.2.53"],"ipAddressRange":{"start":"192.0.2.10","stop":"192.0.2.200"},"leaseTimeSeconds":86400,"pingConflictDetectionEnabled":true}}}`,
	}
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != commandTestAPIKey {
			t.Errorf("X-API-KEY = %q", got)
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		const dnsPath = "/proxy/network/integration/v1/sites/site-uuid/dns/policies"
		if r.Method == http.MethodGet && r.URL.Path == dnsPath {
			w.Header().Set("Content-Type", "application/json")
			body, _ := json.Marshal(dnsPolicies)
			_, _ = io.WriteString(w, `{"offset":0,"limit":100,"count":`+strconv.Itoa(len(dnsPolicies))+`,"totalCount":`+strconv.Itoa(len(dnsPolicies))+`,"data":`+string(body)+`}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, dnsPath+"/") {
			id := strings.TrimPrefix(r.URL.Path, dnsPath+"/")
			switch r.Method {
			case http.MethodGet:
				for _, policy := range dnsPolicies {
					if policy["id"] == id {
						_ = json.NewEncoder(w).Encode(policy)
						return
					}
				}
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			case http.MethodPut:
				var policy map[string]any
				if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
					http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
					return
				}
				policy["id"] = id
				for i := range dnsPolicies {
					if dnsPolicies[i]["id"] == id {
						dnsPolicies[i] = policy
					}
				}
				_ = json.NewEncoder(w).Encode(policy)
				return
			case http.MethodDelete:
				filtered := dnsPolicies[:0]
				for _, policy := range dnsPolicies {
					if policy["id"] != id {
						filtered = append(filtered, policy)
					}
				}
				dnsPolicies = filtered
				_, _ = io.WriteString(w, `{}`)
				return
			}
		}
		if r.Method == http.MethodPost && r.URL.Path == dnsPath {
			var policy map[string]any
			if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
				http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
				return
			}
			policy["id"] = "dns-new"
			dnsPolicies = append(dnsPolicies, policy)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(policy)
			return
		}
		if r.Method == http.MethodGet {
			if data, ok := officialCollections[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/json")
				var items []json.RawMessage
				if err := json.Unmarshal([]byte(data), &items); err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(w, `{"offset":0,"limit":100,"count":`+strconv.Itoa(len(items))+`,"totalCount":`+strconv.Itoa(len(items))+`,"data":`+data+`}`)
				return
			}
			if data, ok := officialDetails[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, data)
				return
			}
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

func TestDNSMutationRiskWiring(t *testing.T) {
	t.Run("update is routine", func(t *testing.T) {
		srv := newCommandTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		flagYes = true
		_, _, err := captureProcessOutput(t, func() error {
			return runDNSUpdate("dns-1", domain.DNSInput{IP: "192.0.2.2", SetIP: true})
		})
		if err != nil {
			t.Fatalf("routine update was blocked without --force: %v", err)
		}
	})

	t.Run("delete is destructive", func(t *testing.T) {
		srv := newCommandTestServer(t)
		defer srv.Close()
		useCommandTestRuntime(t, srv, true)
		flagYes = true
		stdout, _, err := captureProcessOutput(t, func() error { return runDNSDelete("dns-1") })
		if err == nil || !strings.Contains(stdout, "safe_mode_blocked") {
			t.Fatalf("delete without --force: err=%v stdout=%q, want safe_mode_blocked", err, stdout)
		}
	})
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
		{name: "site list", resource: "site", action: "list", humanMarker: "Default", run: runSiteList},
		{name: "site get", resource: "site", action: "get", humanMarker: "id: site-uuid", run: func() error { return runSiteGet("site-uuid") }},
		{name: "device list", resource: "device", action: "list", humanMarker: "Gateway", run: runDeviceList},
		{name: "device get", resource: "device", action: "get", humanMarker: "id: dev-1", run: func() error { return runDeviceGet("dev-1") }},
		{name: "client list", resource: "client", action: "list", humanMarker: "Laptop", run: runClientList},
		{name: "client get", resource: "client", action: "get", humanMarker: "id: client-1", run: func() error { return runClientGet("client-1") }},
		{name: "network list", resource: "network", action: "list", humanMarker: "LAN", run: runNetworkList},
		{name: "network get", resource: "network", action: "get", humanMarker: "id: network-1", run: func() error { return runNetworkGet("network-1") }},
		{name: "wlan list", resource: "wlan", action: "list", humanMarker: "Main", run: runWlanList},
		{name: "wlan get", resource: "wlan", action: "get", humanMarker: "id: wlan-1", run: func() error { return runWlanGet("wlan-1") }},
		{name: "port list", resource: "port", action: "list", humanMarker: "RJ45", run: func() error { return runPortList("") }},
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

func TestOfficialStableReadGoldenOutput(t *testing.T) {
	srv := newCommandTestServer(t)
	defer srv.Close()

	tests := []struct {
		name      string
		run       func() error
		humanWant string
		jsonWant  string
	}{
		{
			name: "site", run: func() error { return runSiteGet("site-uuid") },
			humanWant: "id: site-uuid\nname: Default\ndesc: \nrole: \n",
			jsonWant:  `{"id":"site-uuid","name":"Default","desc":"","role":""}`,
		},
		{
			name: "device", run: func() error { return runDeviceGet("dev-1") },
			humanWant: "id: dev-1\nmac: aa:bb:cc:dd:ee:01\nname: Gateway\nmodel: UDM\ntype: gateway\nstate: connected\nip: 192.0.2.1\nversion: 1.0\nuplink: \nadopted: true\n",
			jsonWant:  `{"id":"dev-1","mac":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","type":"gateway","state":"connected","ip":"192.0.2.1","version":"1.0","uplink":"","adopted":true}`,
		},
		{
			name: "client", run: func() error { return runClientGet("client-1") },
			humanWant: "id: client-1\nmac: aa:bb:cc:dd:ee:02\nhostname: \nname: Laptop\nip: 192.0.2.2\nessid: \nnetwork: \nis_wired: false\nblocked: false\nlast_seen: 2026-08-07T12:00:00Z\n",
			jsonWant:  `{"id":"client-1","mac":"aa:bb:cc:dd:ee:02","hostname":"","name":"Laptop","ip":"192.0.2.2","essid":"","network":"","is_wired":false,"blocked":false,"last_seen":"2026-08-07T12:00:00Z"}`,
		},
		{
			name: "network", run: func() error { return runNetworkGet("network-1") },
			humanWant: "id: network-1\nname: LAN\npurpose: gateway\nvlan: 10\nsubnet: 192.0.2.1/24\ndhcp_enabled: true\ndomain_name: example.test\nwan: false\n",
			jsonWant:  `{"id":"network-1","name":"LAN","purpose":"gateway","vlan":10,"subnet":"192.0.2.1/24","dhcp_enabled":true,"domain_name":"example.test","wan":false}`,
		},
		{
			name: "wlan", run: func() error { return runWlanGet("wlan-1") },
			humanWant: "id: wlan-1\nname: Main\nenabled: true\nsecurity: wpa2_personal\nnetwork_id: network-1\nband: both\nguest: false\n",
			jsonWant:  `{"id":"wlan-1","name":"Main","enabled":true,"security":"wpa2_personal","network_id":"network-1","band":"both","guest":false}`,
		},
		{
			name: "port", run: func() error { return runPortGet("dev-1", 1) },
			humanWant: "device_id: dev-1\ndevice_name: Gateway\nport_idx: 1\nname: \nmedia: RJ45\nspeed: 1000\npoe: off\nenabled: true\nprofile: \nnetworks: \n",
			jsonWant:  `{"device_id":"dev-1","device_name":"Gateway","port_idx":1,"name":"","media":"RJ45","speed":"1000","poe":"off","enabled":true,"profile":"","networks":""}`,
		},
		{
			name: "firewall", run: func() error { return runFirewallGet("rule-1") },
			humanWant: "id: rule-1\nname: Allow DNS\ndescription: Permit DNS\nenabled: true\naction: accept\nruleset: \nsrc: \ndst: \nprotocol: udp\nindex: 1\n",
			jsonWant:  `{"id":"rule-1","name":"Allow DNS","description":"Permit DNS","enabled":true,"action":"accept","ruleset":"","src":"","dst":"","protocol":"udp","index":1}`,
		},
		{
			name: "dns", run: func() error { return runDNSGet("dns-1") },
			humanWant: "id: dns-1\ntype: A_RECORD\ndomain: router.example.test\nvalue: 192.0.2.1\nenabled: true\n",
			jsonWant:  `{"id":"dns-1","type":"A_RECORD","domain":"router.example.test","enabled":true,"ipv4_address":"192.0.2.1","ttl_seconds":300}`,
		},
		{
			name: "health", run: runSystemHealth,
			humanWant: "status: ok\ndevice_total: 1\ndevice_connected: 1\nclient_total: 1\n",
			jsonWant:  `{"status":"ok","device_total":1,"device_connected":1,"client_total":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" human", func(t *testing.T) {
			useCommandTestRuntime(t, srv, false)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			if stdout != tt.humanWant {
				t.Fatalf("human output:\n%s\nwant:\n%s", stdout, tt.humanWant)
			}
		})

		t.Run(tt.name+" json", func(t *testing.T) {
			useCommandTestRuntime(t, srv, true)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			var envelope struct {
				Data any `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			var want any
			if err := json.Unmarshal([]byte(tt.jsonWant), &want); err != nil {
				t.Fatalf("decode golden JSON: %v", err)
			}
			if !reflect.DeepEqual(envelope.Data, want) {
				t.Fatalf("JSON data = %#v, want %#v", envelope.Data, want)
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
