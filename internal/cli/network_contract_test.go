package cli

import (
	"github.com/noahjenkins/unifi-cli/internal/testutil/contractapi"
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
