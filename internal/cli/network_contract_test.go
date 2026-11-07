package cli

import (
	"github.com/noahjenkins/unifi-cli/internal/testutil/contractapi"
	"strings"
	"testing"
)

func TestNetworkContractUpdates(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, mode := range []string{"GATEWAY", "SWITCH", "UNMANAGED"} {
			t.Run(version+"/"+mode, func(t *testing.T) {
				s := contractRuntime(t, version)
				seed := contractapi.NetworkSeed(mode)
				s.Seed("network", seed)
				contractCommand(t, true, "network", "update", contractapi.NetworkID, "--name", "Renamed")
				if s.Attempts() != 0 {
					t.Fatal("plan sent a write")
				}
				preserved(t, s, "network", contractapi.NetworkID, seed)
				contractCommand(t, true, applied("network", "update", contractapi.NetworkID, "--name", "Renamed")...)
				want := contractapi.Clone(seed)
				want["name"] = "Renamed"
				preserved(t, s, "network", contractapi.NetworkID, want)
				if s.Attempts() != 1 {
					t.Fatal("update not exactly once")
				}
				if mode == "GATEWAY" {
					for _, value := range []bool{true, false} {
						flag := "--mdns-forwarding=false"
						if value {
							flag = "--mdns-forwarding=true"
						}
						contractCommand(t, true, applied("network", "update", contractapi.NetworkID, flag)...)
						want["mdnsForwardingEnabled"] = value
						preserved(t, s, "network", contractapi.NetworkID, want)
					}
				} else {
					contractCommand(t, false, applied("network", "update", contractapi.NetworkID, "--mdns-forwarding=true")...)
					if s.Attempts() != 1 {
						t.Fatal("unsupported mode wrote")
					}
				}
			})
		}
	}
}
func TestContractFailurePathsAndGates(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, resource := range []string{"network", "wlan", "firewall"} {
			for _, kind := range []string{"no-yes", "dry-run", "no-experimental", "no-force", "drift", "mismatch-readback", "corrupt-write-response"} {
				t.Run(version+"/"+resource+"/"+kind, func(t *testing.T) {
					s := contractRuntime(t, version)
					seed := contractapi.NetworkSeed("GATEWAY")
					id := contractapi.NetworkID
					if resource == "wlan" {
						seed = contractapi.WLANSeed()
						id = contractapi.WLANID
					}
					if resource == "firewall" {
						seed = contractapi.FirewallSeed()
						id = contractapi.PolicyID
					}
					s.Seed(resource, seed)
					args := []string{resource, "update", id, "--name", "Intended rename"}
					wantOK := false
					wantWrites := 0
					switch kind {
					case "no-yes":
						args = append(args, "--experimental", "--force")
						wantOK = true
					case "dry-run":
						args = append(applied(args...), "--dry-run")
						wantOK = true
					case "no-experimental":
						args = append(args, "--yes", "--force")
					case "no-force":
						args = append(args, "--yes", "--experimental")
						if resource == "wlan" {
							wantOK = true
							wantWrites = 1
						}
					default:
						args = applied(args...)
						s.SetFault(contractapi.Fault{Resource: resource, Kind: kind})
						if kind != "drift" {
							wantWrites = 1
						}
					}
					out := contractCommand(t, wantOK, args...)
					if s.Attempts() != wantWrites {
						t.Fatalf("writes=%d want %d", s.Attempts(), wantWrites)
					}
					if !wantOK && out["data"] != nil {
						t.Fatal("failed operation reported completion data")
					}
					want := contractapi.Clone(seed)
					if wantWrites == 1 {
						want["name"] = "Intended rename"
					}
					if kind == "drift" {
						want["name"] = "Concurrent rename"
					}
					preserved(t, s, resource, id, want)
				})
			}
		}
	}
}
func TestNetworkContractMissingMDNSResponse(t *testing.T) {
	for _, version := range contractapi.Versions() {
		t.Run(version, func(t *testing.T) {
			s := contractRuntime(t, version)
			seed := contractapi.NetworkSeed("GATEWAY")
			s.Seed("network", seed)
			s.SetFault(contractapi.Fault{Resource: "network", Kind: "omit-mdns"})
			contractCommand(t, true, applied("network", "update", contractapi.NetworkID, "--mdns-forwarding=true")...)
			seed["mdnsForwardingEnabled"] = true
			preserved(t, s, "network", contractapi.NetworkID, seed)
			if s.Attempts() != 1 {
				t.Fatal("wrong mutation count")
			}
		})
	}
}
func TestNetworkContractCreateDelete(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, variant := range []string{"GATEWAY", "GATEWAY-no-dhcp", "SWITCH", "UNMANAGED"} {
			t.Run(version+"/"+variant, func(t *testing.T) {
				s := contractRuntime(t, version)
				args := []string{"network", "create", "--name", "Synthetic network", "--vlan", "20"}
				mode, _, _ := strings.Cut(variant, "-")
				want := contractapi.NetworkSeed(mode)
				switch variant {
				case "GATEWAY":
					args = append(args, "--management", "gateway", "--subnet", "192.0.2.1/24", "--dhcp-mode", "server", "--dhcp-range-start", "192.0.2.10", "--dhcp-range-end", "192.0.2.200", "--dhcp-lease-seconds", "86400", "--dhcp-conflict-detection", "--dns-server", "192.0.2.53", "--domain-name", "example.test")
				case "GATEWAY-no-dhcp":
					args = append(args, "--management", "gateway", "--subnet", "192.0.2.1/24", "--dhcp-mode", "none")
					delete(want["ipv4Configuration"].(map[string]any), "dhcpConfiguration")
				case "SWITCH":
					args = append(args, "--management", "switch", "--device", contractapi.DeviceID, "--subnet", "192.0.2.1/24")
				case "UNMANAGED":
					args = append(args, "--management", "unmanaged")
				}
				contractCommand(t, true, args...)
				contractCommand(t, false, append(args, "--yes")...)
				if s.Attempts() != 0 || s.Snapshot("network", contractapi.NetworkID) != nil {
					t.Fatal("create plan or experimental gate wrote")
				}
				contractCommand(t, true, applied(args...)...)
				preserved(t, s, "network", contractapi.NetworkID, want)
				contractCommand(t, false, "network", "delete", contractapi.NetworkID, "--yes", "--experimental")
				if s.Attempts() != 1 {
					t.Fatal("destructive delete wrote without force")
				}
				preserved(t, s, "network", contractapi.NetworkID, want)
				contractCommand(t, true, applied("network", "delete", contractapi.NetworkID)...)
				if s.Attempts() != 2 || s.Snapshot("network", contractapi.NetworkID) != nil {
					t.Fatal("network lifecycle did not restore empty state with two writes")
				}
			})
		}
	}
}
func TestContractCreateDeleteFaults(t *testing.T) {
	creates := map[string][]string{
		"network":  {"network", "create", "--name", "Synthetic network", "--vlan", "20", "--management", "unmanaged"},
		"wlan":     {"wlan", "create", "--name", "Synthetic WLAN", "--security", "open"},
		"firewall": firewallContractArgs("tcp", "ipv4"),
	}
	ids := map[string]string{"network": contractapi.NetworkID, "wlan": contractapi.WLANID, "firewall": contractapi.PolicyID}
	for _, version := range contractapi.Versions() {
		for _, resource := range []string{"network", "wlan", "firewall"} {
			for _, kind := range []string{"corrupt-write-response", "mismatch-readback", "retain-after-delete"} {
				t.Run(version+"/"+resource+"/"+kind, func(t *testing.T) {
					s := contractRuntime(t, version)
					id := ids[resource]
					if kind == "retain-after-delete" {
						contractCommand(t, true, applied(creates[resource]...)...)
					}
					s.SetFault(contractapi.Fault{Resource: resource, Kind: kind})
					args := applied(creates[resource]...)
					if kind == "retain-after-delete" {
						args = applied(resource, "delete", id)
					}
					writes := s.Attempts() + 1
					out := contractCommand(t, false, args...)
					if s.Attempts() != writes {
						t.Fatalf("writes=%d want %d; an ambiguous write must not be retried", s.Attempts(), writes)
					}
					if out["data"] != nil {
						t.Fatal("failed operation reported completion data")
					}
					if code := out["error"].(map[string]any)["code"]; kind != "corrupt-write-response" && code != "conflict" {
						t.Fatalf("verification failure code=%v, want conflict", code)
					}
					if s.Snapshot(resource, id) == nil {
						t.Fatal("fault scenario should leave the stored object in place")
					}
				})
			}
		}
	}
}
