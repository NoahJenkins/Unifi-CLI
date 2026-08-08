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
	Name            string
	SetName         bool
	Purpose         string
	SetPurpose      bool
	VLAN            *int
	Subnet          string
	SetSubnet       bool
	DHCPEnabled     bool
	SetDHCPEnabled  bool
	DomainName      string
	SetDomainName   bool
	ClearDomainName bool
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
		after := NormalizeNetwork(networkResponseView(body, doc.wire))
		p := plan.Update("network", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update network %s", doc.normalized.Name), networkSnapshot(doc.normalized), networkSnapshot(after))
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
	if inputSetsNetworkName(in) {
		body["name"] = in.Name
	}
	if inputSetsNetworkPurpose(in) {
		management, err := officialNetworkManagement(in.Purpose)
		if err != nil {
			return networkDocument{}, nil, err
		}
		currentManagement := strings.ToUpper(strField(doc.wire, "management"))
		if management != currentManagement {
			return networkDocument{}, nil, apperr.Newf(apperr.ValidationFailed,
				"network management cannot change from %s to %s without a complete target-mode document", currentManagement, management)
		}
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
	if in.SetDHCPEnabled || inputSetsNetworkDomain(in) || in.ClearDomainName {
		if ipv4 == nil {
			return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "network has no IPv4 configuration to update")
		}
		dhcp, _ := ipv4["dhcpConfiguration"].(map[string]any)
		if in.SetDHCPEnabled && in.DHCPEnabled && dhcp == nil {
			return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "enabling DHCP requires an existing official DHCP configuration")
		}
		if in.SetDHCPEnabled && !in.DHCPEnabled {
			delete(ipv4, "dhcpConfiguration")
		} else if dhcp != nil {
			dhcp = deepCloneMap(dhcp)
			if inputSetsNetworkDomain(in) {
				dhcp["domainName"] = in.DomainName
			}
			if in.ClearDomainName {
				delete(dhcp, "domainName")
			}
			ipv4["dhcpConfiguration"] = dhcp
		} else if inputSetsNetworkDomain(in) || in.ClearDomainName {
			return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "network has no DHCP configuration to update")
		}
		body["ipv4Configuration"] = ipv4
	}
	if wireDocumentsEqual(body, networkWritableDocument(doc.wire)) {
		return networkDocument{}, nil, apperr.New(apperr.ValidationFailed, "network update would not change controller state")
	}
	return doc, body, nil
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
		after := NormalizeNetwork(networkResponseView(body, doc.wire))
		p := plan.Update("network", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update network %s", doc.normalized.Name), networkSnapshot(doc.normalized), networkSnapshot(after))
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
	body := map[string]any{"name": in.Name, "enabled": true, "management": management, "vlanId": *in.VLAN}
	switch management {
	case "UNMANAGED":
		if inputSetsNetworkSubnet(in) || in.SetDHCPEnabled || inputSetsNetworkDomain(in) || in.ClearDomainName {
			return nil, apperr.New(apperr.ValidationFailed, "unmanaged network creation does not accept gateway-only IPv4 or DHCP fields")
		}
		return body, nil
	case "SWITCH":
		return nil, apperr.New(apperr.ValidationFailed, "switch-managed network creation requires a device ID not exposed by this command")
	case "GATEWAY":
		if !inputSetsNetworkSubnet(in) {
			return nil, apperr.New(apperr.ValidationFailed, "gateway-managed network creation requires a subnet")
		}
		addr, prefix, err := parseNetworkSubnet(in.Subnet)
		if err != nil {
			return nil, err
		}
		if in.DHCPEnabled || inputSetsNetworkDomain(in) {
			return nil, apperr.New(apperr.ValidationFailed, "gateway network creation cannot safely infer the required DHCP address range")
		}
		body["cellularBackupEnabled"] = false
		body["internetAccessEnabled"] = true
		body["isolationEnabled"] = false
		body["ipv4Configuration"] = map[string]any{"hostIpAddress": addr, "prefixLength": prefix, "autoScaleEnabled": false}
		return body, nil
	default:
		return nil, apperr.New(apperr.ValidationFailed, "unsupported official network management mode")
	}
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
	if !inputSetsNetworkName(in) && !inputSetsNetworkPurpose(in) && in.VLAN == nil &&
		!inputSetsNetworkSubnet(in) && !in.SetDHCPEnabled && !inputSetsNetworkDomain(in) && !in.ClearDomainName {
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
