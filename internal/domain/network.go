package domain

import (
	"context"
	"fmt"
	"net/http"
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

func NewNetworkService(api NetworkAPI) *NetworkService {
	return &NetworkService{api: api}
}

func (s *NetworkService) List(ctx context.Context) ([]Network, error) {
	raw, official, err := fetchNetworkObjects(ctx, s.api)
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
	return resolve.One(items, id)
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
	return resolve.One(items, id)
}

func (s *NetworkService) Create(ctx context.Context, in NetworkInput) (plan.Plan, error) {
	_ = ctx
	if err := validateNetworkCreate(in); err != nil {
		return plan.Plan{}, err
	}
	after := networkInputBody(in)
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
	if err := validateNetworkUpdate(in); err != nil {
		return Network{}, err
	}
	n, err := s.getLegacy(ctx, id)
	if err != nil {
		return Network{}, err
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
	n, err := s.getLegacy(ctx, id)
	if err != nil {
		return Network{}, err
	}
	path := s.api.SitePath(client.PathRestNetwork, n.ID)
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return Network{}, err
	}
	return n, nil
}

// NetworkDeleteDestructive reports whether deleting n requires safe_mode --force.
func NetworkDeleteDestructive(n Network) bool {
	return n.WAN
}

func NormalizeNetwork(m map[string]any) Network {
	n := Network{
		ID:          strField(m, "_id", "id"),
		Name:        strField(m, "name"),
		Purpose:     strField(m, "purpose"),
		Subnet:      strField(m, "ip_subnet", "subnet"),
		DHCPEnabled: boolField(m, "dhcpd_enabled"),
		DomainName:  strField(m, "domain_name"),
	}
	if n.Purpose == "" {
		n.Purpose = strings.ToLower(strField(m, "management"))
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

func fetchNetworkObjects(ctx context.Context, api any) ([]map[string]any, bool, error) {
	raw, official, err := fetchOfficialSite(api, ctx, "networks")
	if err != nil || !official || !supportsOfficialDetails(api) {
		return raw, official, err
	}
	for i, overview := range raw {
		id := strField(overview, "id")
		if id == "" {
			continue
		}
		detail, err := fetchOfficialSiteDetail(api, ctx, id, "networks")
		if err != nil {
			return nil, true, err
		}
		raw[i] = detail
	}
	return raw, true, nil
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
	if err := validateEnum("network purpose", in.Purpose, "corporate", "guest", "wan"); err != nil {
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
