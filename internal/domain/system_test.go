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

func fixtureJSON(t *testing.T, name string) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", name)
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

func TestNormalizeEvent(t *testing.T) {
	raw := fixtureJSON(t, "stat_event.json")
	e0 := domain.NormalizeEvent(raw[0])
	if e0.ID != "evt1" {
		t.Fatalf("id: %+v", e0)
	}
	if e0.Message == "" {
		t.Fatal("message empty")
	}
	if e0.Time == "" {
		t.Fatal("time empty")
	}
	e1 := domain.NormalizeEvent(raw[1])
	if e1.Message != "AP disconnected" {
		t.Fatalf("message = %q", e1.Message)
	}
	if e1.Time != "2024-07-26T12:00:00Z" {
		t.Fatalf("time = %q", e1.Time)
	}
}

func TestSystemEvents(t *testing.T) {
	api := &fakeSystemAPI{
		byPath: map[string][]map[string]any{
			"/proxy/network/api/s/default/stat/event": fixtureJSON(t, "stat_event.json"),
		},
	}
	svc := domain.NewSystemService(api)
	got, err := svc.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].ID != "evt1" {
		t.Fatalf("got: %+v", got)
	}
}

func TestSystemEventsNotImplementedOn404(t *testing.T) {
	api := &fakeSystemAPI{
		errs: map[string]error{
			"/proxy/network/api/s/default/stat/event": apperr.New(apperr.NotFound, "not found"),
		},
	}
	svc := domain.NewSystemService(api)
	_, err := svc.Events(context.Background())
	if !apperr.Is(err, apperr.NotImplemented) {
		t.Fatalf("err = %v, want not_implemented", err)
	}
}

func TestSystemAlerts(t *testing.T) {
	api := &fakeSystemAPI{
		byPath: map[string][]map[string]any{
			"/proxy/network/api/s/default/stat/alarm": fixtureJSON(t, "stat_alarm.json"),
		},
	}
	svc := domain.NewSystemService(api)
	got, err := svc.Alerts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "alm1" {
		t.Fatalf("got: %+v", got)
	}
	if got[0].Message == "" {
		t.Fatal("message empty")
	}
}

func TestSystemAlertsNotImplementedOn404(t *testing.T) {
	api := &fakeSystemAPI{
		errs: map[string]error{
			"/proxy/network/api/s/default/stat/alarm": apperr.New(apperr.NotFound, "not found"),
		},
	}
	svc := domain.NewSystemService(api)
	_, err := svc.Alerts(context.Background())
	if !apperr.Is(err, apperr.NotImplemented) {
		t.Fatalf("err = %v, want not_implemented", err)
	}
}

// silence unused import if http only used in other packages via paths
var _ = http.MethodGet
