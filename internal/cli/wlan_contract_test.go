package cli

import (
	"github.com/noahjenkins/unifi-cli/internal/testutil/contractapi"
	"testing"
)

func TestWLANContractUpdates(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, variant := range []string{"absent", "null", "DEVICES", "DEVICE_TAGS"} {
			t.Run(version+"/"+variant, func(t *testing.T) {
				s := contractRuntime(t, version)
				seed := contractapi.WLANSeed()
				switch variant {
				case "null":
					seed["broadcastingDeviceFilter"] = nil
				case "DEVICES":
					seed["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICES", "deviceIds": []any{contractapi.DeviceID}}
				case "DEVICE_TAGS":
					seed["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICE_TAGS", "deviceTagIds": []any{contractapi.TagID}}
				}
				s.Seed("wlan", seed)
				contractCommand(t, true, "wlan", "update", contractapi.WLANID, "--name", "Renamed WLAN")
				if s.Attempts() != 0 {
					t.Fatal("plan wrote")
				}
				preserved(t, s, "wlan", contractapi.WLANID, seed)
				contractCommand(t, true, applied("wlan", "update", contractapi.WLANID, "--name", "Renamed WLAN")...)
				want := contractapi.Clone(seed)
				want["name"] = "Renamed WLAN"
				preserved(t, s, "wlan", contractapi.WLANID, want)
				contractCommand(t, true, applied("wlan", "update", contractapi.WLANID, "--network", contractapi.OtherNetworkID)...)
				want["network"] = map[string]any{"type": "SPECIFIC", "networkId": contractapi.OtherNetworkID}
				preserved(t, s, "wlan", contractapi.WLANID, want)
				// Use a different synthetic device to ensure this is a change for DEVICES.
				const selected = "77777777-7777-4777-8777-777777777778"
				contractCommand(t, true, applied("wlan", "update", contractapi.WLANID, "--broadcasting-ap", selected)...)
				want["broadcastingDeviceFilter"] = map[string]any{"type": "DEVICES", "deviceIds": []any{selected}}
				preserved(t, s, "wlan", contractapi.WLANID, want)
				contractCommand(t, true, applied("wlan", "update", contractapi.WLANID, "--all-aps")...)
				want["broadcastingDeviceFilter"] = nil
				preserved(t, s, "wlan", contractapi.WLANID, want)
				if s.Attempts() != 4 {
					t.Fatal("WLAN updates not exactly once")
				}
			})
		}
	}
}
func TestContractRuntimeClearsAmbientSettings(t *testing.T) {
	for key, value := range map[string]string{"UNIFI_PROFILE": "unavailable", "UNIFI_CA_CERT": "/nonexistent-operator-ca", "UNIFI_SAFE_MODE": "false", "UNIFI_HOST": "outside.invalid", "UNIFI_API_KEY": "unused-synthetic-value", "HTTPS_PROXY": "http://127.0.0.1:1"} {
		t.Setenv(key, value)
	}
	s := contractRuntime(t, "10.4.57")
	seed := contractapi.NetworkSeed("GATEWAY")
	s.Seed("network", seed)
	contractCommand(t, false, "network", "update", contractapi.NetworkID, "--name", "Blocked", "--yes", "--experimental")
	if s.Attempts() != 0 {
		t.Fatal("ambient safe-mode override bypassed force gate")
	}
	preserved(t, s, "network", contractapi.NetworkID, seed)
	contractCommand(t, true, applied("network", "update", contractapi.NetworkID, "--name", "Allowed")...)
	seed["name"] = "Allowed"
	preserved(t, s, "network", contractapi.NetworkID, seed)
}
func TestWLANContractLifecycle(t *testing.T) {
	for _, version := range contractapi.Versions() {
		for _, network := range []string{"NATIVE", "SPECIFIC"} {
			t.Run(version+"/"+network, func(t *testing.T) {
				s := contractRuntime(t, version)
				args := []string{"wlan", "create", "--name", "Synthetic WLAN", "--security", "open"}
				want := map[string]any{"id": contractapi.WLANID, "metadata": map[string]any{"origin": "USER_DEFINED"}, "type": "STANDARD", "name": "Synthetic WLAN", "enabled": true, "hideName": false, "clientIsolationEnabled": false, "multicastToUnicastConversionEnabled": false, "uapsdEnabled": true, "advertiseDeviceName": false, "arpProxyEnabled": false, "bssTransitionEnabled": true, "channel2gLockedTo6": false, "dtimPeriod2gLockedTo3": false, "broadcastingFrequenciesGHz": []any{2.4, float64(5)}, "securityConfiguration": map[string]any{"type": "OPEN"}, "network": map[string]any{"type": "NATIVE"}}
				if network == "SPECIFIC" {
					args = append(args, "--network", contractapi.NetworkID)
					want["network"] = map[string]any{"type": "SPECIFIC", "networkId": contractapi.NetworkID}
				}
				contractCommand(t, true, args...)
				contractCommand(t, false, append(args, "--yes")...)
				if s.Attempts() != 0 || s.Snapshot("wlan", contractapi.WLANID) != nil {
					t.Fatal("create plan or experimental gate wrote")
				}
				contractCommand(t, true, append(args, "--yes", "--experimental")...)
				preserved(t, s, "wlan", contractapi.WLANID, want)
				contractCommand(t, true, "wlan", "disable", contractapi.WLANID, "--yes", "--experimental")
				want["enabled"] = false
				preserved(t, s, "wlan", contractapi.WLANID, want)
				contractCommand(t, true, "wlan", "enable", contractapi.WLANID, "--yes", "--experimental")
				want["enabled"] = true
				preserved(t, s, "wlan", contractapi.WLANID, want)
				contractCommand(t, false, "wlan", "delete", contractapi.WLANID, "--yes", "--experimental")
				if s.Attempts() != 3 {
					t.Fatal("destructive delete wrote without force")
				}
				preserved(t, s, "wlan", contractapi.WLANID, want)
				contractCommand(t, true, applied("wlan", "delete", contractapi.WLANID)...)
				if s.Attempts() != 4 || s.Snapshot("wlan", contractapi.WLANID) != nil {
					t.Fatal("WLAN lifecycle did not restore empty state with four writes")
				}
			})
		}
	}
}
