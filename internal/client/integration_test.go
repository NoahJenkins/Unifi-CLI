package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
)

type officialFixtureResource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func officialFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("fixtures", "official", name))
	if err != nil {
		t.Fatalf("read official fixture %q: %v", name, err)
	}
	return body
}

func TestFetchOfficialAllReturnsEmptyPage(t *testing.T) {
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/proxy/network/integration/v1/resources" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		if got := r.URL.Query(); got.Get("limit") != "100" || got.Get("offset") != "0" {
			t.Fatalf("query = %q, want limit=100 and offset=0", got.Encode())
		}
		_, _ = w.Write(officialFixture(t, "resources-empty.json"))
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	got, err := client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
	if err != nil {
		t.Fatalf("FetchOfficialAll: %v", err)
	}
	if len(got) != 0 || requests != 1 {
		t.Fatalf("resources = %#v, requests = %d; want empty result from one request", got, requests)
	}
}

func TestFetchOfficialAllReturnsSinglePageAndPreservesExistingQuery(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query(); got.Get("filter") != "switch & ap" || got.Get("limit") != "100" || got.Get("offset") != "0" {
			t.Fatalf("query = %q", got.Encode())
		}
		_, _ = w.Write(officialFixture(t, "resources-single.json"))
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	got, err := client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources?filter=switch+%26+ap")
	if err != nil {
		t.Fatalf("FetchOfficialAll: %v", err)
	}
	want := []officialFixtureResource{{ID: "resource-1", Name: "Main Switch"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resources = %#v, want %#v", got, want)
	}
}

func TestFetchOfficialAllAdvancesByCountUntilTotalCountReached(t *testing.T) {
	var offsets []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		if r.URL.Query().Get("limit") != "100" {
			t.Fatalf("limit = %q, want 100", r.URL.Query().Get("limit"))
		}
		switch offset {
		case "0":
			_, _ = w.Write(officialFixture(t, "resources-page-1.json"))
		case "2":
			_, _ = w.Write(officialFixture(t, "resources-page-2.json"))
		default:
			t.Fatalf("unexpected offset %q", offset)
		}
	}))
	defer srv.Close()

	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	got, err := client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
	if err != nil {
		t.Fatalf("FetchOfficialAll: %v", err)
	}
	want := []officialFixtureResource{
		{ID: "resource-1", Name: "One"},
		{ID: "resource-2", Name: "Two"},
		{ID: "resource-3", Name: "Three"},
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(offsets, []string{"0", "2"}) {
		t.Fatalf("resources = %#v, offsets = %#v; want %#v and [0 2]", got, offsets, want)
	}
}

func TestFetchOfficialAllRejectsMalformedPages(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{name: "missing metadata", fixture: "resources-missing-total.json", want: "missing required pagination fields"},
		{name: "count disagrees with data", fixture: "resources-count-mismatch.json", want: "does not match data length"},
		{name: "unexpected offset", fixture: "resources-offset-mismatch.json", want: "does not match requested offset"},
		{name: "invalid JSON", fixture: "resources-invalid.json", want: "decode official API response"},
		{name: "returned limit differs from request", fixture: "resources-limit-mismatch.json", want: "does not match requested limit"},
		{name: "count exceeds returned limit", fixture: "resources-count-over-limit.json", want: "exceeds returned limit"},
		{name: "page range exceeds total", fixture: "resources-range-over-total.json", want: "exceeds totalCount"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(officialFixture(t, tt.fixture))
			}))
			defer srv.Close()
			c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
			if err != nil {
				t.Fatalf("NewWithAPIKey: %v", err)
			}

			_, err = client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
			if !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want internal malformed-page error containing %q", err, tt.want)
			}
		})
	}
}

func TestFetchOfficialAllRejectsNonProgressingPage(t *testing.T) {
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write(officialFixture(t, "resources-non-progressing.json"))
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	_, err = client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
	if !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), "did not advance") {
		t.Fatalf("error = %v, want fail-closed non-progress error", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestFetchOfficialAllRejectsChangingTotalCount(t *testing.T) {
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		offset := r.URL.Query().Get("offset")
		items := make([]officialFixtureResource, 100)
		for i := range items {
			items[i] = officialFixtureResource{ID: fmt.Sprintf("resource-%s-%d", offset, i)}
		}
		total := 200
		if offset == "100" {
			total = 300
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"offset": mustAtoi(t, offset), "limit": 100, "count": len(items), "totalCount": total, "data": items,
		})
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	_, err = client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
	if !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), "totalCount changed") {
		t.Fatalf("error = %v, want changing-totalCount failure", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestFetchOfficialAllRejectsExcessiveTotalCountBeforeSecondRequest(t *testing.T) {
	requests := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"offset": 0, "limit": 100, "count": 1, "totalCount": 100001,
			"data": []officialFixtureResource{{ID: "resource-1"}},
		})
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	_, err = client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
	if !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("error = %v, want excessive-totalCount failure", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestFetchOfficialAllRejectsAggregateResponseBytes(t *testing.T) {
	requests := 0
	payload := strings.Repeat("x", 12<<20)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := mustAtoi(t, r.URL.Query().Get("offset"))
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"offset": offset, "limit": 100, "count": 1, "totalCount": 3,
			"data": []map[string]any{{"id": fmt.Sprintf("resource-%d", offset), "payload": payload}},
		})
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	_, err = client.FetchOfficialAll[map[string]any](context.Background(), c, "/proxy/network/integration/v1/resources")
	if !apperr.Is(err, apperr.Internal) || !strings.Contains(err.Error(), "aggregate response bytes") {
		t.Fatalf("error = %v, want aggregate-byte failure", err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

func TestFetchOfficialAllMapsPermissionDeniedWithoutResponseSecrets(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"api-key=do-not-render"}`)
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	_, err = client.FetchOfficialAll[officialFixtureResource](context.Background(), c, "/proxy/network/integration/v1/resources")
	if !apperr.Is(err, apperr.PermissionDenied) {
		t.Fatalf("error = %v, want permission_denied", err)
	}
	if strings.Contains(err.Error(), "do-not-render") {
		t.Fatalf("permission error rendered controller response: %v", err)
	}
}

func TestIntegrationSitePathMatchesExactSelectorsAndCachesResolvedUUID(t *testing.T) {
	tests := []struct {
		name     string
		selector string
	}{
		{name: "UUID", selector: "site/id"},
		{name: "internal reference", selector: "default"},
		{name: "display name", selector: "Main Campus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			siteRequests := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/proxy/network/integration/v1/sites" {
					t.Fatalf("request path = %q", r.URL.Path)
				}
				siteRequests++
				_, _ = w.Write(officialFixture(t, "sites-duplicate-id.json"))
			}))
			defer srv.Close()
			cfg := testConfig(t, srv)
			cfg.Site = tt.selector
			c, err := client.NewWithAPIKey(cfg, "key", "interactive_api_key")
			if err != nil {
				t.Fatalf("NewWithAPIKey: %v", err)
			}

			gotFirst, err := c.IntegrationSitePath(context.Background(), "dns/policies", "record ?#")
			if err != nil {
				t.Fatalf("first IntegrationSitePath: %v", err)
			}
			gotSecond, err := c.IntegrationSitePath(context.Background(), "devices")
			if err != nil {
				t.Fatalf("second IntegrationSitePath: %v", err)
			}
			wantFirst := "/proxy/network/integration/v1/sites/site%2Fid/dns%2Fpolicies/record%20%3F%23"
			wantSecond := "/proxy/network/integration/v1/sites/site%2Fid/devices"
			if gotFirst != wantFirst || gotSecond != wantSecond || siteRequests != 1 {
				t.Fatalf("paths = %q, %q; site requests = %d; want %q, %q and 1", gotFirst, gotSecond, siteRequests, wantFirst, wantSecond)
			}
		})
	}
}

func TestIntegrationSitePathRejectsDistinctAmbiguousSiteIDs(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(officialFixture(t, "sites-ambiguous-name.json"))
	}))
	defer srv.Close()
	cfg := testConfig(t, srv)
	cfg.Site = "Branch"
	c, err := client.NewWithAPIKey(cfg, "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}

	_, err = c.IntegrationSitePath(context.Background(), "devices")
	if !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("error = %v, want ambiguous_id", err)
	}
	if strings.Contains(err.Error(), "site-a") || strings.Contains(err.Error(), "site-b") {
		t.Fatalf("ambiguity error exposed site details: %v", err)
	}
}

func TestDoOfficialDecodesFullEnvelopeWhileDecodeDataRemainsLegacy(t *testing.T) {
	body := officialFixture(t, "resources-single.json")
	var legacy []officialFixtureResource
	if err := client.DecodeData(body, &legacy); err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	if len(legacy) != 1 || legacy[0].ID != "resource-1" {
		t.Fatalf("legacy decoded data = %#v", legacy)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c, err := client.NewWithAPIKey(testConfig(t, srv), "key", "interactive_api_key")
	if err != nil {
		t.Fatalf("NewWithAPIKey: %v", err)
	}
	var page client.OfficialPage[officialFixtureResource]
	if err := c.DoOfficial(context.Background(), http.MethodGet, "/proxy/network/integration/v1/resources", nil, &page); err != nil {
		t.Fatalf("DoOfficial: %v", err)
	}
	if page.Offset != 0 || page.Count != 1 || page.TotalCount != 1 {
		encoded, _ := json.Marshal(page)
		t.Fatalf("official page metadata = %s", encoded)
	}
}

func TestOfficialPathEscapesEveryDynamicSegment(t *testing.T) {
	got := client.OfficialPath("sites", "site/id", "devices", "device ?#")
	want := "/proxy/network/integration/v1/sites/site%2Fid/devices/device%20%3F%23"
	if got != want {
		t.Fatalf("OfficialPath = %q, want %q", got, want)
	}
	if strings.Contains(got, "?") || strings.Contains(got, "#") {
		t.Fatalf("OfficialPath contains unescaped query/fragment delimiters: %q", got)
	}
}

func TestOfficialPathEscapesExactDotSegments(t *testing.T) {
	got := client.OfficialPath("sites", ".", "devices", "..")
	want := "/proxy/network/integration/v1/sites/%2E/devices/%2E%2E"
	if got != want {
		t.Fatalf("OfficialPath = %q, want %q", got, want)
	}
}
