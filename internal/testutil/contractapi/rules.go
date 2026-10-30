package contractapi

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// CoverageError is an unmodeled input, not evidence of an upstream rejection.
type CoverageError struct{ Path string }

func (e *CoverageError) Error() string { return e.Path + ": outside offline coverage" }

type rule struct {
	kind                      string
	fields                    map[string]*rule
	required                  []string
	values                    []any
	min, max                  float64
	bounded, unique, nullable bool
	item                      *rule
	key                       string
	branches                  map[string]*rule
}

func obj(required string, fields map[string]*rule) *rule {
	return &rule{kind: "object", fields: fields, required: strings.Fields(required)}
}
func str(values ...string) *rule {
	r := &rule{kind: "string"}
	for _, v := range values {
		r.values = append(r.values, v)
	}
	return r
}
func number(min, max float64) *rule { return &rule{kind: "integer", min: min, max: max, bounded: true} }
func boolean() *rule                { return &rule{kind: "boolean"} }
func list(item *rule, min, max float64, unique bool) *rule {
	return &rule{kind: "array", item: item, min: min, max: max, bounded: true, unique: unique}
}
func union(key string, branches map[string]*rule) *rule {
	return &rule{kind: "object", key: key, branches: branches}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func uuid() *rule { return &rule{kind: "uuid"} }
func (r *rule) check(v any, path string) error {
	fail := func() error { return fmt.Errorf("%s: contract rule failed", path) }
	if v == nil && r.nullable {
		return nil
	}
	switch r.kind {
	case "object":
		o, ok := v.(map[string]any)
		if !ok {
			return fail()
		}
		if r.key != "" {
			key, ok := o[r.key].(string)
			if !ok {
				return fail()
			}
			branch := r.branches[key]
			if branch == nil {
				return &CoverageError{path + "/" + r.key}
			}
			return branch.check(o, path)
		}
		for _, name := range r.required {
			if _, ok := o[name]; !ok {
				return fmt.Errorf("%s/%s: required", path, name)
			}
		}
		for name, v := range o {
			field := r.fields[name]
			if field == nil {
				return &CoverageError{path}
			}
			if err := field.check(v, path+"/"+name); err != nil {
				return err
			}
		}
	case "string", "uuid":
		s, ok := v.(string)
		if !ok {
			return fail()
		}
		if r.kind == "uuid" && !uuidPattern.MatchString(s) {
			return fail()
		}
		if r.bounded && float64(utf8.RuneCountInString(s)) < r.min {
			return fail()
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fail()
		}
	case "integer", "number":
		n, ok := v.(float64)
		if !ok {
			return fail()
		}
		if r.kind == "integer" && n != math.Trunc(n) {
			return fail()
		}
		if r.bounded && (n < r.min || n > r.max) {
			return fail()
		}
	case "array":
		xs, ok := v.([]any)
		if !ok {
			return fail()
		}
		if float64(len(xs)) < r.min || float64(len(xs)) > r.max {
			return fail()
		}
		seen := map[string]bool{}
		for i, x := range xs {
			if err := r.item.check(x, fmt.Sprintf("%s/%d", path, i)); err != nil {
				return err
			}
			if r.unique {
				b, _ := json.Marshal(x)
				if seen[string(b)] {
					return fail()
				}
				seen[string(b)] = true
			}
		}
	}
	if len(r.values) > 0 {
		for _, want := range r.values {
			if v == want {
				return nil
			}
		}
		return fail()
	}
	return nil
}
func (v *Validator) Validate(resource string, response bool, value any) error {
	names, ok := resourceSchemas[resource]
	if !ok {
		return &CoverageError{"$"}
	}
	// Normalize JSON numbers and reject values which cannot occur on the wire.
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("$: invalid JSON value")
	}
	var normalized any
	if json.Unmarshal(b, &normalized) != nil {
		return fmt.Errorf("$: invalid JSON")
	}
	if v.engine != nil {
		i := 0
		if response {
			i = 1
		}
		return v.engine.validate("#/components/schemas/"+escape(names[i]), normalized)
	}
	r := resourceRule(resource, response)
	return r.check(normalized, "$")
}
func resourceRule(resource string, response bool) *rule {
	var r *rule
	switch resource {
	case "firewall":
		r = firewallRule()
	case "network":
		r = networkRule()
	case "wlan":
		r = wlanRule()
	}
	if !response {
		return r
	}
	// Response roots use the same writable subset plus independent server fields.
	var add func(*rule)
	add = func(x *rule) {
		if x.branches != nil {
			for _, b := range x.branches {
				add(b)
			}
			return
		}
		x.fields["id"] = uuid()
		x.fields["metadata"] = obj("origin", map[string]*rule{"origin": str("USER_DEFINED")})
		x.required = append(x.required, "id", "metadata")
		if resource == "network" {
			x.fields["default"] = boolean()
			x.required = append(x.required, "default")
		}
		if resource == "firewall" {
			x.fields["index"] = number(0, 2147483647)
			x.required = append(x.required, "index")
		}
	}
	add(r)
	if resource == "network" {
		r.branches["GATEWAY"].required = append(r.branches["GATEWAY"].required, "mdnsForwardingEnabled")
	}
	return r
}
func firewallRule() *rule {
	action := union("type", map[string]*rule{
		"ALLOW": obj("type allowReturnTraffic", map[string]*rule{"type": str("ALLOW"), "allowReturnTraffic": boolean()}),
		"BLOCK": obj("type", map[string]*rule{"type": str("BLOCK")}), "REJECT": obj("type", map[string]*rule{"type": str("REJECT")})})
	address := union("type", map[string]*rule{"IP_ADDRESS": obj("type value", map[string]*rule{"type": str("IP_ADDRESS"), "value": str()}), "SUBNET": obj("type value", map[string]*rule{"type": str("SUBNET"), "value": str()})})
	ips := obj("type matchOpposite items", map[string]*rule{"type": str("IP_ADDRESSES"), "matchOpposite": boolean(), "items": list(address, 1, math.MaxInt32, true)})
	port := obj("type value", map[string]*rule{"type": str("PORT_NUMBER"), "value": number(1, 65535)})
	ports := obj("type matchOpposite items", map[string]*rule{"type": str("PORTS"), "matchOpposite": boolean(), "items": list(port, 1, math.MaxInt32, true)})
	endpoint := func() *rule {
		return obj("zoneId", map[string]*rule{"zoneId": uuid(), "trafficFilter": obj("type ipAddressFilter", map[string]*rule{"type": str("IP_ADDRESS"), "ipAddressFilter": ips, "portFilter": ports})})
	}
	scopes := map[string]*rule{}
	for _, ip := range []string{"IPV4", "IPV6", "IPV4_AND_IPV6"} {
		names := []string{"TCP", "UDP"}
		if ip == "IPV4" {
			names = append(names, "ICMP")
		}
		if ip == "IPV6" {
			names = append(names, "ICMPV6")
		}
		filter := union("type", map[string]*rule{
			"NAMED_PROTOCOL": obj("type protocol matchOpposite", map[string]*rule{"type": str("NAMED_PROTOCOL"), "matchOpposite": boolean(), "protocol": obj("name", map[string]*rule{"name": str(names...)})}),
			"PRESET":         obj("type preset", map[string]*rule{"type": str("PRESET"), "preset": obj("name", map[string]*rule{"name": str("TCP_UDP")})})})
		scopes[ip] = obj("ipVersion", map[string]*rule{"ipVersion": str(ip), "protocolFilter": filter})
	}
	name := str()
	name.min = 1
	name.bounded = true
	return obj("action destination enabled ipProtocolScope loggingEnabled name source", map[string]*rule{"name": name, "description": str(), "enabled": boolean(), "loggingEnabled": boolean(), "action": action, "source": endpoint(), "destination": endpoint(), "ipProtocolScope": union("ipVersion", scopes)})
}
func networkRule() *rule {
	modes := map[string]*rule{}
	for _, mode := range []string{"GATEWAY", "SWITCH", "UNMANAGED"} {
		fields := map[string]*rule{"name": str(), "enabled": boolean(), "management": str(mode), "vlanId": number(1, 4009)}
		required := "name enabled management vlanId"
		if mode != "UNMANAGED" {
			ipv4 := obj("autoScaleEnabled hostIpAddress prefixLength", map[string]*rule{"autoScaleEnabled": boolean(), "hostIpAddress": str(), "prefixLength": number(8, 30)})
			if mode == "GATEWAY" {
				ipv4.fields["dhcpConfiguration"] = obj("mode ipAddressRange leaseTimeSeconds pingConflictDetectionEnabled", map[string]*rule{"mode": str("SERVER"), "domainName": str(), "dnsServerIpAddressesOverride": list(str(), 1, 4, false), "ipAddressRange": obj("start stop", map[string]*rule{"start": str(), "stop": str()}), "leaseTimeSeconds": number(0, 31536000), "pingConflictDetectionEnabled": boolean()})
			}
			fields["ipv4Configuration"] = ipv4
			fields["cellularBackupEnabled"] = boolean()
			fields["isolationEnabled"] = boolean()
			required += " ipv4Configuration cellularBackupEnabled isolationEnabled"
			if mode == "GATEWAY" {
				fields["internetAccessEnabled"] = boolean()
				fields["mdnsForwardingEnabled"] = boolean()
				required += " internetAccessEnabled"
			} else {
				fields["deviceId"] = uuid()
				required += " deviceId"
			}
		}
		modes[mode] = obj(required, fields)
	}
	return union("management", modes)
}
func wlanRule() *rule {
	fields := map[string]*rule{"type": str("STANDARD"), "name": str()}
	required := "type name securityConfiguration broadcastingFrequenciesGHz"
	for _, name := range strings.Fields("advertiseDeviceName arpProxyEnabled bssTransitionEnabled channel2gLockedTo6 clientIsolationEnabled dtimPeriod2gLockedTo3 enabled hideName multicastToUnicastConversionEnabled uapsdEnabled") {
		fields[name] = boolean()
		required += " " + name
	}
	fields["bandSteeringEnabled"] = boolean()
	fields["mloEnabled"] = boolean()
	fields["broadcastingFrequenciesGHz"] = list(&rule{kind: "number", values: []any{2.4, float64(5), float64(6)}}, 1, math.MaxInt32, true)
	fields["securityConfiguration"] = obj("type", map[string]*rule{"type": str("OPEN"), "encryption": str("ENHANCED_OPEN", "ENHANCED_OPEN_WITH_TRANSITION")})
	fields["network"] = obj("type networkId", map[string]*rule{"type": str("SPECIFIC"), "networkId": uuid()})
	filter := union("type", map[string]*rule{"DEVICES": obj("type deviceIds", map[string]*rule{"type": str("DEVICES"), "deviceIds": list(uuid(), 1, math.MaxInt32, true)}), "DEVICE_TAGS": obj("type deviceTagIds", map[string]*rule{"type": str("DEVICE_TAGS"), "deviceTagIds": list(uuid(), 1, math.MaxInt32, true)})})
	filter.nullable = true
	fields["broadcastingDeviceFilter"] = filter
	return obj(required, fields)
}
