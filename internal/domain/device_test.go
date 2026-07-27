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

func fixtureDevices(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "stat_device.json")
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

func TestNormalizeDevice(t *testing.T) {
	raw := fixtureDevices(t)
	if len(raw) < 3 {
		t.Fatalf("fixture devices = %d, want >= 3", len(raw))
	}

	gw := domain.NormalizeDevice(raw[0])
	if gw.ID != "gw1" || gw.MAC != "aa:bb:cc:dd:ee:01" || gw.Name != "Gateway" {
		t.Fatalf("gateway identity: %+v", gw)
	}
	if gw.Model != "UDM-SE" || gw.Type != "ugw" {
		t.Fatalf("gateway model/type: %+v", gw)
	}
	if gw.State != "connected" {
		t.Fatalf("gateway state = %q, want connected", gw.State)
	}
	if gw.IP != "192.168.1.1" || gw.Version != "3.2.7" {
		t.Fatalf("gateway ip/version: %+v", gw)
	}
	if !gw.Adopted {
		t.Fatal("gateway should be adopted")
	}
	if gw.Uplink != "" {
		t.Fatalf("gateway uplink = %q, want empty", gw.Uplink)
	}

	ap := domain.NormalizeDevice(raw[1])
	if ap.Name != "AP-Office" {
		t.Fatalf("ap name = %q (display_name fallback)", ap.Name)
	}
	if ap.IP != "192.168.1.10" {
		t.Fatalf("ap ip from last_ip = %q", ap.IP)
	}
	if ap.Version != "6.6.55" {
		t.Fatalf("ap version from sw_version = %q", ap.Version)
	}
	if ap.Uplink != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("ap uplink = %q", ap.Uplink)
	}
	if ap.Type != "uap" || ap.State != "connected" {
		t.Fatalf("ap type/state: %+v", ap)
	}

	sw := domain.NormalizeDevice(raw[2])
	if sw.Type != "usw" || sw.State != "disconnected" {
		t.Fatalf("switch type/state: %+v", sw)
	}
	if sw.Uplink != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("switch uplink = %q", sw.Uplink)
	}
}

func TestNormalizeDeviceStateMapping(t *testing.T) {
	cases := map[int]string{
		0:  "disconnected",
		1:  "connected",
		2:  "pending",
		3:  "firmware_mismatch",
		4:  "upgrading",
		5:  "provisioning",
		6:  "heartbeat_missed",
		7:  "adopting",
		8:  "deleting",
		9:  "inform_error",
		10: "adopting_failed",
		11: "isolated",
	}
	for n, want := range cases {
		got := domain.NormalizeDevice(map[string]any{"state": float64(n)}).State
		if got != want {
			t.Fatalf("state %d = %q, want %q", n, got, want)
		}
	}
	got := domain.NormalizeDevice(map[string]any{"state": float64(99)}).State
	if got != "unknown" {
		t.Fatalf("unknown state = %q", got)
	}
}

type fakeDeviceAPI struct {
	devices []map[string]any
	path    string
	method  string
	err     error
}

func (f *fakeDeviceAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.method = method
	f.path = path
	if f.err != nil {
		return f.err
	}
	b, err := json.Marshal(f.devices)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (f *fakeDeviceAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestDeviceServiceList(t *testing.T) {
	api := &fakeDeviceAPI{devices: fixtureDevices(t)}
	svc := domain.NewDeviceService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if api.method != http.MethodGet {
		t.Fatalf("method = %q", api.method)
	}
	if api.path != "/proxy/network/api/s/default/stat/device" {
		t.Fatalf("path = %q", api.path)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].ID != "gw1" || got[1].Name != "AP-Office" || got[2].Type != "usw" {
		t.Fatalf("list: %+v", got)
	}
}

func TestDeviceServiceGet(t *testing.T) {
	api := &fakeDeviceAPI{devices: fixtureDevices(t)}
	svc := domain.NewDeviceService(api)

	byID, err := svc.Get(context.Background(), "ap1")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "AP-Office" {
		t.Fatalf("by id: %+v", byID)
	}

	byMAC, err := svc.Get(context.Background(), "AA-BB-CC-DD-EE-02")
	if err != nil {
		t.Fatal(err)
	}
	if byMAC.ID != "ap1" {
		t.Fatalf("by mac: %+v", byMAC)
	}

	byName, err := svc.Get(context.Background(), "Switch-Core")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "sw1" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
