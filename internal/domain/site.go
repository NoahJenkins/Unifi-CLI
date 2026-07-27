package domain

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type SiteAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	Role string `json:"role"`
}

func (s Site) GetID() string   { return s.ID }
func (s Site) GetMAC() string  { return "" }
func (s Site) GetName() string { return s.Name }

type SiteService struct {
	api SiteAPI
}

func NewSiteService(api SiteAPI) *SiteService {
	return &SiteService{api: api}
}

func (s *SiteService) List(ctx context.Context) ([]Site, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, client.PathSelfSites, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Site, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeSite(m))
	}
	return out, nil
}

func (s *SiteService) Get(ctx context.Context, id string) (Site, error) {
	items, err := s.List(ctx)
	if err != nil {
		return Site{}, err
	}
	return resolve.One(items, id)
}

func NormalizeSite(m map[string]any) Site {
	return Site{
		ID:   strField(m, "_id", "id"),
		Name: strField(m, "name"),
		Desc: strField(m, "desc", "description"),
		Role: strField(m, "role"),
	}
}
