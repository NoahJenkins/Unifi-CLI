package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func fixtureSites(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "self_sites.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]any
	if err := client.DecodeData(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNormalizeSite(t *testing.T) {
	raw := fixtureSites(t)
	s0 := domain.NormalizeSite(raw[0])
	if s0.ID != "site1" || s0.Name != "default" || s0.Desc != "Default" {
		t.Fatalf("site0: %+v", s0)
	}
	if s0.Role != "admin" {
		t.Fatalf("role = %q", s0.Role)
	}
	s1 := domain.NormalizeSite(raw[1])
	if s1.Name != "office" || s1.Desc != "Office Site" {
		t.Fatalf("site1: %+v", s1)
	}
}

type fakeSiteAPI struct {
	sites  []map[string]any
	path   string
	method string
	err    error
}

func (f *fakeSiteAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.method = method
	f.path = path
	if f.err != nil {
		return f.err
	}
	b, err := json.Marshal(f.sites)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (f *fakeSiteAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestSiteServiceList(t *testing.T) {
	api := &fakeSiteAPI{sites: fixtureSites(t)}
	svc := domain.NewSiteService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if api.method != http.MethodGet {
		t.Fatalf("method = %q", api.method)
	}
	if api.path != client.PathSelfSites && api.path != "/proxy/network/api/self/sites" {
		t.Fatalf("path = %q", api.path)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "default" || got[1].ID != "site2" {
		t.Fatalf("list: %+v", got)
	}
}

func TestSiteServiceGet(t *testing.T) {
	api := &fakeSiteAPI{sites: fixtureSites(t)}
	svc := domain.NewSiteService(api)

	byID, err := svc.Get(context.Background(), "site2")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "office" {
		t.Fatalf("by id: %+v", byID)
	}

	byName, err := svc.Get(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "site1" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
