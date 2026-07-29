package client

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

func (c *Client) ensureAuth(context.Context) error {
	if c.apiKey != "" {
		return nil
	}
	return apperr.WithHint(
		apperr.New(apperr.NotAuthenticated, "not authenticated"),
		"run 'unifi login' to save an API key",
	)
}

func (c *Client) Validate(ctx context.Context) error {
	return c.Do(ctx, http.MethodGet, PathSelfSites, nil, nil)
}
