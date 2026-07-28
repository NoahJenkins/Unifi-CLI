package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/session"
)

type Client struct {
	cfg               config.Config
	http              *http.Client
	baseURL           string
	csrf              string
	cookieJar         http.CookieJar
	sessionStore      session.Store
	loggedIn          bool
	authMethod        string
	allowFileFallback bool
}

func New(cfg config.Config) (*Client, error) {
	return NewWithSessionStore(cfg, session.NewStore(session.Options{}))
}

// NewWithSessionStore creates a controller client that can restore a saved
// password session. API-key clients deliberately bypass the store entirely.
func NewWithSessionStore(cfg config.Config, store session.Store) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, apperr.Newf(apperr.Internal, "cookie jar: %v", err)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			// Local controllers commonly use self-signed certs.
			InsecureSkipVerify: cfg.Insecure, //nolint:gosec
		},
	}
	c := &Client{
		cfg:          cfg,
		baseURL:      cfg.BaseURL(),
		cookieJar:    jar,
		sessionStore: store,
		authMethod:   "password",
		http: &http.Client{
			Timeout:   timeout,
			Jar:       jar,
			Transport: transport,
		},
	}
	if cfg.APIKey != "" {
		c.authMethod = "api_key"
		return c, nil
	}
	if store == nil {
		return c, nil
	}
	record, found, err := store.Load(c.baseURL)
	if err != nil {
		if errors.Is(err, session.ErrKeyringUnavailable) {
			return c, nil
		}
		return nil, apperr.New(apperr.Internal, "load saved session")
	}
	if !found {
		return c, nil
	}
	baseURL, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, apperr.Newf(apperr.Internal, "parse controller URL: %v", err)
	}
	c.cookieJar.SetCookies(baseURL, record.HTTPCookies())
	c.csrf = record.CSRF
	c.loggedIn = true
	c.authMethod = "saved_session"
	return c, nil
}

// SetAllowFileFallback permits this client to save a new password session to
// protected local state only if the native keyring is unavailable.
func (c *Client) SetAllowFileFallback(allow bool) {
	c.allowFileFallback = allow
}

// AuthMethod describes the authentication path active for this client.
func (c *Client) AuthMethod() string {
	return c.authMethod
}

func (c *Client) Site() string {
	return c.cfg.Site
}

func (c *Client) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/" + c.cfg.Site
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		p += "/" + part
	}
	return p
}

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	err := c.doJSON(ctx, method, path, in, out, true)
	if !apperr.Is(err, apperr.AuthFailed) || c.cfg.APIKey != "" {
		return err
	}
	c.loggedIn = false
	if c.sessionStore != nil {
		_ = c.sessionStore.Delete(c.baseURL)
	}
	message := "authentication failed"
	if authErr := apperr.As(err); authErr != nil && authErr.Message != "" {
		message = authErr.Message
	}
	return apperr.WithHint(
		apperr.New(apperr.AuthFailed, message),
		"run 'unifi auth login' to create a new saved session",
	)
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any, _ bool) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return apperr.Newf(apperr.Internal, "marshal request: %v", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return apperr.Newf(apperr.Internal, "build request: %v", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("X-API-KEY", c.cfg.APIKey)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return mapTransportError(err)
	}
	defer resp.Body.Close()

	if tok := resp.Header.Get("X-CSRF-Token"); tok != "" {
		c.csrf = tok
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return apperr.Newf(apperr.ControllerUnreachable, "read response: %v", err)
	}

	if err := mapStatus(resp.StatusCode, respBody); err != nil {
		return err
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := DecodeData(respBody, out); err != nil {
		return apperr.Newf(apperr.Internal, "decode response: %v", err)
	}
	return nil
}

func mapStatus(code int, body []byte) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized:
		return apperr.New(apperr.AuthFailed, statusMessage(body, "authentication failed"))
	case code == http.StatusForbidden:
		return apperr.New(apperr.PermissionDenied, statusMessage(body, "permission denied"))
	case code == http.StatusNotFound:
		return apperr.New(apperr.NotFound, statusMessage(body, "not found"))
	case code == http.StatusConflict:
		return apperr.New(apperr.Conflict, statusMessage(body, "conflict"))
	default:
		return apperr.Newf(apperr.Internal, "unexpected status %d: %s", code, truncate(string(body), 200))
	}
}

func statusMessage(body []byte, fallback string) string {
	var envelope struct {
		Meta struct {
			Msg string `json:"msg"`
			RC  string `json:"rc"`
		} `json:"meta"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		if envelope.Meta.Msg != "" {
			return envelope.Meta.Msg
		}
		if envelope.Message != "" {
			return envelope.Message
		}
	}
	return fallback
}

func mapTransportError(err error) error {
	hint := "check host, port, TLS settings, and that the controller is online"
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return apperr.WithHint(
			apperr.New(apperr.ControllerUnreachable, "controller request timed out"),
			hint,
		)
	}
	return apperr.WithHint(
		apperr.New(apperr.ControllerUnreachable, "cannot reach controller"),
		hint,
	)
}

// DecodeData unmarshals body into out. When body is a UniFi envelope with a
// "data" field, only that field is decoded.
func DecodeData(body []byte, out any) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err == nil {
		if data, ok := probe["data"]; ok {
			return json.Unmarshal(data, out)
		}
	}
	return json.Unmarshal(body, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
