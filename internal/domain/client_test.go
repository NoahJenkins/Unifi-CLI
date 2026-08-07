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

func fixtureSta(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "stat_sta.json")
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

func TestNormalizeClient(t *testing.T) {
	raw := fixtureSta(t)
	if len(raw) < 3 {
		t.Fatalf("fixture sta = %d, want >= 3", len(raw))
	}

	c0 := domain.NormalizeClient(raw[0])
	if c0.ID != "sta1" || c0.MAC != "11:22:33:44:55:01" {
		t.Fatalf("identity: %+v", c0)
	}
	if c0.Hostname != "laptop.local" || c0.Name != "example-laptop" {
		t.Fatalf("hostname/name: %+v", c0)
	}
	if c0.IP != "192.168.1.50" || c0.ESSID != "Home" || c0.Network != "LAN" {
		t.Fatalf("network fields: %+v", c0)
	}
	if c0.IsWired || c0.Blocked {
		t.Fatalf("flags: %+v", c0)
	}
	if c0.LastSeen != "1722000000" {
		t.Fatalf("last_seen = %q", c0.LastSeen)
	}

	c1 := domain.NormalizeClient(raw[1])
	if !c1.IsWired {
		t.Fatal("sta2 should be wired")
	}
	if c1.Name != "printer" {
		t.Fatalf("name fallback to hostname = %q", c1.Name)
	}
	if c1.LastSeen != "1722000100" {
		t.Fatalf("last_seen string = %q", c1.LastSeen)
	}

	c2 := domain.NormalizeClient(raw[2])
	if !c2.Blocked {
		t.Fatal("sta3 should be blocked")
	}
	if c2.Name != "" && c2.Hostname != "" {
		// no name/hostname in fixture for sta3
	}
	if c2.ESSID != "Guest" {
		t.Fatalf("essid = %q", c2.ESSID)
	}
}

type fakeClientAPI struct {
	sta    []map[string]any
	path   string
	method string
	err    error
}

func (f *fakeClientAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.method = method
	f.path = path
	if f.err != nil {
		return f.err
	}
	b, err := json.Marshal(f.sta)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (f *fakeClientAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestClientServiceList(t *testing.T) {
	api := &fakeClientAPI{sta: fixtureSta(t)}
	svc := domain.NewClientService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if api.method != http.MethodGet {
		t.Fatalf("method = %q", api.method)
	}
	if api.path != "/proxy/network/api/s/default/stat/sta" {
		t.Fatalf("path = %q", api.path)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].ID != "sta1" || got[1].MAC != "11:22:33:44:55:02" {
		t.Fatalf("list: %+v", got)
	}
}

func TestClientServiceGet(t *testing.T) {
	api := &fakeClientAPI{sta: fixtureSta(t)}
	svc := domain.NewClientService(api)

	byID, err := svc.Get(context.Background(), "sta1")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "example-laptop" {
		t.Fatalf("by id: %+v", byID)
	}

	byMAC, err := svc.Get(context.Background(), "11-22-33-44-55-02")
	if err != nil {
		t.Fatal(err)
	}
	if byMAC.ID != "sta2" {
		t.Fatalf("by mac: %+v", byMAC)
	}

	byName, err := svc.Get(context.Background(), "example-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "sta1" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
