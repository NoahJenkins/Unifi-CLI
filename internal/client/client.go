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
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
	authMu     sync.Mutex

	integrationSiteMu sync.Mutex
	integrationSiteID string
}

const (
	maxResponseBodyBytes           = 16 << 20
	maxCACertBytes                 = 1 << 20
	maxControllerErrorCodeBytes    = 128
	maxControllerErrorMessageBytes = 512
)

var (
	controllerErrorCodePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,127}$`)
	controllerSecretPattern      = regexp.MustCompile(`(?i)\b(api[-_ ]?key|authorization|bearer|token|password|passphrase|secret|cookie|csrf)\b\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)
	controllerURLPattern         = regexp.MustCompile(`(?i)https?://[^\s,;]+`)
	controllerUUIDPattern        = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	controllerMACPattern         = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)
	controllerIPv4Pattern        = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?:/[0-9]{1,3})?\b`)
	controllerIPv6Pattern        = regexp.MustCompile(`(?i)(?:\b[0-9a-f]{1,4})?(?::[0-9a-f]{0,4}){2,7}\b`)
	controllerQuotedValuePattern = regexp.MustCompile("(?:\\\"[^\\\"\\r\\n]*\\\"|'[^'\\r\\n]*'|`[^`\\r\\n]*`)")
	controllerLongTokenPattern   = regexp.MustCompile(`\b[A-Za-z0-9_+/=-]{24,}\b`)
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
	c.authMu.Lock()
	defer c.authMu.Unlock()
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
	return c.doWithAuth(ctx, func(apiKey string) error {
		return c.doJSON(ctx, apiKey, method, path, in, out)
	})
}

// DoOfficial performs a request without applying the legacy UniFi "data"
// unwrapping behavior. Official Network API callers need the complete response
// envelope so they can validate pagination metadata.
func (c *Client) DoOfficial(ctx context.Context, method, path string, in, out any) error {
	return c.doWithAuth(ctx, func(apiKey string) error {
		return c.doOfficialJSON(ctx, apiKey, method, path, in, out)
	})
}

// DoOfficialSized performs an official API request and returns the number of
// response bytes consumed after all transport and decoding checks succeed.
func (c *Client) DoOfficialSized(ctx context.Context, method, path string, in, out any) (int, error) {
	var responseBytes int
	err := c.doWithAuth(ctx, func(apiKey string) error {
		var err error
		responseBytes, err = c.doJSONWithDecoder(ctx, apiKey, method, path, in, out, json.Unmarshal, "official API ", true)
		return err
	})
	return responseBytes, err
}

func (c *Client) doWithAuth(ctx context.Context, request func(string) error) error {
	apiKey, authMethod, err := c.authSnapshot(ctx)
	if err != nil {
		return err
	}
	err = request(apiKey)
	if !apperr.Is(err, apperr.AuthFailed) || authMethod != "saved_api_key" {
		return err
	}

	c.authMu.Lock()
	if c.authMethod == authMethod && c.apiKey == apiKey {
		c.apiKey = ""
		c.authMethod = ""
	}
	c.authMu.Unlock()
	message := "authentication failed"
	if authErr := apperr.As(err); authErr != nil && authErr.Message != "" {
		message = authErr.Message
	}
	return apperr.WithHint(
		apperr.New(apperr.AuthFailed, message),
		"run 'unifi login' to save an API key",
	)
}

func (c *Client) doJSON(ctx context.Context, apiKey, method, path string, in, out any) error {
	_, err := c.doJSONWithDecoder(ctx, apiKey, method, path, in, out, DecodeData, "", false)
	return err
}

func (c *Client) doOfficialJSON(ctx context.Context, apiKey, method, path string, in, out any) error {
	_, err := c.doJSONWithDecoder(ctx, apiKey, method, path, in, out, json.Unmarshal, "official API ", true)
	return err
}

func (c *Client) doJSONWithDecoder(
	ctx context.Context,
	apiKey string,
	method, path string,
	in, out any,
	decode func([]byte, any) error,
	decodeKind string,
	officialErrors bool,
) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, apperr.Newf(apperr.Internal, "marshal request: %v", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, apperr.Newf(apperr.Internal, "build request: %v", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, mapTransportError(err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return 0, apperr.Newf(apperr.ControllerUnreachable, "read response: %v", err)
	}
	if len(respBody) > maxResponseBodyBytes {
		return 0, apperr.New(apperr.Internal, "controller response is too large")
	}

	if err := mapStatus(resp.StatusCode, respBody, officialErrors); err != nil {
		return 0, err
	}
	if out == nil || len(respBody) == 0 {
		return len(respBody), nil
	}
	if err := decode(respBody, out); err != nil {
		return 0, apperr.Newf(apperr.Internal, "decode %sresponse: %v", decodeKind, err)
	}
	return len(respBody), nil
}

func mapStatus(code int, body []byte, officialErrors bool) error {
	switch {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusBadRequest && officialErrors:
		return mapBadRequest(body)
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

type controllerErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func mapBadRequest(body []byte) error {
	message := "validation failed"
	var response controllerErrorResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return apperr.Newf(apperr.ValidationFailed, "controller returned HTTP status %d: %s", http.StatusBadRequest, message)
	}

	code, codeOK := sanitizeControllerErrorCode(response.Code)
	detail, detailOK := sanitizeControllerErrorMessage(response.Message)
	switch {
	case codeOK && detailOK:
		message = code + ": " + detail
	case codeOK:
		message = code
	}
	return apperr.Newf(apperr.ValidationFailed, "controller returned HTTP status %d: %s", http.StatusBadRequest, message)
}

func sanitizeControllerErrorCode(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxControllerErrorCodeBytes || !controllerErrorCodePattern.MatchString(value) ||
		controllerUUIDPattern.MatchString(value) || controllerIPv4Pattern.MatchString(value) ||
		controllerLongTokenPattern.MatchString(value) {
		return "", false
	}
	return value, true
}

func sanitizeControllerErrorMessage(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxControllerErrorMessageBytes || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "{}[]") || containsUnsafeControllerErrorRune(value) {
		return "", false
	}

	value = controllerSecretPattern.ReplaceAllString(value, "$1=[redacted]")
	for _, pattern := range []*regexp.Regexp{
		controllerURLPattern,
		controllerUUIDPattern,
		controllerMACPattern,
		controllerIPv4Pattern,
		controllerIPv6Pattern,
		controllerQuotedValuePattern,
		controllerLongTokenPattern,
	} {
		value = pattern.ReplaceAllString(value, "[redacted]")
	}
	return strings.Join(strings.Fields(value), " "), true
}

func containsUnsafeControllerErrorRune(value string) bool {
	for _, current := range value {
		if unicode.IsControl(current) || unicode.In(current, unicode.Cf) {
			return true
		}
	}
	return false
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
