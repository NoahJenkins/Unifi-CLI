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
	"os"
	"strings"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/config"
)

type Client struct {
	cfg        config.Config
	http       *http.Client
	baseURL    string
	apiKey     string
	store      authstore.Store
	authMethod string
}

func New(cfg config.Config) (*Client, error) {
	return NewWithStore(cfg, authstore.NewStore(authstore.Options{}))
}

func NewWithStore(cfg config.Config, store authstore.Store) (*Client, error) {
	apiKey := os.Getenv("UNIFI_API_KEY")
	if apiKey != "" {
		return newClient(cfg, apiKey, "environment_api_key", store), nil
	}
	if store == nil {
		return newClient(cfg, "", "", nil), nil
	}
	apiKey, found, err := store.Load(cfg.BaseURL())
	if err != nil {
		return nil, apperr.WithCause(
			apperr.New(apperr.Internal, "load saved API key"),
			err,
		)
	}
	if !found {
		return newClient(cfg, "", "", store), nil
	}
	return newClient(cfg, apiKey, "saved_api_key", store), nil
}

func NewWithAPIKey(cfg config.Config, apiKey, method string) (*Client, error) {
	return newClient(cfg, apiKey, method, nil), nil
}

func newClient(cfg config.Config, apiKey, method string, store authstore.Store) *Client {
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
	return &Client{
		cfg:        cfg,
		baseURL:    cfg.BaseURL(),
		apiKey:     apiKey,
		store:      store,
		authMethod: method,
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

// AuthMethod describes the API-key source active for this client.
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
	err := c.doJSON(ctx, method, path, in, out)
	if !apperr.Is(err, apperr.AuthFailed) || c.authMethod != "saved_api_key" {
		return err
	}

	c.apiKey = ""
	var cleanupErr error
	if c.store != nil {
		cleanupErr = c.store.Delete(c.baseURL)
	}
	message := "authentication failed"
	if authErr := apperr.As(err); authErr != nil && authErr.Message != "" {
		message = authErr.Message
	}
	result := apperr.WithHint(
		apperr.New(apperr.AuthFailed, message),
		"run 'unifi login' to save an API key",
	)
	if cleanupErr != nil {
		return apperr.WithCause(result, cleanupErr)
	}
	return result
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
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
	req.Header.Set("X-API-KEY", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return mapTransportError(err)
	}
	defer resp.Body.Close()

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

func mapStatus(code int, _ []byte) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusUnauthorized:
		return apperr.Newf(apperr.AuthFailed, "controller returned HTTP status %d: authentication failed", code)
	case code == http.StatusForbidden:
		return apperr.Newf(apperr.PermissionDenied, "controller returned HTTP status %d: permission denied", code)
	case code == http.StatusNotFound:
		return apperr.Newf(apperr.NotFound, "controller returned HTTP status %d: not found", code)
	case code == http.StatusConflict:
		return apperr.Newf(apperr.Conflict, "controller returned HTTP status %d: conflict", code)
	default:
		return apperr.Newf(apperr.Internal, "controller returned unexpected HTTP status %d", code)
	}
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
