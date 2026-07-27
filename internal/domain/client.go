package domain

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/client"
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
	var raw []map[string]any
	path := s.api.SitePath(client.PathStatSta)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
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

func NormalizeClient(m map[string]any) Client {
	c := Client{
		ID:       strField(m, "_id", "id"),
		MAC:      strField(m, "mac"),
		Hostname: strField(m, "hostname"),
		Name:     strField(m, "name"),
		IP:       strField(m, "ip", "last_ip"),
		ESSID:    strField(m, "essid"),
		Network:  strField(m, "network", "network_name"),
		IsWired:  boolField(m, "is_wired"),
		Blocked:  boolField(m, "blocked"),
		LastSeen: strField(m, "last_seen"),
	}
	if c.Name == "" {
		c.Name = c.Hostname
	}
	return c
}
