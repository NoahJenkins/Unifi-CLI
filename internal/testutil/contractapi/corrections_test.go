package contractapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPinnedProtocolContradictionAndCorrection(t *testing.T) {
	cache := os.Getenv("UNIFI_CONTRACT_CACHE")
	if cache == "" {
		t.Skip("explicit cached contract check")
	}
	for _, version := range Versions() {
		t.Run(version, func(t *testing.T) {
			p, _ := ProfileFor(version)
			path := filepath.Join(cache, version+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("cache unavailable")
			}
			var doc map[string]any
			if json.Unmarshal(raw, &doc) != nil {
				t.Fatal("invalid cache")
			}
			original, err := newSchemaEngine(doc)
			if err != nil {
				t.Fatal(err)
			}
			body := Writable("firewall", FirewallSeed())
			if err := original.validate("#/components/schemas/Create or update firewall policy", body); err == nil {
				t.Fatal("original uppercase enum conflict disappeared")
			}
			lower := Clone(body)
			lower["ipProtocolScope"].(map[string]any)["protocolFilter"].(map[string]any)["protocol"].(map[string]any)["name"] = "tcp"
			if err := original.validate("#/components/schemas/Create or update firewall policy", lower); err == nil {
				t.Fatal("original lowercase discriminator conflict disappeared")
			}
			corrected, err := NewValidator(p, cache)
			if err != nil {
				t.Fatal(err)
			}
			if err := corrected.Validate("firewall", false, body); err != nil {
				t.Fatal(err)
			}
			if err := corrected.Validate("firewall", false, lower); err == nil {
				t.Fatal("correction allowed lowercase key")
			}
			delete(body["action"].(map[string]any), "allowReturnTraffic")
			if err := corrected.Validate("firewall", false, body); err == nil {
				t.Fatal("correction weakened unrelated subtype")
			}
			after, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(raw, after) {
				t.Fatal("cache modified")
			}
			schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
			schemas["Firewall policy IPv4 named protocol"].(map[string]any)["discriminator"].(map[string]any)["propertyName"] = "wrong"
			if err := correctNamedProtocolEnums(p, doc); err == nil {
				t.Fatal("unexpected shape corrected")
			}
		})
	}
}
