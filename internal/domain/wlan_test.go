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

func fixtureWlans(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "rest_wlanconf.json")
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

func TestNormalizeWlan(t *testing.T) {
	raw := fixtureWlans(t)
	if len(raw) < 3 {
		t.Fatalf("fixture wlans = %d, want >= 3", len(raw))
	}

	home := domain.NormalizeWlan(raw[0])
	if home.ID != "wlan1" || home.Name != "Home" {
		t.Fatalf("home identity: %+v", home)
	}
	if !home.Enabled || home.Security != "wpapsk" || home.NetworkID != "net1" {
		t.Fatalf("home fields: %+v", home)
	}
	if home.Band != "both" || home.Guest {
		t.Fatalf("home band/guest: %+v", home)
	}

	iot := domain.NormalizeWlan(raw[1])
	if iot.Band != "2g" || iot.NetworkID != "net2" {
		t.Fatalf("iot: %+v", iot)
	}

	guest := domain.NormalizeWlan(raw[2])
	if guest.Enabled || !guest.Guest || guest.Security != "open" {
		t.Fatalf("guest: %+v", guest)
	}
	if guest.NetworkID != "net3" || guest.Band != "both" {
		t.Fatalf("guest network/band: %+v", guest)
	}
}

type fakeWlanAPI struct {
	wlans []map[string]any
	calls []wlanCall
	err   error
}

type wlanCall struct {
	method string
	path   string
	body   any
}

func (f *fakeWlanAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, wlanCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	if method == http.MethodGet {
		b, err := json.Marshal(f.wlans)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	if method == http.MethodPost && out != nil {
		created := map[string]any{
			"_id":            "wlan-new",
			"name":           "Created",
			"enabled":        true,
			"security":       "wpapsk",
			"networkconf_id": "net1",
			"wlan_band":      "both",
			"is_guest":       false,
		}
		if body, ok := in.(map[string]any); ok {
			for k, v := range body {
				created[k] = v
			}
			created["_id"] = "wlan-new"
		}
		b, err := json.Marshal([]map[string]any{created})
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	if out != nil {
		_ = json.Unmarshal([]byte(`[]`), out)
	}
	return nil
}

func (f *fakeWlanAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestWlanServiceList(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)
	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(api.calls) != 1 {
		t.Fatalf("calls = %d", len(api.calls))
	}
	if api.calls[0].method != http.MethodGet {
		t.Fatalf("method = %q", api.calls[0].method)
	}
	if api.calls[0].path != "/proxy/network/api/s/default/rest/wlanconf" {
		t.Fatalf("path = %q", api.calls[0].path)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "Home" || got[2].Guest != true {
		t.Fatalf("list: %+v", got)
	}
}

func TestWlanServiceGet(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)

	byID, err := svc.Get(context.Background(), "wlan2")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "IoT" {
		t.Fatalf("by id: %+v", byID)
	}

	byName, err := svc.Get(context.Background(), "Guest")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "wlan3" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestWlanCreatePlanAndApply(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)
	ctx := context.Background()

	in := domain.WlanInput{
		Name:     "Cameras",
		Security: "wpapsk",
		Network:  "net2",
		Password: "s3cret-pass",
	}
	p, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "create" {
		t.Fatalf("plan: %+v", p)
	}
	if p.Changes[0].Name != "Cameras" {
		t.Fatalf("plan name = %q", p.Changes[0].Name)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	if after["name"] != "Cameras" || after["security"] != "wpapsk" {
		t.Fatalf("after: %+v", after)
	}
	if after["network"] != "net2" && after["networkconf_id"] != "net2" {
		t.Fatalf("after network: %+v", after)
	}
	if after["password"] != "***" {
		t.Fatalf("password in plan must be masked, got %+v", after["password"])
	}
	if _, ok := after["x_passphrase"]; ok {
		t.Fatal("plan after must not expose x_passphrase")
	}

	got, err := svc.ApplyCreate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Cameras" {
		t.Fatalf("created: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPost {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/wlanconf" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["name"] != "Cameras" || body["security"] != "wpapsk" {
		t.Fatalf("body = %+v", body)
	}
	if body["networkconf_id"] != "net2" {
		t.Fatalf("network body = %+v", body)
	}
	if body["x_passphrase"] != "s3cret-pass" {
		t.Fatalf("passphrase body = %+v", body["x_passphrase"])
	}
}

func TestWlanUpdatePlanAndApply(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)
	ctx := context.Background()

	in := domain.WlanInput{
		Name:     "IoT-Renamed",
		Security: "wpapsk",
	}
	p, n, err := svc.Update(ctx, "wlan2", in)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "IoT" {
		t.Fatalf("preview: %+v", n)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" || p.Changes[0].ID != "wlan2" {
		t.Fatalf("plan: %+v", p)
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["name"] != "IoT" || after["name"] != "IoT-Renamed" {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyUpdate(ctx, "wlan2", in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "wlan2" {
		t.Fatalf("updated: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/wlanconf/wlan2" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestWlanUpdateRequiresPasswordWhenTransitioningFromOpenToSecured(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)
	ctx := context.Background()

	_, _, err := svc.Update(ctx, "wlan3", domain.WlanInput{Security: "wpapsk"})
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("open-to-secured update error = %v", err)
	}

	_, _, err = svc.Update(ctx, "wlan3", domain.WlanInput{Security: "wpapsk", Password: "secret"})
	if err != nil {
		t.Fatalf("open-to-secured update with password: %v", err)
	}

	_, _, err = svc.Update(ctx, "wlan2", domain.WlanInput{Security: "wpapsk"})
	if err != nil {
		t.Fatalf("secured update without password should preserve the existing password: %v", err)
	}
}

func TestWlanDeletePlanAndApply(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)
	ctx := context.Background()

	p, n, err := svc.Delete(ctx, "IoT")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "wlan2" {
		t.Fatalf("wlan: %+v", n)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "delete" {
		t.Fatalf("plan: %+v", p)
	}

	got, err := svc.ApplyDelete(ctx, "IoT")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "wlan2" {
		t.Fatalf("deleted: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodDelete {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/wlanconf/wlan2" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestWlanEnableDisablePlanAndApply(t *testing.T) {
	api := &fakeWlanAPI{wlans: fixtureWlans(t)}
	svc := domain.NewWlanService(api)
	ctx := context.Background()

	// Guest is disabled in fixture → enable
	p, n, err := svc.Enable(ctx, "Guest")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "wlan3" || n.Enabled {
		t.Fatalf("preview enable: %+v", n)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" {
		t.Fatalf("enable plan: %+v", p)
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["enabled"] != false || after["enabled"] != true {
		t.Fatalf("enable before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyEnable(ctx, "Guest")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Fatalf("enabled result: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/wlanconf/wlan3" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["enabled"] != true {
		t.Fatalf("enable body = %+v", body)
	}

	// Home is enabled → disable
	p, n, err = svc.Disable(ctx, "Home")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "wlan1" || !n.Enabled {
		t.Fatalf("preview disable: %+v", n)
	}
	before, _ = p.Changes[0].Before.(map[string]any)
	after, _ = p.Changes[0].After.(map[string]any)
	if before["enabled"] != true || after["enabled"] != false {
		t.Fatalf("disable before/after: %+v %+v", before, after)
	}

	got, err = svc.ApplyDisable(ctx, "Home")
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatalf("disabled result: %+v", got)
	}
	last = api.calls[len(api.calls)-1]
	body, _ = last.body.(map[string]any)
	if body["enabled"] != false {
		t.Fatalf("disable body = %+v", body)
	}
}
