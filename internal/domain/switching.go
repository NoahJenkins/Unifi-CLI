package domain

import (
	"context"
	"sort"

	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type SwitchingAPI interface {
	FetchOfficialObjects(context.Context, string) ([]map[string]any, error)
	IntegrationSitePath(context.Context, ...string) (string, error)
	DoOfficial(context.Context, string, string, any, any) error
}

type SwitchingMember struct {
	DeviceID string `json:"device_id"`
	PortIdxs []int  `json:"port_idxs,omitempty"`
}

type LAG struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	SwitchStackID string            `json:"switch_stack_id,omitempty"`
	MCLAGDomainID string            `json:"mc_lag_domain_id,omitempty"`
	Members       []SwitchingMember `json:"members"`
	Origin        string            `json:"origin"`
}

func (r LAG) GetID() string   { return r.ID }
func (r LAG) GetMAC() string  { return "" }
func (r LAG) GetName() string { return "" }

type MCLAGPeer struct {
	DeviceID     string `json:"device_id"`
	LinkPortIdxs []int  `json:"link_port_idxs"`
	Role         string `json:"role"`
}

type MCLAGDomain struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Peers  []MCLAGPeer `json:"peers"`
	LAGIDs []string    `json:"lag_ids"`
	Origin string      `json:"origin"`
}

func (r MCLAGDomain) GetID() string   { return r.ID }
func (r MCLAGDomain) GetMAC() string  { return "" }
func (r MCLAGDomain) GetName() string { return r.Name }

type SwitchStack struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	DeviceIDs []string `json:"device_ids"`
	LAGIDs    []string `json:"lag_ids"`
	Origin    string   `json:"origin"`
}

func (r SwitchStack) GetID() string   { return r.ID }
func (r SwitchStack) GetMAC() string  { return "" }
func (r SwitchStack) GetName() string { return r.Name }

type RadiusProfile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Origin string `json:"origin"`
}

func (r RadiusProfile) GetID() string   { return r.ID }
func (r RadiusProfile) GetMAC() string  { return "" }
func (r RadiusProfile) GetName() string { return r.Name }

type SwitchingService struct{ api SwitchingAPI }

func NewSwitchingService(api SwitchingAPI) *SwitchingService { return &SwitchingService{api: api} }

func (s *SwitchingService) ListLAGs(ctx context.Context) ([]LAG, error) {
	raw, err := s.list(ctx, "switching", "lags")
	if err != nil {
		return nil, err
	}
	items := make([]LAG, 0, len(raw))
	for _, value := range raw {
		items = append(items, normalizeLAG(value))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Type != items[j].Type {
			return items[i].Type < items[j].Type
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *SwitchingService) GetLAG(ctx context.Context, query string) (LAG, error) {
	items, err := s.ListLAGs(ctx)
	if err != nil {
		return LAG{}, err
	}
	item, err := resolve.One(items, query)
	if err != nil {
		return LAG{}, err
	}
	raw, err := fetchOfficialSiteDetail(s.api, ctx, item.ID, "switching", "lags")
	if err != nil {
		return LAG{}, err
	}
	return normalizeLAG(raw), nil
}

func (s *SwitchingService) ListMCLAGDomains(ctx context.Context) ([]MCLAGDomain, error) {
	raw, err := s.list(ctx, "switching", "mc-lag-domains")
	if err != nil {
		return nil, err
	}
	items := make([]MCLAGDomain, 0, len(raw))
	for _, value := range raw {
		items = append(items, normalizeMCLAGDomain(value))
	}
	sortNamedMCLAG(items)
	return items, nil
}

func (s *SwitchingService) GetMCLAGDomain(ctx context.Context, query string) (MCLAGDomain, error) {
	items, err := s.ListMCLAGDomains(ctx)
	if err != nil {
		return MCLAGDomain{}, err
	}
	item, err := resolve.One(items, query)
	if err != nil {
		return MCLAGDomain{}, err
	}
	raw, err := fetchOfficialSiteDetail(s.api, ctx, item.ID, "switching", "mc-lag-domains")
	if err != nil {
		return MCLAGDomain{}, err
	}
	return normalizeMCLAGDomain(raw), nil
}

func (s *SwitchingService) ListSwitchStacks(ctx context.Context) ([]SwitchStack, error) {
	raw, err := s.list(ctx, "switching", "switch-stacks")
	if err != nil {
		return nil, err
	}
	items := make([]SwitchStack, 0, len(raw))
	for _, value := range raw {
		items = append(items, normalizeSwitchStack(value))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *SwitchingService) GetSwitchStack(ctx context.Context, query string) (SwitchStack, error) {
	items, err := s.ListSwitchStacks(ctx)
	if err != nil {
		return SwitchStack{}, err
	}
	item, err := resolve.One(items, query)
	if err != nil {
		return SwitchStack{}, err
	}
	raw, err := fetchOfficialSiteDetail(s.api, ctx, item.ID, "switching", "switch-stacks")
	if err != nil {
		return SwitchStack{}, err
	}
	return normalizeSwitchStack(raw), nil
}

func (s *SwitchingService) list(ctx context.Context, parts ...string) ([]map[string]any, error) {
	path, err := s.api.IntegrationSitePath(ctx, parts...)
	if err != nil {
		return nil, err
	}
	return s.api.FetchOfficialObjects(ctx, path)
}

type RadiusService struct{ api SwitchingAPI }

func NewRadiusService(api SwitchingAPI) *RadiusService { return &RadiusService{api: api} }

func (s *RadiusService) ListProfiles(ctx context.Context) ([]RadiusProfile, error) {
	path, err := s.api.IntegrationSitePath(ctx, "radius", "profiles")
	if err != nil {
		return nil, err
	}
	raw, err := s.api.FetchOfficialObjects(ctx, path)
	if err != nil {
		return nil, err
	}
	items := make([]RadiusProfile, 0, len(raw))
	for _, value := range raw {
		metadata, _ := value["metadata"].(map[string]any)
		items = append(items, RadiusProfile{ID: strField(value, "id"), Name: strField(value, "name"), Origin: strField(metadata, "origin")})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *RadiusService) GetProfile(ctx context.Context, query string) (RadiusProfile, error) {
	items, err := s.ListProfiles(ctx)
	if err != nil {
		return RadiusProfile{}, err
	}
	return resolve.One(items, query)
}

func normalizeLAG(value map[string]any) LAG {
	metadata, _ := value["metadata"].(map[string]any)
	return LAG{
		ID: valueString(value, "id"), Type: valueString(value, "type"),
		SwitchStackID: valueString(value, "switchStackId"), MCLAGDomainID: valueString(value, "mcLagDomainId"),
		Members: switchingMembers(value["members"]), Origin: valueString(metadata, "origin"),
	}
}

func normalizeMCLAGDomain(value map[string]any) MCLAGDomain {
	metadata, _ := value["metadata"].(map[string]any)
	return MCLAGDomain{ID: valueString(value, "id"), Name: valueString(value, "name"), Peers: mcLAGPeers(value["peers"]), LAGIDs: nestedIDs(value["lags"]), Origin: valueString(metadata, "origin")}
}

func normalizeSwitchStack(value map[string]any) SwitchStack {
	metadata, _ := value["metadata"].(map[string]any)
	return SwitchStack{ID: valueString(value, "id"), Name: valueString(value, "name"), DeviceIDs: nestedStringFields(value["members"], "deviceId"), LAGIDs: nestedIDs(value["lags"]), Origin: valueString(metadata, "origin")}
}

func switchingMembers(value any) []SwitchingMember {
	raw, _ := value.([]any)
	items := make([]SwitchingMember, 0, len(raw))
	for _, entry := range raw {
		member, _ := entry.(map[string]any)
		items = append(items, SwitchingMember{DeviceID: valueString(member, "deviceId"), PortIdxs: integerSlice(member["portIdxs"])})
	}
	return items
}

func mcLAGPeers(value any) []MCLAGPeer {
	raw, _ := value.([]any)
	items := make([]MCLAGPeer, 0, len(raw))
	for _, entry := range raw {
		peer, _ := entry.(map[string]any)
		items = append(items, MCLAGPeer{DeviceID: valueString(peer, "deviceId"), LinkPortIdxs: integerSlice(peer["linkPortIdxs"]), Role: valueString(peer, "role")})
	}
	return items
}

func nestedIDs(value any) []string { return nestedStringFields(value, "id") }

func nestedStringFields(value any, field string) []string {
	raw, _ := value.([]any)
	items := make([]string, 0, len(raw))
	for _, entry := range raw {
		item, _ := entry.(map[string]any)
		if result := valueString(item, field); result != "" {
			items = append(items, result)
		}
	}
	return items
}

func integerSlice(value any) []int {
	raw, _ := value.([]any)
	items := make([]int, 0, len(raw))
	for _, entry := range raw {
		switch number := entry.(type) {
		case float64:
			items = append(items, int(number))
		case int:
			items = append(items, number)
		}
	}
	return items
}

func valueString(value map[string]any, key string) string { return strField(value, key) }

func sortNamedMCLAG(items []MCLAGDomain) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
}
