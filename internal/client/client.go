package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/authstore"
	"github.com/noahjenkins/unifi-cli/internal/config"
	"github.com/noahjenkins/unifi-cli/internal/fileutil"
)

type Client struct {
	cfg        config.Config
	http       *http.Client
	baseURL    string
	apiKey     string
	store      authstore.Store
	authMethod string

	integrationSiteMu sync.Mutex
	integrationSiteID string
}

const (
	maxResponseBodyBytes = 16 << 20
	maxCACertBytes       = 1 << 20
)

func New(cfg config.Config) (*Client, error) {
	return NewWithStore(cfg, authstore.NewStore(authstore.Options{}))
}

func NewWithStore(cfg config.Config, store authstore.Store) (*Client, error) {
	var err error
	cfg, err = validatedConfig(cfg)
	if err != nil {
		return nil, err
	}
	apiKey := os.Getenv("UNIFI_API_KEY")
	if apiKey != "" {
		return newClient(cfg, apiKey, "environment_api_key", store)
	}
	if store == nil {
		return newClient(cfg, "", "", nil)
	}
	apiKey, found, err := store.Load(cfg.BaseURL())
	if err != nil {
		return nil, apperr.WithCause(
			apperr.New(apperr.Internal, "load saved API key"),
			err,
		)
	}
	if !found {
		return newClient(cfg, "", "", store)
	}
	return newClient(cfg, apiKey, "saved_api_key", store)
}

func NewWithAPIKey(cfg config.Config, apiKey, method string) (*Client, error) {
	var err error
	cfg, err = validatedConfig(cfg)
	if err != nil {
		return nil, err
	}
	return newClient(cfg, apiKey, method, nil)
}

func validatedConfig(cfg config.Config) (config.Config, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func newClient(cfg config.Config, apiKey, method string, store authstore.Store) (*Client, error) {
	tlsConfig := &tls.Config{
		// Local controllers may require either a custom CA or explicit insecure mode.
		InsecureSkipVerify: cfg.Insecure, //nolint:gosec
	}
	if cfg.CACert != "" {
		rootCAs, err := x509.SystemCertPool()
		if err != nil || rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}
		caPEM, err := fileutil.ReadRegularFile(cfg.CACert, maxCACertBytes)
		if err != nil {
			return nil, fmt.Errorf("load ca_cert: %w", err)
		}
		if !rootCAs.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("load ca_cert: no certificates found")
		}
		tlsConfig.RootCAs = rootCAs
	}
	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	return &Client{
		cfg:        cfg,
		baseURL:    cfg.BaseURL(),
		apiKey:     apiKey,
		store:      store,
		authMethod: method,
		http: &http.Client{
			Timeout:       cfg.Timeout,
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// AuthMethod describes the API-key source active for this client.
func (c *Client) AuthMethod() string {
	return c.authMethod
}

func (c *Client) Site() string {
	return c.cfg.Site
}

func (c *Client) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/" + escapeLegacySegment(c.cfg.Site)
	for i, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		if i == 0 && isLegacyStaticRoute(part) {
			for _, segment := range strings.Split(part, "/") {
				p += "/" + url.PathEscape(segment)
			}
			continue
		}
		p += "/" + escapeLegacySegment(part)
	}
	return p
}

func escapeLegacySegment(value string) string {
	if value == "." || value == ".." {
		return strings.Repeat("%2E", len(value))
	}
	return url.PathEscape(value)
}

func isLegacyStaticRoute(part string) bool {
	switch part {
	case PathStatDevice, PathStatSta, PathStatHealth, PathCmdDevMgr, PathCmdStaMgr,
		PathRestDevice, PathRestNetwork, PathRestWlan, PathRestUser:
		return true
	default:
		return false
	}
}

func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	return c.doWithAuth(ctx, func() error {
		return c.doJSON(ctx, method, path, in, out)
	})
}

// DoOfficial performs a request without applying the legacy UniFi "data"
// unwrapping behavior. Official Network API callers need the complete response
// envelope so they can validate pagination metadata.
func (c *Client) DoOfficial(ctx context.Context, method, path string, in, out any) error {
	return c.doWithAuth(ctx, func() error {
		return c.doOfficialJSON(ctx, method, path, in, out)
	})
}

func (c *Client) doWithAuth(ctx context.Context, request func() error) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	err := request()
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
	return c.doJSONWithDecoder(ctx, method, path, in, out, DecodeData, "")
}

func (c *Client) doOfficialJSON(ctx context.Context, method, path string, in, out any) error {
	return c.doJSONWithDecoder(ctx, method, path, in, out, json.Unmarshal, "official API ")
}

func (c *Client) doJSONWithDecoder(
	ctx context.Context,
	method, path string,
	in, out any,
	decode func([]byte, any) error,
	decodeKind string,
) error {
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return apperr.Newf(apperr.ControllerUnreachable, "read response: %v", err)
	}
	if len(respBody) > maxResponseBodyBytes {
		return apperr.New(apperr.Internal, "controller response is too large")
	}

	if err := mapStatus(resp.StatusCode, respBody); err != nil {
		return err
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := decode(respBody, out); err != nil {
		return apperr.Newf(apperr.Internal, "decode %sresponse: %v", decodeKind, err)
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
