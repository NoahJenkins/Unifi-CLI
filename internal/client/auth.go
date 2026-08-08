package client

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

func (c *Client) authSnapshot(context.Context) (string, string, error) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.apiKey != "" {
		return c.apiKey, c.authMethod, nil
	}
	return "", "", apperr.WithHint(
		apperr.New(apperr.NotAuthenticated, "not authenticated"),
		"run 'unifi login' to save an API key",
	)
}

func (c *Client) Validate(ctx context.Context) error {
	return c.Do(ctx, http.MethodGet, PathSelfSites, nil, nil)
}
