package domain_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type fakeSystemAPI struct {
	byPath map[string][]map[string]any
	errs   map[string]error
	calls  []string
}

func (f *fakeSystemAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, method+" "+path)
	if f.errs != nil {
		if err, ok := f.errs[path]; ok {
			return err
		}
	}
	data, ok := f.byPath[path]
	if !ok {
		return apperr.New(apperr.NotFound, "not found")
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (f *fakeSystemAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestSystemHealthFromDevices(t *testing.T) {
	api := &fakeSystemAPI{
		byPath: map[string][]map[string]any{
			"/proxy/network/api/s/default/stat/device": fixtureDevices(t),
			"/proxy/network/api/s/default/stat/sta":    fixtureSta(t),
		},
	}
	svc := domain.NewSystemService(api)
	h, err := svc.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.DeviceTotal != 3 {
		t.Fatalf("device_total = %d", h.DeviceTotal)
	}
	if h.DeviceConnected != 2 {
		t.Fatalf("device_connected = %d", h.DeviceConnected)
	}
	if h.ClientTotal != 3 {
		t.Fatalf("client_total = %d", h.ClientTotal)
	}
	if h.Status != "degraded" {
		t.Fatalf("status = %q, want degraded (1 disconnected adopted)", h.Status)
	}
}

func TestSystemHealthAllConnected(t *testing.T) {
	devs := fixtureDevices(t)
	for _, d := range devs {
		d["state"] = float64(1)
	}
	// fixtureDevices returns maps that are shared refs — re-read and set
	raw := fixtureDevices(t)
	for i := range raw {
		raw[i]["state"] = float64(1)
	}
	api := &fakeSystemAPI{
		byPath: map[string][]map[string]any{
			"/proxy/network/api/s/default/stat/device": raw,
			"/proxy/network/api/s/default/stat/sta":    fixtureSta(t),
		},
	}
	svc := domain.NewSystemService(api)
	h, err := svc.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "ok" {
		t.Fatalf("status = %q, want ok", h.Status)
	}
	if h.DeviceConnected != 3 {
		t.Fatalf("connected = %d", h.DeviceConnected)
	}
}

func TestSystemHealthNoneConnected(t *testing.T) {
	raw := fixtureDevices(t)
	for i := range raw {
		raw[i]["state"] = float64(0)
	}
	api := &fakeSystemAPI{
		byPath: map[string][]map[string]any{
			"/proxy/network/api/s/default/stat/device": raw,
			"/proxy/network/api/s/default/stat/sta":    nil,
		},
	}
	// empty sta still needs key
	api.byPath["/proxy/network/api/s/default/stat/sta"] = []map[string]any{}
	svc := domain.NewSystemService(api)
	h, err := svc.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != "error" {
		t.Fatalf("status = %q, want error", h.Status)
	}
	if h.ClientTotal != 0 {
		t.Fatalf("client_total = %d", h.ClientTotal)
	}
}
