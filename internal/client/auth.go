package client

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/session"
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
	c.sessionCookies = session.CookiesFromHTTP(c.responseCookies)
	if err := c.saveSession(); err != nil {
		return err
	}
	c.loggedIn = true
	c.authMethod = "password"
	return nil
}

func (c *Client) saveSession() error {
	if c.sessionStore == nil {
		return nil
	}
	record := session.Session{
		Controller: c.baseURL,
		Cookies:    append([]session.RequestCookie(nil), c.sessionCookies...),
		CSRF:       c.csrf,
		UpdatedAt:  time.Now().UTC(),
	}
	if err := c.sessionStore.Save(record, c.allowFileFallback); err != nil {
		if errors.Is(err, session.ErrKeyringUnavailable) {
			return apperr.WithHint(
				apperr.New(apperr.AuthFailed, "cannot save authenticated session"),
				"run 'unifi auth login --file-fallback' to save a protected local session",
			)
		}
		return apperr.New(apperr.AuthFailed, "cannot save authenticated session")
	}
	return nil
}
