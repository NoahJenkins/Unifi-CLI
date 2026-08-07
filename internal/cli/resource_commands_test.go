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

const (
	commandSiteID     = "11111111-1111-4111-8111-111111111111"
	commandDeviceID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	commandClientID   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1"
	commandNetworkID  = "cccccccc-cccc-4ccc-8ccc-ccccccccccc1"
	commandWlanID     = "dddddddd-dddd-4ddd-8ddd-ddddddddddd1"
	commandFirewallID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1"
	commandDNSID      = "10000000-0000-4000-8000-000000000001"
)

type commandServerOptions struct {
	officialCollections map[string]string
	officialStatuses    map[string]int
	dnsPolicies         []map[string]any
}

func newCommandTestServer(t *testing.T) *httptest.Server {
	return newCommandTestServerWithOptions(t, commandServerOptions{})
}

func newCommandTestServerWithOptions(t *testing.T, opts commandServerOptions) *httptest.Server {
	t.Helper()
	dnsPolicies := []map[string]any{{
		"id": commandDNSID, "type": "A_RECORD", "domain": "router.example.test",
		"ipv4Address": "192.0.2.1", "enabled": true, "ttlSeconds": float64(300), "metadata": map[string]any{"origin": "USER"},
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
		"/proxy/network/integration/v1/sites":                                                        `[{"id":"11111111-1111-4111-8111-111111111111","internalReference":"default","name":"Default"}]`,
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/devices":           `[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1","macAddress":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","state":"ONLINE","ipAddress":"192.0.2.1","firmwareVersion":"1.0","features":["gateway","switching"],"interfaces":["ports"],"firmwareUpdatable":false,"supported":true}]`,
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/clients":           `[{"type":"WIRELESS","id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1","macAddress":"aa:bb:cc:dd:ee:02","name":"Laptop","ipAddress":"192.0.2.2","connectedAt":"2026-08-07T12:00:00Z","access":{"type":"DEFAULT"},"uplinkDeviceId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"}]`,
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/networks":          `[{"id":"cccccccc-cccc-4ccc-8ccc-ccccccccccc1","name":"LAN","enabled":true,"default":true,"management":"GATEWAY","vlanId":10,"metadata":{"origin":"USER"}}]`,
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/wifi/broadcasts":   `[{"type":"STANDARD","id":"dddddddd-dddd-4ddd-8ddd-ddddddddddd1","name":"Main","enabled":true,"metadata":{"origin":"USER"},"network":{"type":"SPECIFIC","networkId":"cccccccc-cccc-4ccc-8ccc-ccccccccccc1"},"securityConfiguration":{"type":"WPA2_PERSONAL"},"broadcastingFrequenciesGHz":[2.4,6]}]`,
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/firewall/policies": `[{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1","name":"Allow DNS","description":"Permit DNS","enabled":true,"action":{"type":"ALLOW","allowReturnTraffic":false},"source":{"zoneId":"ffffffff-ffff-4fff-8fff-fffffffffff1"},"destination":{"zoneId":"ffffffff-ffff-4fff-8fff-fffffffffff2"},"ipProtocolScope":{"ipVersion":"IPV4","protocolFilter":{"type":"NAMED_PROTOCOL","protocol":{"name":"udp"},"matchOpposite":false}},"loggingEnabled":false,"index":1,"metadata":{"origin":"USER"}}]`,
	}
	officialDetails := map[string]string{
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/devices/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1":  `{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1","configurationId":"config-1","macAddress":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","state":"ONLINE","ipAddress":"192.0.2.1","firmwareVersion":"1.0","features":{"switching":{"lags":[]}},"interfaces":{"ports":[{"idx":1,"connector":"RJ45","maxSpeedMbps":1000,"speedMbps":1000,"state":"UP","poe":{"enabled":false,"standard":"802.3at","state":"DOWN","type":2}}]},"firmwareUpdatable":false,"supported":true}`,
		"/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/networks/cccccccc-cccc-4ccc-8ccc-ccccccccccc1": `{"id":"cccccccc-cccc-4ccc-8ccc-ccccccccccc1","name":"LAN","enabled":true,"default":true,"management":"GATEWAY","vlanId":10,"metadata":{"origin":"USER"},"cellularBackupEnabled":false,"internetAccessEnabled":true,"isolationEnabled":false,"mdnsForwardingEnabled":true,"ipv4Configuration":{"hostIpAddress":"192.0.2.1","prefixLength":24,"autoScaleEnabled":false,"dhcpConfiguration":{"mode":"SERVER","domainName":"example.test","dnsServerIpAddressesOverride":["192.0.2.53"],"ipAddressRange":{"start":"192.0.2.10","stop":"192.0.2.200"},"leaseTimeSeconds":86400,"pingConflictDetectionEnabled":true}}}`,
	}
	for path, body := range opts.officialCollections {
		officialCollections[path] = body
	}
	if opts.dnsPolicies != nil {
		dnsPolicies = opts.dnsPolicies
	}
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-KEY"); got != commandTestAPIKey {
			t.Errorf("X-API-KEY = %q", got)
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if status := opts.officialStatuses[r.URL.Path]; status != 0 {
			http.Error(w, `{"message":"forbidden by test fixture"}`, status)
			return
		}
		const dnsPath = "/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111/dns/policies"
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
			policy["id"] = "10000000-0000-4000-8000-000000000099"
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
			return runDNSUpdate(commandDNSID, domain.DNSInput{IP: "192.0.2.2", SetIP: true})
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
		stdout, _, err := captureProcessOutput(t, func() error { return runDNSDelete(commandDNSID) })
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
		{name: "site get", resource: "site", action: "get", humanMarker: "id: " + commandSiteID, run: func() error { return runSiteGet(commandSiteID) }},
		{name: "device list", resource: "device", action: "list", humanMarker: "Gateway", run: runDeviceList},
		{name: "device get", resource: "device", action: "get", humanMarker: "id: " + commandDeviceID, run: func() error { return runDeviceGet(commandDeviceID) }},
		{name: "client list", resource: "client", action: "list", humanMarker: "Laptop", run: runClientList},
		{name: "client get", resource: "client", action: "get", humanMarker: "id: " + commandClientID, run: func() error { return runClientGet(commandClientID) }},
		{name: "network list", resource: "network", action: "list", humanMarker: "LAN", run: runNetworkList},
		{name: "network get", resource: "network", action: "get", humanMarker: "id: " + commandNetworkID, run: func() error { return runNetworkGet(commandNetworkID) }},
		{name: "wlan list", resource: "wlan", action: "list", humanMarker: "Main", run: runWlanList},
		{name: "wlan get", resource: "wlan", action: "get", humanMarker: "id: " + commandWlanID, run: func() error { return runWlanGet(commandWlanID) }},
		{name: "port list", resource: "port", action: "list", humanMarker: "RJ45", run: func() error { return runPortList("") }},
		{name: "port get", resource: "port", action: "get", humanMarker: "port_idx: 1", run: func() error { return runPortGet(commandDeviceID, 1) }},
		{name: "firewall list", resource: "firewall", action: "list", humanMarker: "Allow DNS", run: runFirewallList},
		{name: "firewall get", resource: "firewall", action: "get", humanMarker: "id: " + commandFirewallID, run: func() error { return runFirewallGet(commandFirewallID) }},
		{name: "dns list", resource: "dns", action: "list", humanMarker: "router.example.test", run: runDNSList},
		{name: "dns get", resource: "dns", action: "get", humanMarker: "id: " + commandDNSID, run: func() error { return runDNSGet(commandDNSID) }},
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
			name: "site", run: func() error { return runSiteGet(commandSiteID) },
			humanWant: "id: " + commandSiteID + "\nname: Default\ndesc: \nrole: \n",
			jsonWant:  `{"id":"11111111-1111-4111-8111-111111111111","name":"Default","desc":"","role":""}`,
		},
		{
			name: "device", run: func() error { return runDeviceGet(commandDeviceID) },
			humanWant: "id: " + commandDeviceID + "\nmac: aa:bb:cc:dd:ee:01\nname: Gateway\nmodel: UDM\ntype: gateway\nstate: connected\nip: 192.0.2.1\nversion: 1.0\nuplink: \nadopted: true\n",
			jsonWant:  `{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1","mac":"aa:bb:cc:dd:ee:01","name":"Gateway","model":"UDM","type":"gateway","state":"connected","ip":"192.0.2.1","version":"1.0","uplink":"","adopted":true}`,
		},
		{
			name: "client", run: func() error { return runClientGet(commandClientID) },
			humanWant: "id: " + commandClientID + "\nmac: aa:bb:cc:dd:ee:02\nhostname: \nname: Laptop\nip: 192.0.2.2\nessid: \nnetwork: \nis_wired: false\nblocked: false\nlast_seen: \n",
			jsonWant:  `{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1","mac":"aa:bb:cc:dd:ee:02","hostname":"","name":"Laptop","ip":"192.0.2.2","essid":"","network":"","is_wired":false,"blocked":false,"last_seen":""}`,
		},
		{
			name: "network", run: func() error { return runNetworkGet(commandNetworkID) },
			humanWant: "id: " + commandNetworkID + "\nname: LAN\npurpose: \nvlan: 10\nsubnet: 192.0.2.1/24\ndhcp_enabled: true\ndomain_name: example.test\nwan: false\n",
			jsonWant:  `{"id":"cccccccc-cccc-4ccc-8ccc-ccccccccccc1","name":"LAN","purpose":"","vlan":10,"subnet":"192.0.2.1/24","dhcp_enabled":true,"domain_name":"example.test","wan":false}`,
		},
		{
			name: "wlan", run: func() error { return runWlanGet(commandWlanID) },
			humanWant: "id: " + commandWlanID + "\nname: Main\nenabled: true\nsecurity: wpa2_personal\nnetwork_id: " + commandNetworkID + "\nband: 2.4+6\nguest: false\n",
			jsonWant:  `{"id":"dddddddd-dddd-4ddd-8ddd-ddddddddddd1","name":"Main","enabled":true,"security":"wpa2_personal","network_id":"cccccccc-cccc-4ccc-8ccc-ccccccccccc1","band":"2.4+6","guest":false}`,
		},
		{
			name: "port", run: func() error { return runPortGet(commandDeviceID, 1) },
			humanWant: "device_id: " + commandDeviceID + "\ndevice_name: Gateway\nport_idx: 1\nname: \nmedia: RJ45\nspeed: 1000\npoe: off\nenabled: \nprofile: \nnetworks: \n",
			jsonWant:  `{"device_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1","device_name":"Gateway","port_idx":1,"name":"","media":"RJ45","speed":"1000","poe":"off","profile":"","networks":""}`,
		},
		{
			name: "firewall", run: func() error { return runFirewallGet(commandFirewallID) },
			humanWant: "id: " + commandFirewallID + "\nname: Allow DNS\nenabled: true\naction: accept\nruleset: \nsrc: \ndst: \nprotocol: ipv4:udp\nindex: 1\n",
			jsonWant:  `{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1","name":"Allow DNS","enabled":true,"action":"accept","ruleset":"","src":"","dst":"","protocol":"ipv4:udp","index":1}`,
		},
		{
			name: "dns", run: func() error { return runDNSGet(commandDNSID) },
			humanWant: "id: " + commandDNSID + "\ntype: A_RECORD\ndomain: router.example.test\nvalue: 192.0.2.1\nenabled: true\n",
			jsonWant:  `{"id":"10000000-0000-4000-8000-000000000001","type":"A_RECORD","domain":"router.example.test","enabled":true,"ipv4_address":"192.0.2.1","ttl_seconds":300}`,
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

func TestStableReadEmptyOutputExactHumanAndJSONEnvelopes(t *testing.T) {
	const sitePath = "/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name, resource, action, humanWant string
		run                               func() error
		jsonData                          string
		opts                              commandServerOptions
	}{
		{name: "sites", resource: "site", action: "list", run: runSiteList, humanWant: "NAME  ID  DESC  ROLE\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{"/proxy/network/integration/v1/sites": `[]`}}},
		{name: "devices", resource: "device", action: "list", run: runDeviceList, humanWant: "NAME  MAC  MODEL  TYPE  STATE  IP\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/devices": `[]`}}},
		{name: "clients", resource: "client", action: "list", run: runClientList, humanWant: "NAME  MAC  IP  NETWORK  ESSID  WIRED  BLOCKED\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/clients": `[]`}}},
		{name: "networks", resource: "network", action: "list", run: runNetworkList, humanWant: "NAME  PURPOSE  VLAN  SUBNET  DHCP  WAN\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/networks": `[]`}}},
		{name: "wlans", resource: "wlan", action: "list", run: runWlanList, humanWant: "NAME  ENABLED  SECURITY  NETWORK  BAND  GUEST\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/wifi/broadcasts": `[]`}}},
		{name: "ports", resource: "port", action: "list", run: func() error { return runPortList("") }, humanWant: "DEVICE  PORT  NAME  MEDIA  SPEED  POE  ENABLED  PROFILE\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/devices": `[]`}}},
		{name: "firewall", resource: "firewall", action: "list", run: runFirewallList, humanWant: "INDEX  NAME  ACTION  RULESET  SRC  DST  PROTO  ENABLED  ID\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/firewall/policies": `[]`}}},
		{name: "dns", resource: "dns", action: "list", run: runDNSList, humanWant: "TYPE  DOMAIN  VALUE  ENABLED  ID\n", jsonData: `[]`, opts: commandServerOptions{dnsPolicies: []map[string]any{}}},
		{name: "resolvers", resource: "dns", action: "resolvers list", run: runDNSResolversList, humanWant: "NETWORK  DNS  WAN  ID\n", jsonData: `[]`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/networks": `[]`}}},
		{name: "health", resource: "system", action: "health", run: runSystemHealth, humanWant: "status: ok\ndevice_total: 0\ndevice_connected: 0\nclient_total: 0\n", jsonData: `{"status":"ok","device_total":0,"device_connected":0,"client_total":0}`, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/devices": `[]`, sitePath + "/clients": `[]`}}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" human", func(t *testing.T) {
			srv := newCommandTestServerWithOptions(t, tt.opts)
			defer srv.Close()
			useCommandTestRuntime(t, srv, false)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			if stdout != tt.humanWant {
				t.Fatalf("human output = %q, want %q", stdout, tt.humanWant)
			}
		})
		t.Run(tt.name+" json", func(t *testing.T) {
			srv := newCommandTestServerWithOptions(t, tt.opts)
			defer srv.Close()
			useCommandTestRuntime(t, srv, true)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err != nil {
				t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout, stderr)
			}
			want := `{"schema_version":"1","ok":true,"resource":` + strconv.Quote(tt.resource) + `,"action":` + strconv.Quote(tt.action) + `,"data":` + tt.jsonData + `,"meta":{"site":"default","dry_run":false}}`
			assertDecodedJSONEqual(t, stdout, want)
		})
	}
}

func TestStableReadPermissionErrorsUseExactHumanAndJSONEnvelopes(t *testing.T) {
	const sitePath = "/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name, resource, action, forbiddenPath string
		run                                   func() error
	}{
		{name: "sites", resource: "site", action: "list", forbiddenPath: "/proxy/network/integration/v1/sites", run: runSiteList},
		{name: "devices", resource: "device", action: "list", forbiddenPath: sitePath + "/devices", run: runDeviceList},
		{name: "clients", resource: "client", action: "list", forbiddenPath: sitePath + "/clients", run: runClientList},
		{name: "networks", resource: "network", action: "list", forbiddenPath: sitePath + "/networks", run: runNetworkList},
		{name: "wlans", resource: "wlan", action: "list", forbiddenPath: sitePath + "/wifi/broadcasts", run: runWlanList},
		{name: "ports", resource: "port", action: "get", forbiddenPath: sitePath + "/devices/" + commandDeviceID, run: func() error { return runPortGet(commandDeviceID, 1) }},
		{name: "firewall", resource: "firewall", action: "list", forbiddenPath: sitePath + "/firewall/policies", run: runFirewallList},
		{name: "dns", resource: "dns", action: "list", forbiddenPath: sitePath + "/dns/policies", run: runDNSList},
		{name: "health", resource: "system", action: "health", forbiddenPath: sitePath + "/clients", run: runSystemHealth},
	}
	for _, tt := range tests {
		t.Run(tt.name+" human", func(t *testing.T) {
			srv := newCommandTestServerWithOptions(t, commandServerOptions{officialStatuses: map[string]int{tt.forbiddenPath: http.StatusForbidden}})
			defer srv.Close()
			useCommandTestRuntime(t, srv, false)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err == nil {
				t.Fatalf("permission error missing; stdout=%q stderr=%q", stdout, stderr)
			}
			if stdout != "" || stderr != "permission_denied: controller returned HTTP status 403: permission denied\n" {
				t.Fatalf("human error stdout=%q stderr=%q", stdout, stderr)
			}
		})
		t.Run(tt.name+" json", func(t *testing.T) {
			srv := newCommandTestServerWithOptions(t, commandServerOptions{officialStatuses: map[string]int{tt.forbiddenPath: http.StatusForbidden}})
			defer srv.Close()
			useCommandTestRuntime(t, srv, true)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err == nil {
				t.Fatalf("permission error missing; stdout=%q stderr=%q", stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("JSON error wrote stderr %q", stderr)
			}
			want := `{"schema_version":"1","ok":false,"resource":` + strconv.Quote(tt.resource) + `,"action":` + strconv.Quote(tt.action) + `,"data":null,"meta":{"site":"default","dry_run":false},"error":{"code":"permission_denied","message":"controller returned HTTP status 403: permission denied"}}`
			assertDecodedJSONEqual(t, stdout, want)
		})
	}
}

func TestStableGetSelectorsReturnExactAmbiguityEnvelopes(t *testing.T) {
	const sitePath = "/proxy/network/integration/v1/sites/11111111-1111-4111-8111-111111111111"
	duplicates := map[string]string{
		"site":     `[{"id":"11111111-1111-4111-8111-111111111111","internalReference":"default","name":"Duplicate"},{"id":"22222222-2222-4222-8222-222222222222","internalReference":"lab","name":"Duplicate"}]`,
		"device":   `[{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1","macAddress":"aa:bb:cc:dd:ee:01","name":"Duplicate","model":"UDM","state":"ONLINE","ipAddress":"192.0.2.1","firmwareVersion":"1.0","features":["gateway"],"interfaces":["ports"],"firmwareUpdatable":false,"supported":true},{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2","macAddress":"aa:bb:cc:dd:ee:02","name":"Duplicate","model":"USW","state":"ONLINE","ipAddress":"192.0.2.2","firmwareVersion":"1.0","features":["switching"],"interfaces":["ports"],"firmwareUpdatable":false,"supported":true}]`,
		"client":   `[{"type":"WIRED","id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb1","name":"Duplicate","macAddress":"aa:bb:cc:dd:ee:11","access":{"type":"DEFAULT"},"uplinkDeviceId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"},{"type":"WIRELESS","id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2","name":"Duplicate","macAddress":"aa:bb:cc:dd:ee:12","access":{"type":"DEFAULT"},"uplinkDeviceId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"}]`,
		"network":  `[{"id":"cccccccc-cccc-4ccc-8ccc-ccccccccccc1","name":"Duplicate","enabled":true,"default":true,"management":"GATEWAY","vlanId":1,"metadata":{"origin":"SYSTEM"}},{"id":"cccccccc-cccc-4ccc-8ccc-ccccccccccc2","name":"Duplicate","enabled":true,"default":false,"management":"UNMANAGED","vlanId":20,"metadata":{"origin":"USER"}}]`,
		"wlan":     `[{"type":"STANDARD","id":"dddddddd-dddd-4ddd-8ddd-ddddddddddd1","name":"Duplicate","enabled":true,"metadata":{"origin":"USER"},"securityConfiguration":{"type":"OPEN"},"broadcastingFrequenciesGHz":[2.4]},{"type":"STANDARD","id":"dddddddd-dddd-4ddd-8ddd-ddddddddddd2","name":"Duplicate","enabled":true,"metadata":{"origin":"USER"},"securityConfiguration":{"type":"OPEN"},"broadcastingFrequenciesGHz":[5]}]`,
		"firewall": `[{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee1","name":"Duplicate","enabled":true,"action":{"type":"BLOCK"},"source":{"zoneId":"ffffffff-ffff-4fff-8fff-fffffffffff1"},"destination":{"zoneId":"ffffffff-ffff-4fff-8fff-fffffffffff2"},"ipProtocolScope":{"ipVersion":"IPV4"},"loggingEnabled":false,"index":1,"metadata":{"origin":"USER"}},{"id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeee2","name":"Duplicate","enabled":true,"action":{"type":"BLOCK"},"source":{"zoneId":"ffffffff-ffff-4fff-8fff-fffffffffff1"},"destination":{"zoneId":"ffffffff-ffff-4fff-8fff-fffffffffff2"},"ipProtocolScope":{"ipVersion":"IPV4"},"loggingEnabled":false,"index":2,"metadata":{"origin":"USER"}}]`,
	}
	tests := []struct {
		name, resource, action string
		run                    func() error
		opts                   commandServerOptions
	}{
		{name: "site", resource: "site", action: "get", run: func() error { return runSiteGet("Duplicate") }, opts: commandServerOptions{officialCollections: map[string]string{"/proxy/network/integration/v1/sites": duplicates["site"]}}},
		{name: "device", resource: "device", action: "get", run: func() error { return runDeviceGet("Duplicate") }, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/devices": duplicates["device"]}}},
		{name: "client", resource: "client", action: "get", run: func() error { return runClientGet("Duplicate") }, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/clients": duplicates["client"]}}},
		{name: "network", resource: "network", action: "get", run: func() error { return runNetworkGet("Duplicate") }, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/networks": duplicates["network"]}}},
		{name: "wlan", resource: "wlan", action: "get", run: func() error { return runWlanGet("Duplicate") }, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/wifi/broadcasts": duplicates["wlan"]}}},
		{name: "port device", resource: "port", action: "get", run: func() error { return runPortGet("Duplicate", 1) }, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/devices": duplicates["device"]}}},
		{name: "firewall", resource: "firewall", action: "get", run: func() error { return runFirewallGet("Duplicate") }, opts: commandServerOptions{officialCollections: map[string]string{sitePath + "/firewall/policies": duplicates["firewall"]}}},
		{name: "dns", resource: "dns", action: "get", run: func() error { return runDNSGet("Duplicate") }, opts: commandServerOptions{dnsPolicies: []map[string]any{
			{"id": "10000000-0000-4000-8000-000000000001", "type": "A_RECORD", "domain": "Duplicate", "enabled": true, "ipv4Address": "192.0.2.1", "ttlSeconds": 300, "metadata": map[string]any{"origin": "USER"}},
			{"id": "10000000-0000-4000-8000-000000000002", "type": "A_RECORD", "domain": "Duplicate", "enabled": true, "ipv4Address": "192.0.2.2", "ttlSeconds": 300, "metadata": map[string]any{"origin": "USER"}},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name+" human", func(t *testing.T) {
			srv := newCommandTestServerWithOptions(t, tt.opts)
			defer srv.Close()
			useCommandTestRuntime(t, srv, false)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err == nil {
				t.Fatalf("ambiguity missing; stdout=%q stderr=%q", stdout, stderr)
			}
			if stdout != "" || stderr != "ambiguous_id: multiple matches for \"Duplicate\"\n" {
				t.Fatalf("human ambiguity stdout=%q stderr=%q", stdout, stderr)
			}
		})
		t.Run(tt.name+" json", func(t *testing.T) {
			srv := newCommandTestServerWithOptions(t, tt.opts)
			defer srv.Close()
			useCommandTestRuntime(t, srv, true)
			stdout, stderr, err := captureProcessOutput(t, tt.run)
			if err == nil {
				t.Fatalf("ambiguity missing; stdout=%q stderr=%q", stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("JSON ambiguity wrote stderr %q", stderr)
			}
			want := `{"schema_version":"1","ok":false,"resource":` + strconv.Quote(tt.resource) + `,"action":` + strconv.Quote(tt.action) + `,"data":null,"meta":{"site":"default","dry_run":false},"error":{"code":"ambiguous_id","message":"multiple matches for \"Duplicate\""}}`
			assertDecodedJSONEqual(t, stdout, want)
		})
	}
}

func assertDecodedJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v; got=%q", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v; want=%q", err, want)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %#v, want %#v", gotValue, wantValue)
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
