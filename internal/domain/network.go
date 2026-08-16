package domain

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type NetworkAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Network struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Purpose     string `json:"purpose"`
	VLAN        *int   `json:"vlan,omitempty"`
	Subnet      string `json:"subnet"`
	DHCPEnabled bool   `json:"dhcp_enabled"`
	DomainName  string `json:"domain_name"`
	WAN         bool   `json:"wan"`
}

func (n Network) GetID() string   { return n.ID }
func (n Network) GetMAC() string  { return "" }
func (n Network) GetName() string { return n.Name }

// NetworkInput is the create/update payload from CLI flags.
type NetworkInput struct {
	Name                            string
	SetName                         bool
	Purpose                         string
	SetPurpose                      bool
	Enabled                         bool
	SetEnabled                      bool
	VLAN                            *int
	DeviceID                        string
	SetDeviceID                     bool
	Subnet                          string
	SetSubnet                       bool
	DHCPEnabled                     bool
	SetDHCPEnabled                  bool
	DHCPMode                        string
	SetDHCPMode                     bool
	DHCPRangeStart                  string
	SetDHCPRangeStart               bool
	DHCPRangeStop                   string
	SetDHCPRangeStop                bool
	DHCPLeaseTimeSeconds            int
	SetDHCPLeaseTimeSeconds         bool
	DHCPConflictDetectionEnabled    bool
	SetDHCPConflictDetectionEnabled bool
	DHCPRelayServerIPAddresses      []string
	SetDHCPRelayServerIPAddresses   bool
	DNSServerIPAddresses            []string
	SetDNSServerIPAddresses         bool
	DomainName                      string
	SetDomainName                   bool
	ClearDomainName                 bool
}

type NetworkService struct {
	api NetworkAPI
}

type networkDocument struct {
	normalized Network
	wire       map[string]any
}

func NewNetworkService(api NetworkAPI) *NetworkService {
	return &NetworkService{api: api}
}

func (s *NetworkService) List(ctx context.Context) ([]Network, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "networks")
	if err != nil {
		return nil, err
	}
	if !official {
		path := s.api.SitePath(client.PathRestNetwork)
		if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
	}
	out := make([]Network, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeNetwork(m))
	}
	return out, nil
}

func (s *NetworkService) Get(ctx context.Context, id string) (Network, error) {
	items, err := s.List(ctx)
	if err != nil {
		return Network{}, err
	}
	overview, err := resolve.One(items, id)
	if err != nil {
		return Network{}, err
	}
	if !supportsOfficialDetails(s.api) || !looksLikeUUID(overview.ID) {
		return overview, nil
	}
	detail, err := fetchOfficialSiteDetail(s.api, ctx, overview.ID, "networks")
	if err != nil {
		return Network{}, err
	}
	return mergeOfficialNetworkDetail(overview, NormalizeNetwork(detail)), nil
}

func (s *NetworkService) listLegacy(ctx context.Context) ([]Network, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestNetwork), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(raw))
	for _, item := range raw {
		out = append(out, NormalizeNetwork(item))
	}
	return out, nil
}

func (s *NetworkService) getLegacy(ctx context.Context, id string) (Network, error) {
	items, err := s.listLegacy(ctx)
	if err != nil {
		return Network{}, err
	}
	if item, ok := findExactID(items, id); ok {
		return item, nil
	}
	if !looksLikeUUID(id) {
		return resolve.One(items, id)
	}
	raw, official, err := fetchOfficialSite(s.api, ctx, "networks")
	if err != nil {
		return Network{}, err
	}
	if !official {
		return resolve.One(items, id)
	}
	officialItems := make([]Network, 0, len(raw))
	for _, item := range raw {
		officialItems = append(officialItems, NormalizeNetwork(item))
	}
	return resolveLegacyMutationTarget(items, officialItems, id, "network", func(a, b Network) bool { return sameName(a, b) })
}

func (s *NetworkService) Create(ctx context.Context, in NetworkInput) (plan.Plan, error) {
	if err := validateNetworkCreate(in); err != nil {
		return plan.Plan{}, err
	}
	after := networkInputBody(in)
	if supportsOfficialDetails(s.api) {
		var err error
		after, err = officialNetworkCreateBody(in)
		if err != nil {
			return plan.Plan{}, err
		}
	}
	_ = ctx
	p := plan.Create("network", in.Name,
		fmt.Sprintf("create network %s", in.Name),
		after,
	)
	return p, nil
}

func (s *NetworkService) ApplyCreate(ctx context.Context, in NetworkInput) (Network, error) {
	if err := validateNetworkCreate(in); err != nil {
		return Network{}, err
	}
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialCreate(ctx, in)
	}
	path := s.api.SitePath(client.PathRestNetwork)
	body := networkInputBody(in)
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return Network{}, err
	}
	if len(raw) > 0 {
		return NormalizeNetwork(raw[0]), nil
	}
	// Fallback: synthesize from input when controller returns empty data.
	n := Network{
		Name:        in.Name,
		Purpose:     in.Purpose,
		VLAN:        in.VLAN,
		Subnet:      in.Subnet,
		DHCPEnabled: in.DHCPEnabled,
		DomainName:  in.DomainName,
		WAN:         in.Purpose == "wan",
	}
	return n, nil
}

func (s *NetworkService) Update(ctx context.Context, id string, in NetworkInput) (plan.Plan, Network, error) {
	if err := validateNetworkUpdate(in); err != nil {
		return plan.Plan{}, Network{}, err
	}
	if supportsOfficialDetails(s.api) {
		doc, body, err := s.prepareOfficialUpdate(ctx, id, in)
		if err != nil {
			return plan.Plan{}, Network{}, err
		}
		p := plan.Update("network", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update network %s", doc.normalized.Name), officialNetworkSnapshot(doc.wire), officialNetworkSnapshot(networkResponseView(body, doc.wire)))
		return p, doc.normalized, nil
	}
	n, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Network{}, err
	}
	before := networkSnapshot(n)
	after := mergeNetworkAfter(n, in)
	p := plan.Update("network", n.ID, n.Name,
		fmt.Sprintf("update network %s", n.Name),
		before,
		after,
	)
	return p, n, nil
}

func (s *NetworkService) ApplyUpdate(ctx context.Context, id string, in NetworkInput) (Network, error) {
	return s.applyUpdate(ctx, id, in, nil)
}

func (s *NetworkService) ApplyUpdatePrepared(ctx context.Context, target plan.Target, id string, in NetworkInput) (Network, error) {
	return s.applyUpdate(ctx, id, in, &target)
}

func (s *NetworkService) applyUpdate(ctx context.Context, id string, in NetworkInput, target *plan.Target) (Network, error) {
	if err := validateNetworkUpdate(in); err != nil {
		return Network{}, err
	}
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialUpdate(ctx, id, in, target)
	}
	n, err := s.getLegacy(ctx, id)
	if err != nil {
		return Network{}, err
	}
	if target != nil {
		p := plan.Update("network", n.ID, n.Name,
			fmt.Sprintf("update network %s", n.Name), networkSnapshot(n), mergeNetworkAfter(n, in))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Network{}, err
		}
	}
	path := s.api.SitePath(client.PathRestNetwork, n.ID)
	body := networkInputBody(in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Network{}, err
	}
	if inputSetsNetworkName(in) {
		n.Name = in.Name
	}
	if inputSetsNetworkPurpose(in) {
		n.Purpose = in.Purpose
		n.WAN = in.Purpose == "wan"
	}
	if inputSetsNetworkSubnet(in) {
		n.Subnet = in.Subnet
	}
	if inputSetsNetworkDomain(in) {
		n.DomainName = in.DomainName
	}
	if in.ClearDomainName {
		n.DomainName = ""
	}
	if in.VLAN != nil {
		n.VLAN = in.VLAN
	}
	if in.SetDHCPEnabled || in.DHCPEnabled {
		n.DHCPEnabled = in.DHCPEnabled
	}
	return n, nil
}

func (s *NetworkService) Delete(ctx context.Context, id string) (plan.Plan, Network, error) {
	if supportsOfficialDetails(s.api) {
		doc, err := s.resolveOfficialDocument(ctx, id)
		if err != nil {
			return plan.Plan{}, Network{}, err
		}
		p := plan.Delete("network", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("delete network %s", doc.normalized.Name), networkSnapshot(doc.normalized))
		return p, doc.normalized, nil
	}
	n, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Network{}, err
	}
	p := plan.Delete("network", n.ID, n.Name,
		fmt.Sprintf("delete network %s", n.Name),
		networkSnapshot(n),
	)
	return p, n, nil
}

func (s *NetworkService) ApplyDelete(ctx context.Context, id string) (Network, error) {
	return s.applyDelete(ctx, id, nil)
}

func (s *NetworkService) ApplyDeletePrepared(ctx context.Context, target plan.Target, id string) (Network, error) {
	return s.applyDelete(ctx, id, &target)
}

func (s *NetworkService) applyDelete(ctx context.Context, id string, target *plan.Target) (Network, error) {
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialDelete(ctx, id, target)
	}
	n, err := s.getLegacy(ctx, id)
	if err != nil {
		return Network{}, err
	}
	if target != nil {
		p := plan.Delete("network", n.ID, n.Name,
			fmt.Sprintf("delete network %s", n.Name), networkSnapshot(n))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Network{}, err
		}
	}
	path := s.api.SitePath(client.PathRestNetwork, n.ID)
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return Network{}, err
	}
	return n, nil
}

// NetworkDeleteDestructive reports whether deleting a network requires
// safe_mode --force. Every network deletion is destructive.
func NetworkDeleteDestructive(Network) bool {
	return true
}

func NormalizeNetwork(m map[string]any) Network {
	n := Network{
		ID:          strField(m, "_id", "id"),
		Name:        strField(m, "name"),
		Purpose:     strings.ToLower(strField(m, "purpose", "management")),
		Subnet:      strField(m, "ip_subnet", "subnet"),
		DHCPEnabled: boolField(m, "dhcpd_enabled"),
		DomainName:  strField(m, "domain_name"),
	}
	if ipv4, ok := m["ipv4Configuration"].(map[string]any); ok {
		host := strField(ipv4, "hostIpAddress")
		prefix := intField(ipv4, "prefixLength")
		if host != "" && prefix > 0 {
			n.Subnet = host + "/" + strconv.Itoa(prefix)
		}
		if dhcp, ok := ipv4["dhcpConfiguration"].(map[string]any); ok {
			n.DHCPEnabled = true
			n.DomainName = strField(dhcp, "domainName")
		}
	}
	if n.Purpose == "wan" {
		n.WAN = true
	}
	if v, ok := asInt(m["vlan"]); ok {
		// Treat vlan 0 as absent when vlan_enabled is explicitly false.
		if enabled, has := m["vlan_enabled"]; has && !boolField(map[string]any{"vlan_enabled": enabled}, "vlan_enabled") {
			// leave nil
		} else if v != 0 || boolField(m, "vlan_enabled") {
			n.VLAN = &v
		}
	}
	if v, ok := asInt(m["vlanId"]); ok {
		n.VLAN = &v
	}
	return n
}

func (s *NetworkService) resolveOfficialDocument(ctx context.Context, query string) (networkDocument, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "networks")
	if err != nil {
		return networkDocument{}, err
	}
	if !official {
		return networkDocument{}, apperr.New(apperr.Internal, "official network transport is unavailable")
	}
	items := make([]Network, 0, len(raw))
	for _, item := range raw {
		items = append(items, NormalizeNetwork(item))
	}
	selected, err := resolve.One(items, query)
	if err != nil {
		return networkDocument{}, err
	}
	if !looksLikeUUID(selected.ID) {
		return networkDocument{}, apperr.New(apperr.Conflict, "official network target has an invalid ID")
	}
	wire, err := fetchOfficialSiteDetail(s.api, ctx, selected.ID, "networks")
	if err != nil {
		return networkDocument{}, err
	}
	if strField(wire, "id") != selected.ID {
		return networkDocument{}, apperr.New(apperr.Conflict, "official network detail returned an ambiguous ID")
	}
	return networkDocument{normalized: NormalizeNetwork(wire), wire: deepCloneMap(wire)}, nil
}

func networkWritableDocument(raw map[string]any) map[string]any {
	body := deepCloneMap(raw)
	delete(body, "id")
	delete(body, "default")
	delete(body, "metadata")
	return body
}

func networkResponseView(body, existing map[string]any) map[string]any {
	view := deepCloneMap(body)
	for _, key := range []string{"id", "default", "metadata"} {
		if value, ok := existing[key]; ok {
			view[key] = deepCloneValue(value)
		}
	}
	return view
}

func (s *NetworkService) applyOfficialCreate(ctx context.Context, in NetworkInput) (Network, error) {
	transport, err := requireOfficialMutationAPI(s.api)
	if err != nil {
		return Network{}, err
	}
	body, err := officialNetworkCreateBody(in)
	if err != nil {
		return Network{}, err
	}
	path, err := transport.IntegrationSitePath(ctx, "networks")
	if err != nil {
		return Network{}, err
	}
	var created map[string]any
	if err := transport.DoOfficial(ctx, http.MethodPost, path, body, &created); err != nil {
		return Network{}, err
	}
	id := strField(created, "id")
	if !looksLikeUUID(id) {
		return Network{}, apperr.New(apperr.Conflict, "network create result is unverified: controller response is missing a valid network ID")
	}
	observed, err := fetchOfficialSiteDetail(s.api, ctx, id, "networks")
	if err != nil {
		return Network{}, verificationError("created network could not be verified", err)
	}
	if err := requireObservedResourceID(observed, id, "network create"); err != nil {
		return Network{}, err
	}
	if !wireDocumentContains(networkWritableDocument(observed), body, nil) {
		return Network{}, apperr.New(apperr.Conflict, "network create verification failed: observed writable document differs from requested state")
	}
	return NormalizeNetwork(observed), nil
}

func (s *NetworkService) prepareOfficialUpdate(ctx context.Context, query string, in NetworkInput) (networkDocument, map[string]any, error) {
	doc, err := s.resolveOfficialDocument(ctx, query)
	if err != nil {
		return networkDocument{}, nil, err
	}
	body := networkWritableDocument(doc.wire)
	if in.SetEnabled {
		body["enabled"] = in.Enabled
	}
	if inputSetsNetworkName(in) {
		body["name"] = in.Name
	}
	currentManagement := strings.ToUpper(strField(doc.wire, "management"))
	if inputSetsNetworkPurpose(in) {
		management, err := officialNetworkManagement(in.Purpose)
		if err != nil {
			return networkDocument{}, nil, err
		}
		if management != currentManagement {
			body, err = officialNetworkTransitionBody(doc.wire, in, management)
			if err != nil {
				return networkDocument{}, nil, err
			}
			if wireDocumentsEqual(body, networkWritableDocument(doc.wire)) {
				return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "network update would not change controller state")
			}
			return doc, body, nil
		}
	}
	if currentManagement == "UNMANAGED" && inputSetsOfficialNetworkManagedField(in) {
		return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "unmanaged networks do not accept device, IPv4, or DHCP fields")
	}
	if in.SetDeviceID {
		if currentManagement != "SWITCH" {
			return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "a network device ID applies only to switch-managed networks")
		}
		deviceID := strings.TrimSpace(in.DeviceID)
		if !looksLikeUUID(deviceID) {
			return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "switch-managed network requires a valid device ID")
		}
		body["deviceId"] = deviceID
	}
	if in.VLAN != nil {
		if err := validateOfficialNetworkVLAN(*in.VLAN); err != nil {
			return networkDocument{}, nil, err
		}
		body["vlanId"] = *in.VLAN
	}
	ipv4, _ := body["ipv4Configuration"].(map[string]any)
	if inputSetsNetworkSubnet(in) {
		addr, prefix, err := parseNetworkSubnet(in.Subnet)
		if err != nil {
			return networkDocument{}, nil, err
		}
		if ipv4 == nil {
			ipv4 = map[string]any{"autoScaleEnabled": false}
		} else {
			ipv4 = deepCloneMap(ipv4)
		}
		ipv4["hostIpAddress"] = addr
		ipv4["prefixLength"] = prefix
		body["ipv4Configuration"] = ipv4
	}
	if in.SetDHCPEnabled && in.SetDHCPMode {
		return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "use DHCP mode instead of DHCP enabled when both are specified")
	}
	if in.SetDHCPEnabled || in.SetDHCPMode || inputSetsNetworkDHCPDetails(in) || inputSetsNetworkSubnet(in) {
		updatedIPv4, err := updateOfficialNetworkDHCP(ipv4, currentManagement, in)
		if err != nil {
			return networkDocument{}, nil, err
		}
		body["ipv4Configuration"] = updatedIPv4
	}
	if wireDocumentsEqual(body, networkWritableDocument(doc.wire)) {
		return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "network update would not change controller state")
	}
	return doc, body, nil
}

func officialNetworkTransitionBody(current map[string]any, in NetworkInput, targetManagement string) (map[string]any, error) {
	vlan, ok := asInt(current["vlanId"])
	if !ok {
		return nil, apperr.New(apperr.Conflict, "official network transition cannot preserve an invalid VLAN ID")
	}
	transition := in
	transition.Name = strField(current, "name")
	if inputSetsNetworkName(in) {
		transition.Name = in.Name
	}
	transition.Purpose = strings.ToLower(targetManagement)
	transition.VLAN = &vlan
	transition.Enabled = boolField(current, "enabled")
	transition.SetEnabled = true
	if in.SetEnabled {
		transition.Enabled = in.Enabled
	}
	body, err := officialNetworkCreateBody(transition)
	if err != nil {
		if typed := apperr.As(err); typed != nil {
			return nil, apperr.WithHint(typed, "management transitions require every target-mode field, including subnet, DHCP mode, and switch device when applicable")
		}
		return nil, err
	}
	if guarding, exists := current["dhcpGuarding"]; exists {
		body["dhcpGuarding"] = deepCloneValue(guarding)
	}
	return body, nil
}

func updateOfficialNetworkDHCP(ipv4 map[string]any, management string, in NetworkInput) (map[string]any, error) {
	if ipv4 == nil {
		return nil, apperr.New(apperr.ValidationFailed, "network has no IPv4 configuration to update")
	}
	ipv4 = deepCloneMap(ipv4)
	host, err := netip.ParseAddr(strField(ipv4, "hostIpAddress"))
	prefixLength := intField(ipv4, "prefixLength")
	if err != nil || !host.Is4() || prefixLength < 8 || prefixLength > 30 {
		return nil, apperr.New(apperr.Conflict, "network has an invalid official IPv4 configuration")
	}
	prefix := netip.PrefixFrom(host, prefixLength)
	dhcp, _ := ipv4["dhcpConfiguration"].(map[string]any)
	if in.SetDHCPEnabled {
		if !in.DHCPEnabled {
			delete(ipv4, "dhcpConfiguration")
			return ipv4, nil
		}
		if dhcp == nil {
			return nil, apperr.New(apperr.ValidationFailed, "enabling DHCP requires an explicit mode and complete target-mode fields")
		}
	}
	if in.SetDHCPMode {
		mode := strings.ToLower(strings.TrimSpace(in.DHCPMode))
		if mode == "none" {
			if inputSetsNetworkDHCPDetails(in) {
				return nil, apperr.New(apperr.ValidationFailed, "DHCP mode none does not accept server or relay fields")
			}
			delete(ipv4, "dhcpConfiguration")
			return ipv4, nil
		}
		if dhcp == nil || strings.ToLower(strField(dhcp, "mode")) != mode {
			replacement, err := officialNetworkCreateDHCP(in, management, prefix)
			if err != nil {
				return nil, err
			}
			ipv4["dhcpConfiguration"] = replacement
			return ipv4, nil
		}
	}
	if dhcp == nil {
		if inputSetsNetworkDHCPDetails(in) {
			return nil, apperr.New(apperr.ValidationFailed, "network has no DHCP configuration to update")
		}
		return ipv4, nil
	}
	dhcp = deepCloneMap(dhcp)
	switch strings.ToUpper(strField(dhcp, "mode")) {
	case "SERVER":
		if in.SetDHCPRelayServerIPAddresses {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP server mode does not accept relay server addresses")
		}
		rangeBody, _ := dhcp["ipAddressRange"].(map[string]any)
		if rangeBody == nil {
			return nil, apperr.New(apperr.Conflict, "DHCP server configuration has no address range")
		}
		rangeBody = deepCloneMap(rangeBody)
		if in.SetDHCPRangeStart {
			rangeBody["start"] = in.DHCPRangeStart
		}
		if in.SetDHCPRangeStop {
			rangeBody["stop"] = in.DHCPRangeStop
		}
		start, stop, err := validateNetworkDHCPRange(prefix, strField(rangeBody, "start"), strField(rangeBody, "stop"))
		if err != nil {
			return nil, err
		}
		rangeBody["start"], rangeBody["stop"] = start, stop
		dhcp["ipAddressRange"] = rangeBody
		if in.SetDHCPLeaseTimeSeconds {
			if in.DHCPLeaseTimeSeconds < 0 || in.DHCPLeaseTimeSeconds > 31536000 {
				return nil, apperr.New(apperr.ValidationFailed, "DHCP lease duration must be between 0 and 31536000 seconds")
			}
			dhcp["leaseTimeSeconds"] = in.DHCPLeaseTimeSeconds
		}
		if in.SetDHCPConflictDetectionEnabled {
			if management != "GATEWAY" {
				return nil, apperr.New(apperr.ValidationFailed, "DHCP conflict detection applies only to gateway-managed networks")
			}
			dhcp["pingConflictDetectionEnabled"] = in.DHCPConflictDetectionEnabled
		}
		if in.SetDNSServerIPAddresses {
			servers, err := validateNetworkIPList("DNS server", in.DNSServerIPAddresses, true, 4, false)
			if err != nil {
				return nil, err
			}
			dhcp["dnsServerIpAddressesOverride"] = servers
		}
		if inputSetsNetworkDomain(in) {
			dhcp["domainName"] = in.DomainName
		}
		if in.ClearDomainName {
			delete(dhcp, "domainName")
		}
	case "RELAY":
		if inputSetsNetworkDHCPServerFields(in) {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP relay mode does not accept DHCP server fields")
		}
		if in.SetDHCPRelayServerIPAddresses {
			servers, err := validateNetworkIPList("DHCP relay server", in.DHCPRelayServerIPAddresses, true, 0, true)
			if err != nil {
				return nil, err
			}
			dhcp["dhcpServerIpAddresses"] = servers
		}
	default:
		return nil, apperr.New(apperr.Conflict, "network has an unsupported DHCP mode")
	}
	ipv4["dhcpConfiguration"] = dhcp
	return ipv4, nil
}

func (s *NetworkService) applyOfficialUpdate(ctx context.Context, query string, in NetworkInput, target *plan.Target) (Network, error) {
	doc, body, err := s.prepareOfficialUpdate(ctx, query, in)
	if err != nil {
		return Network{}, err
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	path, err := transport.IntegrationSitePath(ctx, "networks", doc.normalized.ID)
	if err != nil {
		return Network{}, err
	}
	if target != nil {
		p := plan.Update("network", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update network %s", doc.normalized.Name), officialNetworkSnapshot(doc.wire), officialNetworkSnapshot(networkResponseView(body, doc.wire)))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Network{}, err
		}
	}
	var response map[string]any
	if err := transport.DoOfficial(ctx, http.MethodPut, path, body, &response); err != nil {
		return Network{}, err
	}
	observed, err := fetchOfficialSiteDetail(s.api, ctx, doc.normalized.ID, "networks")
	if err != nil {
		return Network{}, verificationError("updated network could not be verified", err)
	}
	if err := requireObservedResourceID(observed, doc.normalized.ID, "network update"); err != nil {
		return Network{}, err
	}
	if !wireDocumentsEqual(networkWritableDocument(observed), body) {
		return Network{}, apperr.New(apperr.Conflict, "network update verification failed: observed writable document differs from requested state")
	}
	return NormalizeNetwork(observed), nil
}

func (s *NetworkService) applyOfficialDelete(ctx context.Context, query string, target *plan.Target) (Network, error) {
	doc, err := s.resolveOfficialDocument(ctx, query)
	if err != nil {
		return Network{}, err
	}
	if target != nil {
		p := plan.Delete("network", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("delete network %s", doc.normalized.Name), networkSnapshot(doc.normalized))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Network{}, err
		}
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	path, err := transport.IntegrationSitePath(ctx, "networks", doc.normalized.ID)
	if err != nil {
		return Network{}, err
	}
	if err := transport.DoOfficial(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return Network{}, err
	}
	if _, err := fetchOfficialSiteDetail(s.api, ctx, doc.normalized.ID, "networks"); err == nil {
		return Network{}, apperr.New(apperr.Conflict, "network delete verification failed: deleted network is still present")
	} else if !apperr.Is(err, apperr.NotFound) {
		return Network{}, verificationError("deleted network could not be verified", err)
	}
	return doc.normalized, nil
}

func officialNetworkCreateBody(in NetworkInput) (map[string]any, error) {
	management, err := officialNetworkManagement(in.Purpose)
	if err != nil {
		return nil, err
	}
	if in.VLAN == nil {
		return nil, apperr.New(apperr.ValidationFailed, "official network creation requires a VLAN ID")
	}
	if err := validateOfficialNetworkVLAN(*in.VLAN); err != nil {
		return nil, err
	}
	enabled := true
	if in.SetEnabled {
		enabled = in.Enabled
	}
	body := map[string]any{"name": in.Name, "enabled": enabled, "management": management, "vlanId": *in.VLAN}
	switch management {
	case "UNMANAGED":
		if inputSetsOfficialNetworkManagedField(in) {
			return nil, apperr.New(apperr.ValidationFailed, "unmanaged network creation does not accept gateway-only IPv4 or DHCP fields")
		}
		return body, nil
	case "SWITCH":
		if !in.SetDeviceID || !looksLikeUUID(strings.TrimSpace(in.DeviceID)) {
			return nil, apperr.New(apperr.ValidationFailed, "switch-managed network creation requires a valid device ID")
		}
		body["cellularBackupEnabled"] = false
		body["deviceId"] = strings.TrimSpace(in.DeviceID)
		body["isolationEnabled"] = false
	case "GATEWAY":
		if in.SetDeviceID {
			return nil, apperr.New(apperr.ValidationFailed, "a network device ID applies only to switch-managed networks")
		}
		body["cellularBackupEnabled"] = false
		body["internetAccessEnabled"] = true
		body["isolationEnabled"] = false
	default:
		return nil, apperr.New(apperr.ValidationFailed, "unsupported official network management mode")
	}
	if !inputSetsNetworkSubnet(in) {
		return nil, apperr.Newf(apperr.ValidationFailed, "%s-managed network creation requires a subnet", strings.ToLower(management))
	}
	addr, prefixLength, err := parseNetworkSubnet(in.Subnet)
	if err != nil {
		return nil, err
	}
	prefix, _ := netip.ParsePrefix(in.Subnet)
	ipv4 := map[string]any{"hostIpAddress": addr, "prefixLength": prefixLength, "autoScaleEnabled": false}
	dhcp, err := officialNetworkCreateDHCP(in, management, prefix)
	if err != nil {
		return nil, err
	}
	if dhcp != nil {
		ipv4["dhcpConfiguration"] = dhcp
	}
	body["ipv4Configuration"] = ipv4
	return body, nil
}

func officialNetworkCreateDHCP(in NetworkInput, management string, prefix netip.Prefix) (map[string]any, error) {
	mode := strings.ToLower(strings.TrimSpace(in.DHCPMode))
	if !in.SetDHCPMode {
		if in.DHCPEnabled || inputSetsNetworkDHCPDetails(in) {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP fields require --dhcp-mode; a DHCP range is never inferred")
		}
		if in.SetDHCPEnabled && !in.DHCPEnabled {
			return nil, nil
		}
		return nil, nil
	}
	switch mode {
	case "none":
		if inputSetsNetworkDHCPDetails(in) {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP mode none does not accept server or relay fields")
		}
		return nil, nil
	case "relay":
		if inputSetsNetworkDHCPServerFields(in) {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP relay mode does not accept DHCP server fields")
		}
		servers, err := validateNetworkIPList("DHCP relay server", in.DHCPRelayServerIPAddresses, in.SetDHCPRelayServerIPAddresses, 0, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"mode": "RELAY", "dhcpServerIpAddresses": servers}, nil
	case "server":
		if in.SetDHCPRelayServerIPAddresses {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP server mode does not accept relay server addresses")
		}
		if !in.SetDHCPRangeStart || !in.SetDHCPRangeStop || !in.SetDHCPLeaseTimeSeconds {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP server mode requires explicit range start, range end, and lease duration")
		}
		if management == "GATEWAY" && !in.SetDHCPConflictDetectionEnabled {
			return nil, apperr.New(apperr.ValidationFailed, "gateway DHCP server mode requires an explicit conflict-detection setting")
		}
		start, stop, err := validateNetworkDHCPRange(prefix, in.DHCPRangeStart, in.DHCPRangeStop)
		if err != nil {
			return nil, err
		}
		if in.DHCPLeaseTimeSeconds < 0 || in.DHCPLeaseTimeSeconds > 31536000 {
			return nil, apperr.New(apperr.ValidationFailed, "DHCP lease duration must be between 0 and 31536000 seconds")
		}
		dhcp := map[string]any{
			"mode": "SERVER", "ipAddressRange": map[string]any{"start": start, "stop": stop},
			"leaseTimeSeconds": in.DHCPLeaseTimeSeconds,
		}
		if management == "GATEWAY" {
			dhcp["pingConflictDetectionEnabled"] = in.DHCPConflictDetectionEnabled
		}
		if in.SetDNSServerIPAddresses {
			servers, err := validateNetworkIPList("DNS server", in.DNSServerIPAddresses, true, 4, false)
			if err != nil {
				return nil, err
			}
			dhcp["dnsServerIpAddressesOverride"] = servers
		}
		if inputSetsNetworkDomain(in) {
			dhcp["domainName"] = in.DomainName
		}
		return dhcp, nil
	default:
		return nil, apperr.Newf(apperr.ValidationFailed, "DHCP mode %q is unsupported; use none, server, or relay", in.DHCPMode)
	}
}

func validateNetworkDHCPRange(prefix netip.Prefix, startValue, stopValue string) (string, string, error) {
	start, startErr := netip.ParseAddr(strings.TrimSpace(startValue))
	stop, stopErr := netip.ParseAddr(strings.TrimSpace(stopValue))
	if startErr != nil || stopErr != nil || !start.Is4() || !stop.Is4() {
		return "", "", apperr.New(apperr.ValidationFailed, "DHCP range start and end must be valid IPv4 addresses")
	}
	if !prefix.Contains(start) || !prefix.Contains(stop) {
		return "", "", apperr.New(apperr.ValidationFailed, "DHCP range must be contained in the network subnet")
	}
	if start.Compare(stop) > 0 {
		return "", "", apperr.New(apperr.ValidationFailed, "DHCP range start must not be after its end")
	}
	return start.String(), stop.String(), nil
}

func validateNetworkIPList(field string, values []string, set bool, max int, ipv4Only bool) ([]string, error) {
	if !set || len(values) == 0 {
		return nil, apperr.Newf(apperr.ValidationFailed, "%s requires at least one address", field)
	}
	if max > 0 && len(values) > max {
		return nil, apperr.Newf(apperr.ValidationFailed, "%s accepts at most %d addresses", field, max)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		address, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil || (ipv4Only && !address.Is4()) {
			return nil, apperr.Newf(apperr.ValidationFailed, "%s contains an invalid address: %s", field, value)
		}
		canonical := address.String()
		if _, duplicate := seen[canonical]; duplicate {
			return nil, apperr.Newf(apperr.ValidationFailed, "%s contains a duplicate address: %s", field, canonical)
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}
	return result, nil
}

func officialNetworkManagement(value string) (string, error) {
	switch strings.ToLower(value) {
	case "gateway":
		return "GATEWAY", nil
	case "switch":
		return "SWITCH", nil
	case "unmanaged":
		return "UNMANAGED", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "network management %q is unsupported by the official API; use gateway, switch, or unmanaged", value)
	}
}

func parseNetworkSubnet(value string) (string, int, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return "", 0, apperr.Newf(apperr.ValidationFailed, "network subnet must be a valid IPv4 CIDR: %q", value)
	}
	if prefix.Bits() < 8 || prefix.Bits() > 30 {
		return "", 0, apperr.Newf(apperr.ValidationFailed, "official gateway network prefix must be between 8 and 30: %q", value)
	}
	return prefix.Addr().String(), prefix.Bits(), nil
}

func validateOfficialNetworkVLAN(vlan int) error {
	if vlan < 1 || vlan > 4009 {
		return apperr.Newf(apperr.ValidationFailed, "official network VLAN must be between 1 and 4009: %d", vlan)
	}
	return nil
}

func mergeOfficialNetworkDetail(overview, detail Network) Network {
	if detail.ID == "" {
		detail.ID = overview.ID
	}
	if detail.Name == "" {
		detail.Name = overview.Name
	}
	if detail.VLAN == nil {
		detail.VLAN = overview.VLAN
	}
	return detail
}

func networkInputBody(in NetworkInput) map[string]any {
	body := map[string]any{}
	if inputSetsNetworkName(in) {
		body["name"] = in.Name
	}
	if inputSetsNetworkPurpose(in) {
		body["purpose"] = in.Purpose
	}
	if inputSetsNetworkSubnet(in) {
		body["ip_subnet"] = in.Subnet
	}
	if inputSetsNetworkDomain(in) {
		body["domain_name"] = in.DomainName
	}
	if in.ClearDomainName {
		body["domain_name"] = ""
	}
	if in.SetDHCPEnabled || in.DHCPEnabled {
		body["dhcpd_enabled"] = in.DHCPEnabled
	}
	if in.VLAN != nil {
		body["vlan"] = *in.VLAN
		body["vlan_enabled"] = true
	}
	return body
}

func networkSnapshot(n Network) map[string]any {
	m := map[string]any{
		"id":           n.ID,
		"name":         n.Name,
		"purpose":      n.Purpose,
		"subnet":       n.Subnet,
		"dhcp_enabled": n.DHCPEnabled,
		"domain_name":  n.DomainName,
		"wan":          n.WAN,
	}
	if n.VLAN != nil {
		m["vlan"] = *n.VLAN
	}
	return m
}

func officialNetworkSnapshot(raw map[string]any) map[string]any {
	snapshot := networkSnapshot(NormalizeNetwork(raw))
	snapshot["enabled"] = boolField(raw, "enabled")
	if deviceID := strField(raw, "deviceId"); deviceID != "" {
		snapshot["device_id"] = deviceID
	}
	ipv4, _ := raw["ipv4Configuration"].(map[string]any)
	dhcp, _ := ipv4["dhcpConfiguration"].(map[string]any)
	if dhcp == nil {
		snapshot["dhcp_mode"] = "none"
		return snapshot
	}
	snapshot["dhcp_mode"] = strings.ToLower(strField(dhcp, "mode"))
	if addressRange, ok := dhcp["ipAddressRange"].(map[string]any); ok {
		snapshot["dhcp_range_start"] = strField(addressRange, "start")
		snapshot["dhcp_range_end"] = strField(addressRange, "stop")
	}
	if _, exists := dhcp["leaseTimeSeconds"]; exists {
		snapshot["dhcp_lease_seconds"] = intField(dhcp, "leaseTimeSeconds")
	}
	if _, exists := dhcp["pingConflictDetectionEnabled"]; exists {
		snapshot["dhcp_conflict_detection"] = boolField(dhcp, "pingConflictDetectionEnabled")
	}
	if _, exists := dhcp["dhcpServerIpAddresses"]; exists {
		snapshot["dhcp_relay_server_ip_addresses"] = firewallStringSlice(dhcp["dhcpServerIpAddresses"])
	}
	if _, exists := dhcp["dnsServerIpAddressesOverride"]; exists {
		snapshot["dns_server_ip_addresses"] = firewallStringSlice(dhcp["dnsServerIpAddressesOverride"])
	}
	return snapshot
}

func mergeNetworkAfter(n Network, in NetworkInput) map[string]any {
	after := networkSnapshot(n)
	if inputSetsNetworkName(in) {
		after["name"] = in.Name
	}
	if inputSetsNetworkPurpose(in) {
		after["purpose"] = in.Purpose
		after["wan"] = in.Purpose == "wan"
	}
	if inputSetsNetworkSubnet(in) {
		after["subnet"] = in.Subnet
	}
	if inputSetsNetworkDomain(in) {
		after["domain_name"] = in.DomainName
	}
	if in.ClearDomainName {
		after["domain_name"] = ""
	}
	if in.VLAN != nil {
		after["vlan"] = *in.VLAN
	}
	if in.SetDHCPEnabled || in.DHCPEnabled {
		after["dhcp_enabled"] = in.DHCPEnabled
	}
	return after
}

func validateNetworkCreate(in NetworkInput) error {
	if err := validateRequired("network name", in.Name); err != nil {
		return err
	}
	if err := validateRequired("network purpose", in.Purpose); err != nil {
		return err
	}
	return validateNetworkFields(in)
}

func validateNetworkUpdate(in NetworkInput) error {
	if !inputSetsNetworkName(in) && !inputSetsNetworkPurpose(in) && !in.SetEnabled && in.VLAN == nil && !in.SetDeviceID &&
		!inputSetsNetworkSubnet(in) && !in.SetDHCPEnabled && !in.SetDHCPMode && !inputSetsNetworkDHCPDetails(in) &&
		!inputSetsNetworkDomain(in) && !in.ClearDomainName {
		return apperr.New(apperr.ValidationFailed, "network update requires at least one changed field")
	}
	if in.ClearDomainName && inputSetsNetworkDomain(in) {
		return apperr.New(apperr.ValidationFailed, "use either domain name or clear domain name, not both")
	}
	if inputSetsNetworkName(in) {
		if err := validateRequired("network name", in.Name); err != nil {
			return err
		}
	}
	return validateNetworkFields(in)
}

func validateNetworkFields(in NetworkInput) error {
	if err := validateVLAN(in.VLAN); err != nil {
		return err
	}
	if err := validateCIDR("network subnet", in.Subnet); err != nil {
		return err
	}
	if err := validateEnum("network purpose", in.Purpose, "corporate", "guest", "wan", "gateway", "switch", "unmanaged"); err != nil {
		return err
	}
	if inputSetsNetworkDomain(in) {
		return validateDNSName(in.DomainName)
	}
	return nil
}

func inputSetsNetworkName(in NetworkInput) bool    { return in.SetName || in.Name != "" }
func inputSetsNetworkPurpose(in NetworkInput) bool { return in.SetPurpose || in.Purpose != "" }
func inputSetsNetworkSubnet(in NetworkInput) bool  { return in.SetSubnet || in.Subnet != "" }
func inputSetsNetworkDomain(in NetworkInput) bool  { return in.SetDomainName || in.DomainName != "" }

func inputSetsNetworkDHCPDetails(in NetworkInput) bool {
	return in.SetDHCPRangeStart || in.SetDHCPRangeStop || in.SetDHCPLeaseTimeSeconds ||
		in.SetDHCPConflictDetectionEnabled || in.SetDHCPRelayServerIPAddresses || in.SetDNSServerIPAddresses ||
		inputSetsNetworkDomain(in) || in.ClearDomainName
}

func inputSetsNetworkDHCPServerFields(in NetworkInput) bool {
	return in.SetDHCPRangeStart || in.SetDHCPRangeStop || in.SetDHCPLeaseTimeSeconds ||
		in.SetDHCPConflictDetectionEnabled || in.SetDNSServerIPAddresses || inputSetsNetworkDomain(in) || in.ClearDomainName
}

func inputSetsOfficialNetworkManagedField(in NetworkInput) bool {
	return inputSetsNetworkSubnet(in) || in.SetDeviceID || in.SetDHCPEnabled || in.SetDHCPMode || inputSetsNetworkDHCPDetails(in)
}
