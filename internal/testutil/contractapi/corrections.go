package contractapi

import (
	"fmt"
	"sort"
	"strings"
)

// correctNamedProtocolEnums is the sole reviewed correction to the pinned
// published contracts. Call only after verifying the original file digest.
// Results must be labeled "published schema plus reviewed correction".
func correctNamedProtocolEnums(profile Profile, doc map[string]any) error {
	pinned, err := ProfileFor(profile.Version)
	if err != nil || pinned != profile {
		return fmt.Errorf("correction profile not pinned")
	}
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	type change struct {
		property map[string]any
		keys     []string
	}
	changes := []change{}
	for name, count := range map[string]int{"Firewall policy IPv4 named protocol": 48, "Firewall policy IPv6 named protocol": 25, "Firewall policy IPv4 and IPv6 named protocol": 19} {
		schema, _ := schemas[name].(map[string]any)
		d, _ := schema["discriminator"].(map[string]any)
		properties, _ := schema["properties"].(map[string]any)
		property, _ := properties["name"].(map[string]any)
		mapping, _ := d["mapping"].(map[string]any)
		enums, _ := property["enum"].([]any)
		if d["propertyName"] != "name" || property["type"] != "string" || len(mapping) != count || len(enums) != 55 {
			return fmt.Errorf("protocol correction source shape changed")
		}
		keys := make([]string, 0, len(mapping))
		for key := range mapping {
			if key != strings.ToUpper(key) {
				return fmt.Errorf("protocol mapping shape changed")
			}
			keys = append(keys, key)
		}
		for _, entry := range enums {
			value, ok := entry.(string)
			if !ok || value != strings.ToLower(value) || mapping[value] != nil {
				return fmt.Errorf("protocol enum conflict changed")
			}
		}
		sort.Strings(keys)
		changes = append(changes, change{property, keys})
	}
	for _, c := range changes {
		values := make([]any, len(c.keys))
		for i, k := range c.keys {
			values[i] = k
		}
		c.property["enum"] = values
	}
	return nil
}
