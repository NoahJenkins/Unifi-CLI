package domain

import (
	"context"
	"fmt"
	"net/http"

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
	Name        string
	Purpose     string
	VLAN        *int
	Subnet      string
	DHCPEnabled bool
	DomainName  string
}

type NetworkService struct {
	api NetworkAPI
}

func NewNetworkService(api NetworkAPI) *NetworkService {
	return &NetworkService{api: api}
}

func (s *NetworkService) List(ctx context.Context) ([]Network, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathRestNetwork)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
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

func (s *NetworkService) Create(ctx context.Context, in NetworkInput) (plan.Plan, error) {
	_ = ctx
	after := networkInputBody(in)
	p := plan.Create("network", in.Name,
		fmt.Sprintf("create network %s", in.Name),
		after,
	)
	return p, nil
}

func (s *NetworkService) ApplyCreate(ctx context.Context, in NetworkInput) (Network, error) {
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
	n, err := s.Get(ctx, id)
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
	n, err := s.Get(ctx, id)
	if err != nil {
		return Network{}, err
	}
	path := s.api.SitePath(client.PathRestNetwork, n.ID)
	body := networkInputBody(in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Network{}, err
	}
	if in.Name != "" {
		n.Name = in.Name
	}
	if in.Purpose != "" {
		n.Purpose = in.Purpose
		n.WAN = in.Purpose == "wan"
	}
	if in.Subnet != "" {
		n.Subnet = in.Subnet
	}
	if in.DomainName != "" {
		n.DomainName = in.DomainName
	}
	if in.VLAN != nil {
		n.VLAN = in.VLAN
	}
	if in.DHCPEnabled {
		n.DHCPEnabled = true
	}
	return n, nil
}

func (s *NetworkService) Delete(ctx context.Context, id string) (plan.Plan, Network, error) {
	n, err := s.Get(ctx, id)
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
	n, err := s.Get(ctx, id)
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
	return n
}

func networkInputBody(in NetworkInput) map[string]any {
	body := map[string]any{}
	if in.Name != "" {
		body["name"] = in.Name
	}
	if in.Purpose != "" {
		body["purpose"] = in.Purpose
	}
	if in.Subnet != "" {
		body["ip_subnet"] = in.Subnet
	}
	if in.DomainName != "" {
		body["domain_name"] = in.DomainName
	}
	if in.DHCPEnabled {
		body["dhcpd_enabled"] = true
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
	if in.Name != "" {
		after["name"] = in.Name
	}
	if in.Purpose != "" {
		after["purpose"] = in.Purpose
		after["wan"] = in.Purpose == "wan"
	}
	if in.Subnet != "" {
		after["subnet"] = in.Subnet
	}
	if in.DomainName != "" {
		after["domain_name"] = in.DomainName
	}
	if in.VLAN != nil {
		after["vlan"] = *in.VLAN
	}
	if in.DHCPEnabled {
		after["dhcp_enabled"] = true
	}
	return after
}
