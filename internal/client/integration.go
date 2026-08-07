package client

import (
	"context"
	"net/http"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

const integrationBasePath = "/proxy/network/integration/v1"

type integrationSite struct {
	ID                string `json:"id"`
	InternalReference string `json:"internalReference"`
}

// IntegrationSitePath resolves the configured legacy site reference to the
// UUID required by the official local Network API.
func (c *Client) IntegrationSitePath(ctx context.Context, parts ...string) (string, error) {
	var sites []integrationSite
	if err := c.Do(ctx, http.MethodGet, integrationBasePath+"/sites?offset=0&limit=100", nil, &sites); err != nil {
		return "", err
	}
	for _, site := range sites {
		if site.InternalReference != c.cfg.Site {
			continue
		}
		path := integrationBasePath + "/sites/" + site.ID
		for _, part := range parts {
			part = strings.Trim(part, "/")
			if part != "" {
				path += "/" + part
			}
		}
		return path, nil
	}
	return "", apperr.Newf(apperr.NotFound, "site %q is unavailable through the official Network API", c.cfg.Site)
}
