package cli

import (
	"reflect"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/domain"
	"github.com/spf13/cobra"
)

func TestUpdateInputsExposeExplicitSetAndClearSemantics(t *testing.T) {
	tests := []struct {
		name   string
		typeOf reflect.Type
		fields []string
	}{
		{name: "network", typeOf: reflect.TypeOf(domain.NetworkInput{}), fields: []string{
			"SetName", "SetPurpose", "SetEnabled", "SetDeviceID", "SetSubnet", "SetDHCPMode",
			"SetDHCPRangeStart", "SetDHCPRangeStop", "SetDHCPLeaseTimeSeconds", "SetDHCPConflictDetectionEnabled",
			"SetDHCPRelayServerIPAddresses", "SetDNSServerIPAddresses", "SetDomainName", "ClearDomainName",
		}},
		{name: "wlan", typeOf: reflect.TypeOf(domain.WlanInput{}), fields: []string{
			"SetName", "SetSecurity", "SetNetwork", "SetPassword", "SetBand", "SetPMFMode",
			"SetSAEAnticloggingThresholdSeconds", "SetSAESyncTimeSeconds", "SetFastRoamingEnabled",
			"SetWPA3FastRoamingEnabled", "SetRadiusProfileID", "SetRadiusNASIDSource", "SetRadiusNASID",
			"SetCOAEnabled", "SetWPA3SecurityMode",
		}},
		{name: "port", typeOf: reflect.TypeOf(domain.PortInput{}), fields: []string{"SetName", "ClearName", "SetProfile"}},
		{name: "firewall", typeOf: reflect.TypeOf(domain.FirewallInput{}), fields: []string{"SetName", "SetDescription", "ClearDescription", "SetAction", "SetSourceZone", "SetDestinationZone", "SetIPVersion", "SetProtocol", "SetLoggingEnabled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, field := range tt.fields {
				if _, ok := tt.typeOf.FieldByName(field); !ok {
					t.Errorf("%s is missing explicit presence field %s", tt.typeOf.Name(), field)
				}
			}
		})
	}

	if newNetworkUpdateCmd().Flags().Lookup("clear-domain-name") == nil {
		t.Error("network update is missing --clear-domain-name")
	}
	for _, command := range []*cobra.Command{newNetworkCreateCmd(), newNetworkUpdateCmd()} {
		for _, flag := range []string{"device", "enabled", "dhcp-mode", "dhcp-range-start", "dhcp-range-end", "dhcp-lease-seconds", "dhcp-conflict-detection", "dhcp-relay-server", "dns-server", "domain-name"} {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("%s is missing --%s", command.CommandPath(), flag)
			}
		}
	}
	for _, command := range []*cobra.Command{newWlanCreateCmd(), newWlanUpdateCmd()} {
		for _, flag := range []string{"pmf-mode", "sae-anticlogging-seconds", "sae-sync-seconds", "fast-roaming", "wpa3-fast-roaming", "radius-profile", "radius-nas-id-source", "radius-nas-id", "coa", "wpa3-security-mode"} {
			if command.Flags().Lookup(flag) == nil {
				t.Errorf("%s is missing --%s", command.CommandPath(), flag)
			}
		}
	}
	if newPortUpdateCmd().Flags().Lookup("clear-name") == nil {
		t.Error("port update is missing --clear-name")
	}
	for _, flag := range []string{"description", "clear-description", "source-zone", "destination-zone", "ip-version", "protocol", "logging-enabled"} {
		if newFirewallUpdateCmd().Flags().Lookup(flag) == nil {
			t.Errorf("firewall update is missing --%s", flag)
		}
	}
	for _, obsolete := range []string{"ruleset", "src", "dst", "clear-src", "clear-dst", "index"} {
		if newFirewallCreateCmd().Flags().Lookup(obsolete) != nil || newFirewallUpdateCmd().Flags().Lookup(obsolete) != nil {
			t.Errorf("modern firewall policy commands still expose obsolete --%s", obsolete)
		}
	}
	if _, ok := reflect.TypeOf(domain.FirewallInput{}).FieldByName("Ruleset"); ok {
		t.Error("modern firewall input still exposes legacy Ruleset")
	}
}
