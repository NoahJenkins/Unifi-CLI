package contractapi

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

type contractCase struct {
	name  string
	edit  func(map[string]any)
	valid bool
}

func TestContractCorpus(t *testing.T) {
	for _, version := range Versions() {
		t.Run(version, func(t *testing.T) {
			p, _ := ProfileFor(version)
			offline, _ := NewValidator(p, "")
			validators := []*Validator{offline}
			if cache := os.Getenv("UNIFI_CONTRACT_CACHE"); cache != "" {
				v, err := NewValidator(p, cache)
				if err != nil {
					t.Fatal(err)
				}
				validators = append(validators, v)
			}
			for resource, seed := range map[string]map[string]any{"firewall": FirewallSeed(), "network": NetworkSeed("GATEWAY"), "wlan": WLANSeed()} {
				for _, v := range validators {
					if err := v.Validate(resource, true, seed); err != nil {
						t.Fatalf("%s response: %v", resource, err)
					}
				}
				cases := []contractCase{
					{"valid", func(map[string]any) {}, true},
					{"missing name", func(b map[string]any) { delete(b, "name") }, false},
					{"wrong bool type", func(b map[string]any) { b["enabled"] = "true" }, false},
				}
				switch resource {
				case "firewall":
					cases = append(cases,
						contractCase{"lowercase tcp", func(b map[string]any) { nested(b, "ipProtocolScope", "protocolFilter", "protocol")["name"] = "tcp" }, false},
						contractCase{"missing protocol key", func(b map[string]any) { delete(nested(b, "ipProtocolScope", "protocolFilter", "protocol"), "name") }, false},
						contractCase{"missing named subtype field", func(b map[string]any) { delete(nested(b, "ipProtocolScope", "protocolFilter"), "matchOpposite") }, false},
						contractCase{"missing action subtype field", func(b map[string]any) { delete(nested(b, "action"), "allowReturnTraffic") }, false},
						contractCase{"bad zone UUID", func(b map[string]any) { nested(b, "source")["zoneId"] = "not-a-uuid" }, false},
						contractCase{"optional description omitted", func(b map[string]any) { delete(b, "description") }, true})
				case "network":
					cases = append(cases,
						contractCase{"vlan upper bound", func(b map[string]any) { b["vlanId"] = 4010 }, false},
						contractCase{"vlan lower bound", func(b map[string]any) { b["vlanId"] = 0 }, false},
						contractCase{"fractional vlan", func(b map[string]any) { b["vlanId"] = 1.5 }, false},
						contractCase{"prefix bound", func(b map[string]any) { nested(b, "ipv4Configuration")["prefixLength"] = 31 }, false},
						contractCase{"lease bound", func(b map[string]any) { nested(b, "ipv4Configuration", "dhcpConfiguration")["leaseTimeSeconds"] = -1 }, false},
						contractCase{"missing DHCP subtype field", func(b map[string]any) {
							delete(nested(b, "ipv4Configuration", "dhcpConfiguration"), "leaseTimeSeconds")
						}, false},
						contractCase{"optional domain omitted", func(b map[string]any) { delete(nested(b, "ipv4Configuration", "dhcpConfiguration"), "domainName") }, true},
						contractCase{"mDNS request omitted", func(b map[string]any) { delete(b, "mdnsForwardingEnabled") }, true})
				case "wlan":
					cases = append(cases,
						contractCase{"empty AP filter", func(b map[string]any) {
							b["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICES", "deviceIds": []any{}}
						}, false},
						contractCase{"duplicate AP IDs", func(b map[string]any) {
							b["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICES", "deviceIds": []any{DeviceID, DeviceID}}
						}, false},
						contractCase{"missing AP discriminator", func(b map[string]any) { b["broadcastingDeviceFilter"] = map[string]any{"deviceIds": []any{DeviceID}} }, false},
						contractCase{"wrong AP branch", func(b map[string]any) {
							b["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICES", "deviceTagIds": []any{TagID}}
						}, false},
						contractCase{"invalid frequency", func(b map[string]any) { b["broadcastingFrequenciesGHz"] = []any{3} }, false},
						contractCase{"duplicate frequency", func(b map[string]any) { b["broadcastingFrequenciesGHz"] = []any{5, 5} }, false},
						contractCase{"null AP filter", func(b map[string]any) { b["broadcastingDeviceFilter"] = nil }, true})
				}

				var containers [][]string
				switch resource {
				case "firewall":
					containers = [][]string{{"action"}, {"ipProtocolScope"}, {"ipProtocolScope", "protocolFilter"}, {"ipProtocolScope", "protocolFilter", "protocol"}}
				case "network":
					containers = [][]string{{"ipv4Configuration", "dhcpConfiguration"}}
				case "wlan":
					containers = [][]string{{"network"}, {"securityConfiguration"}, {"broadcastingDeviceFilter"}}
				}
				for _, fields := range containers {
					for _, invalid := range []any{true, "", []any{}, nil} {
						if fields[0] == "broadcastingDeviceFilter" && invalid == nil {
							continue
						}
						cases = append(cases, contractCase{fmt.Sprintf("container %v %T", fields, invalid), func(b map[string]any) {
							nested(b, fields[:len(fields)-1]...)[fields[len(fields)-1]] = invalid
						}, false})
					}
				}
				for _, tc := range cases {
					t.Run(resource+"/"+tc.name, func(t *testing.T) {
						body := Writable(resource, seed)
						tc.edit(body)
						for _, v := range validators {
							err := v.Validate(resource, false, body)
							if (err == nil) != tc.valid {
								t.Fatalf("valid=%t want %t: %v", err == nil, tc.valid, err)
							}
						}
					})
				}
			}
		})
	}
}
func nested(b map[string]any, fields ...string) map[string]any {
	for _, field := range fields {
		b = b[field].(map[string]any)
	}
	return b
}
func TestOfflineUnmodeledFieldReportsCoverage(t *testing.T) {
	p, _ := ProfileFor("10.4.57")
	v, _ := NewValidator(p, "")
	body := Writable("network", NetworkSeed("UNMANAGED"))
	body["futureField"] = true
	var gap *CoverageError
	if !errors.As(v.Validate("network", false, body), &gap) {
		t.Fatal("unmodeled field must report coverage gap")
	}
}
