package client

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

func (c *Client) ensureAuth(ctx context.Context) error {
	if c.cfg.APIKey != "" {
		return nil
	}
	if c.loggedIn {
		return nil
	}
	return c.Login(ctx)
}

func (c *Client) Login(ctx context.Context) error {
	if c.cfg.APIKey != "" {
		return nil
	}
	if c.cfg.Username == "" || c.cfg.Password == "" {
		return apperr.New(apperr.ValidationFailed, "username/password or api_key required")
	}
	body := map[string]string{
		"username": c.cfg.Username,
		"password": c.cfg.Password,
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/auth/login", body, nil, false); err != nil {
		return err
	}
	c.loggedIn = true
	return nil
}
