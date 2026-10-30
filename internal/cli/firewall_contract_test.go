package cli

import (
	"github.com/noahjenkins/unifi-cli/internal/testutil/contractapi"
	"strings"
	"testing"
)

func firewallContractArgs(protocol, ip string) []string {
	return []string{"firewall", "create", "--name", "Synthetic policy", "--description", "Preserve this optional description", "--action", "allow", "--allow-return-traffic", "--source-zone", "Internal", "--destination-zone", "External", "--ip-version", ip, "--protocol", protocol}
}
func TestFirewallContractLifecycle(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, ip := range []string{"ipv4", "ipv6", "ipv4_and_ipv6"} {
			protocols := []string{"tcp", "udp", "tcp_udp", "all"}
			if ip == "ipv4" {
				protocols = append(protocols, "icmp")
			}
			if ip == "ipv6" {
				protocols = append(protocols, "icmpv6")
			}
			for _, protocol := range protocols {
				t.Run(version+"/"+ip+"/"+protocol, func(t *testing.T) {
					s := contractRuntime(t, version)
					args := firewallContractArgs(protocol, ip)
					contractCommand(t, true, args...)
					if s.Attempts() != 0 || s.Snapshot("firewall", contractapi.PolicyID) != nil {
						t.Fatal("create plan mutated state")
					}
					contractCommand(t, true, applied(args...)...)
					want := contractapi.FirewallSeed()
					scope := map[string]any{"ipVersion": strings.ToUpper(ip)}
					switch protocol {
					case "all":
					case "tcp_udp":
						scope["protocolFilter"] = map[string]any{"type": "PRESET", "preset": map[string]any{"name": "TCP_UDP"}}
					default:
						scope["protocolFilter"] = map[string]any{"type": "NAMED_PROTOCOL", "matchOpposite": false, "protocol": map[string]any{"name": strings.ToUpper(protocol)}}
					}
					want["ipProtocolScope"] = scope
					preserved(t, s, "firewall", contractapi.PolicyID, want)
					contractCommand(t, true, applied("firewall", "update", contractapi.PolicyID, "--name", "Renamed policy")...)
					want["name"] = "Renamed policy"
					preserved(t, s, "firewall", contractapi.PolicyID, want)
					contractCommand(t, true, applied("firewall", "delete", contractapi.PolicyID)...)
					if s.Attempts() != 3 || s.Snapshot("firewall", contractapi.PolicyID) != nil {
						t.Fatal("firewall lifecycle did not restore empty state with three writes")
					}
				})
			}
		}
	}
}
func TestFirewallContractExactFilters(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, subnet := range []bool{false, true} {
			name := "address"
			if subnet {
				name = "subnet"
			}
			t.Run(version+"/"+name, func(t *testing.T) {
				s := contractRuntime(t, version)
				args := firewallContractArgs("tcp", "ipv4")
				sourceFlag, sourceType, sourceValue := "--source-ip", "IP_ADDRESS", "192.0.2.10"
				if subnet {
					sourceFlag, sourceType, sourceValue = "--source-subnet", "SUBNET", "192.0.2.0/24"
				}
				args = append(args, sourceFlag, sourceValue, "--destination-ip", "198.51.100.20", "--destination-port", "443")
				contractCommand(t, true, applied(args...)...)
				want := contractapi.FirewallSeed()
				sourceFilter := map[string]any{"type": "IP_ADDRESS", "ipAddressFilter": map[string]any{"type": "IP_ADDRESSES", "matchOpposite": false, "items": []any{map[string]any{"type": sourceType, "value": sourceValue}}}}
				destinationFilter := map[string]any{"type": "IP_ADDRESS", "ipAddressFilter": map[string]any{"type": "IP_ADDRESSES", "matchOpposite": false, "items": []any{map[string]any{"type": "IP_ADDRESS", "value": "198.51.100.20"}}}, "portFilter": map[string]any{"type": "PORTS", "matchOpposite": false, "items": []any{map[string]any{"type": "PORT_NUMBER", "value": float64(443)}}}}
				want["source"].(map[string]any)["trafficFilter"] = sourceFilter
				want["destination"].(map[string]any)["trafficFilter"] = destinationFilter
				preserved(t, s, "firewall", contractapi.PolicyID, want)
				contractCommand(t, true, applied("firewall", "update", contractapi.PolicyID, "--name", "Renamed exact policy")...)
				want["name"] = "Renamed exact policy"
				preserved(t, s, "firewall", contractapi.PolicyID, want)
				contractCommand(t, true, applied("firewall", "delete", contractapi.PolicyID)...)
				if s.Attempts() != 3 || s.Snapshot("firewall", contractapi.PolicyID) != nil {
					t.Fatal("exact-filter cleanup failed")
				}
			})
		}
	}
}
