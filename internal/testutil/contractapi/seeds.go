package contractapi

import "encoding/json"

const (
	SiteID            = "11111111-1111-4111-8111-111111111111"
	NetworkID         = "22222222-2222-4222-8222-222222222222"
	OtherNetworkID    = "22222222-2222-4222-8222-222222222223"
	WLANID            = "33333333-3333-4333-8333-333333333333"
	PolicyID          = "44444444-4444-4444-8444-444444444444"
	SourceZoneID      = "55555555-5555-4555-8555-555555555555"
	DestinationZoneID = "66666666-6666-4666-8666-666666666666"
	DeviceID          = "77777777-7777-4777-8777-777777777777"
	TagID             = "88888888-8888-4888-8888-888888888888"
)

func Clone(doc map[string]any) map[string]any {
	b, err := json.Marshal(doc)
	if err != nil {
		panic("non-JSON synthetic document")
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		panic("invalid synthetic document")
	}
	return out
}
func Writable(resource string, doc map[string]any) map[string]any {
	out := Clone(doc)
	delete(out, "id")
	delete(out, "metadata")
	if resource == "network" {
		delete(out, "default")
	}
	if resource == "firewall" {
		delete(out, "index")
	}
	return out
}
func metadata() map[string]any { return map[string]any{"origin": "USER_DEFINED"} }
func FirewallSeed() map[string]any {
	return Clone(map[string]any{"id": PolicyID, "metadata": metadata(), "index": 0, "name": "Synthetic policy", "description": "Preserve this optional description", "enabled": true, "loggingEnabled": false, "action": map[string]any{"type": "ALLOW", "allowReturnTraffic": true}, "source": map[string]any{"zoneId": SourceZoneID}, "destination": map[string]any{"zoneId": DestinationZoneID}, "ipProtocolScope": map[string]any{"ipVersion": "IPV4", "protocolFilter": map[string]any{"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": "TCP"}, "matchOpposite": false}}})
}
func NetworkSeed(mode string) map[string]any {
	d := map[string]any{"id": NetworkID, "metadata": metadata(), "default": false, "management": mode, "name": "Synthetic network", "enabled": true, "vlanId": 20}
	if mode != "UNMANAGED" {
		d["cellularBackupEnabled"] = false
		d["isolationEnabled"] = false
		ip := map[string]any{"hostIpAddress": "192.0.2.1", "prefixLength": 24, "autoScaleEnabled": false}
		d["ipv4Configuration"] = ip
		if mode == "GATEWAY" {
			d["internetAccessEnabled"] = true
			d["mdnsForwardingEnabled"] = false
			ip["dhcpConfiguration"] = map[string]any{"mode": "SERVER", "domainName": "example.test", "dnsServerIpAddressesOverride": []any{"192.0.2.53"}, "ipAddressRange": map[string]any{"start": "192.0.2.10", "stop": "192.0.2.200"}, "leaseTimeSeconds": 86400, "pingConflictDetectionEnabled": true}
		} else {
			d["deviceId"] = DeviceID
		}
	}
	return Clone(d)
}
func WLANSeed() map[string]any {
	return Clone(map[string]any{"id": WLANID, "metadata": metadata(), "type": "STANDARD", "name": "Synthetic WLAN", "enabled": true, "channel2gLockedTo6": false, "dtimPeriod2gLockedTo3": false, "clientIsolationEnabled": true, "hideName": false, "multicastToUnicastConversionEnabled": false, "uapsdEnabled": true, "advertiseDeviceName": true, "arpProxyEnabled": false, "bssTransitionEnabled": true, "bandSteeringEnabled": true, "mloEnabled": false, "broadcastingFrequenciesGHz": []any{2.4, 5}, "securityConfiguration": map[string]any{"type": "OPEN"}, "network": map[string]any{"type": "SPECIFIC", "networkId": NetworkID}})
}
