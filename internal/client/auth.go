package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
	if c.sessionStore != nil {
		baseURL, err := url.Parse(c.baseURL)
		if err != nil {
			return apperr.Newf(apperr.Internal, "parse controller URL: %v", err)
		}
		record := session.Session{
			Controller: c.baseURL,
			Cookies:    session.CookiesFromHTTP(c.cookieJar.Cookies(baseURL)),
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
	}
	c.loggedIn = true
	c.authMethod = "password"
	return nil
}
