package contractapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Profile struct{ Version, SHA256 string }

func ProfileFor(version string) (Profile, error) {
	switch version {
	case "10.4.57":
		return Profile{version, "3773947b4572b1bdc84272d3d2b9ebb5cad0d9aaa2640c3418568df97c9df59d"}, nil
	case "10.6.109":
		return Profile{version, "2356b910f76c175db9a22ce18070c2c40af02d9843ca67628410a5afbfc5086c"}, nil
	default:
		return Profile{}, fmt.Errorf("unsupported contract profile")
	}
}
func Versions() []string { return []string{"10.4.57", "10.6.109"} }

type Validator struct {
	profile Profile
	engine  *schemaEngine
}

func NewValidator(profile Profile, cacheDir string) (*Validator, error) {
	pinned, err := ProfileFor(profile.Version)
	if err != nil || pinned != profile {
		return nil, fmt.Errorf("invalid contract profile")
	}
	v := &Validator{profile: profile}
	if cacheDir == "" {
		return v, nil
	}
	path := filepath.Join(cacheDir, profile.Version+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
		return nil, fmt.Errorf("contract cache missing or invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("contract cache unreadable")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != profile.SHA256 {
		return nil, fmt.Errorf("contract cache digest mismatch")
	}
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return nil, fmt.Errorf("contract cache is not JSON")
	}
	metadata, _ := doc["info"].(map[string]any)
	if metadata["version"] != profile.Version || doc["openapi"] != "3.1.0" {
		return nil, fmt.Errorf("contract version mismatch")
	}
	if err := correctNamedProtocolEnums(profile, doc); err != nil {
		return nil, err
	}
	v.engine, err = newSchemaEngine(doc)
	if err != nil {
		return nil, err
	}
	// Both pinned documents explicitly describe null as "all APs" for this
	// filter, despite omitting a JSON Schema type assertion on its base.
	// Other discriminator containers must be objects, including typeless bases.
	v.engine.nullableDiscriminators = map[string]bool{
		"#/components/schemas/Broadcasting device filter": true,
	}
	for _, names := range resourceSchemas {
		for _, name := range names {
			if _, err := v.engine.schema("#/components/schemas/" + escape(name)); err != nil {
				return nil, err
			}
		}
	}
	return v, nil
}

var resourceSchemas = map[string][2]string{
	"firewall": {"Create or update firewall policy", "Firewall policy"},
	"network":  {"Create or update Network", "Network details"},
	"wlan":     {"Wifi broadcast create or update", "Wifi broadcast details"},
}
