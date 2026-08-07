package domain

import (
	"context"
	"fmt"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type ClientAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Client struct {
	ID       string `json:"id"`
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	ESSID    string `json:"essid"`
	Network  string `json:"network"`
	IsWired  bool   `json:"is_wired"`
	Blocked  bool   `json:"blocked"`
	LastSeen string `json:"last_seen"`
}

func (c Client) GetID() string  { return c.ID }
func (c Client) GetMAC() string { return c.MAC }
func (c Client) GetName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Hostname
}

type ClientService struct {
	api ClientAPI
}

func NewClientService(api ClientAPI) *ClientService {
	return &ClientService{api: api}
}

func (s *ClientService) List(ctx context.Context) ([]Client, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "clients")
	if err != nil {
		return nil, err
	}
	if !official {
		path := s.api.SitePath(client.PathStatSta)
		if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
	}
	out := make([]Client, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeClient(m))
	}
	return out, nil
}

func (s *ClientService) Get(ctx context.Context, id string) (Client, error) {
	items, err := s.List(ctx)
	if err != nil {
		return Client{}, err
	}
	return resolve.One(items, id)
}

func (s *ClientService) listLegacy(ctx context.Context) ([]Client, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathStatSta), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Client, 0, len(raw))
	for _, item := range raw {
		out = append(out, NormalizeClient(item))
	}
	return out, nil
}

func (s *ClientService) getLegacy(ctx context.Context, id string) (Client, error) {
	items, err := s.listLegacy(ctx)
	if err != nil {
		return Client{}, err
	}
	if item, ok := findExactID(items, id); ok {
		return item, nil
	}
	if !looksLikeUUID(id) {
		return resolve.One(items, id)
	}
	raw, official, err := fetchOfficialSite(s.api, ctx, "clients")
	if err != nil {
		return Client{}, err
	}
	if !official {
		return resolve.One(items, id)
	}
	officialItems := make([]Client, 0, len(raw))
	for _, item := range raw {
		officialItems = append(officialItems, NormalizeClient(item))
	}
	return resolveLegacyMutationTarget(items, officialItems, id, "client", func(a, b Client) bool { return sameMAC(a, b) })
}

func (s *ClientService) Reconnect(ctx context.Context, id string) (plan.Plan, Client, error) {
	return s.cmdPlan(ctx, id, "kick-sta", "reconnect")
}

func (s *ClientService) ApplyReconnect(ctx context.Context, id string) (Client, error) {
	return s.applyStaMgr(ctx, id, "kick-sta", nil)
}

func (s *ClientService) Block(ctx context.Context, id string) (plan.Plan, Client, error) {
	c, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Client{}, err
	}
	p := plan.Update("client", c.ID, c.GetName(),
		fmt.Sprintf("block client %s", c.GetName()),
		map[string]any{"blocked": c.Blocked},
		map[string]any{"blocked": true},
	)
	return p, c, nil
}

func (s *ClientService) ApplyBlock(ctx context.Context, id string) (Client, error) {
	return s.applyStaMgr(ctx, id, "block-sta", func(c *Client) { c.Blocked = true })
}

func (s *ClientService) Unblock(ctx context.Context, id string) (plan.Plan, Client, error) {
	c, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Client{}, err
	}
	p := plan.Update("client", c.ID, c.GetName(),
		fmt.Sprintf("unblock client %s", c.GetName()),
		map[string]any{"blocked": c.Blocked},
		map[string]any{"blocked": false},
	)
	return p, c, nil
}

func (s *ClientService) ApplyUnblock(ctx context.Context, id string) (Client, error) {
	return s.applyStaMgr(ctx, id, "unblock-sta", func(c *Client) { c.Blocked = false })
}

func (s *ClientService) cmdPlan(ctx context.Context, id, cmd, action string) (plan.Plan, Client, error) {
	c, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Client{}, err
	}
	p := plan.Update("client", c.ID, c.GetName(),
		fmt.Sprintf("%s client %s", action, c.GetName()),
		map[string]any{"action": "none"},
		map[string]any{"action": cmd, "mac": c.MAC},
	)
	return p, c, nil
}

func (s *ClientService) applyStaMgr(ctx context.Context, id, cmd string, mutate func(*Client)) (Client, error) {
	c, err := s.getLegacy(ctx, id)
	if err != nil {
		return Client{}, err
	}
	path := s.api.SitePath(client.PathCmdStaMgr)
	body := map[string]any{"cmd": cmd, "mac": c.MAC}
	if err := s.api.Do(ctx, http.MethodPost, path, body, nil); err != nil {
		return Client{}, err
	}
	if mutate != nil {
		mutate(&c)
	}
	return c, nil
}

func NormalizeClient(m map[string]any) Client {
	c := Client{
		ID:       strField(m, "_id", "id"),
		MAC:      strField(m, "mac", "macAddress"),
		Hostname: strField(m, "hostname"),
		Name:     strField(m, "name"),
		IP:       strField(m, "ip", "last_ip", "ipAddress"),
		ESSID:    strField(m, "essid"),
		Network:  strField(m, "network", "network_name"),
		IsWired:  boolField(m, "is_wired"),
		Blocked:  boolField(m, "blocked"),
		LastSeen: strField(m, "last_seen"),
	}
	if strField(m, "type") == "WIRED" {
		c.IsWired = true
	}
	if c.Name == "" {
		c.Name = c.Hostname
	}
	return c
}
