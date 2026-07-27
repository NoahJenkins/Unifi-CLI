package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type mutateDeviceAPI struct {
	devices []map[string]any
	calls   []mutateCall
	err     error
}

type mutateCall struct {
	method string
	path   string
	body   any
}

func (f *mutateDeviceAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, mutateCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	// list responses
	if method == http.MethodGet {
		b, err := json.Marshal(f.devices)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	// mutate responses: empty ok
	if out != nil {
		_ = json.Unmarshal([]byte(`[]`), out)
	}
	return nil
}

func (f *mutateDeviceAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestDeviceRenamePlanAndApply(t *testing.T) {
	api := &mutateDeviceAPI{devices: fixtureDevices(t)}
	svc := domain.NewDeviceService(api)

	p, d, err := svc.Rename(context.Background(), "ap1", "AP-Lobby")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "AP-Office" {
		t.Fatalf("preview device name = %q", d.Name)
	}
	if p.Summary == "" || len(p.Changes) != 1 {
		t.Fatalf("plan: %+v", p)
	}
	if p.Changes[0].Op != "update" || p.Changes[0].ID != "ap1" {
		t.Fatalf("change: %+v", p.Changes[0])
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["name"] != "AP-Office" || after["name"] != "AP-Lobby" {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyRename(context.Background(), "ap1", "AP-Lobby")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "ap1" {
		t.Fatalf("apply result: %+v", got)
	}
	// last call should be PUT rest/device/ap1
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/device/ap1" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["name"] != "AP-Lobby" {
		t.Fatalf("body = %+v", body)
	}
}

func TestDeviceRestartPlanAndApply(t *testing.T) {
	api := &mutateDeviceAPI{devices: fixtureDevices(t)}
	svc := domain.NewDeviceService(api)

	p, d, err := svc.Restart(context.Background(), "AP-Office")
	if err != nil {
		t.Fatal(err)
	}
	if d.MAC != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("device: %+v", d)
	}
	if p.Changes[0].Op != "update" {
		t.Fatalf("op = %q", p.Changes[0].Op)
	}

	_, err = svc.ApplyRestart(context.Background(), "AP-Office")
	if err != nil {
		t.Fatal(err)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPost {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/cmd/devmgr" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["cmd"] != "restart" || body["mac"] != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("body = %+v", body)
	}
}

func TestDeviceLocateUpgradeAdoptForget(t *testing.T) {
	api := &mutateDeviceAPI{devices: fixtureDevices(t)}
	svc := domain.NewDeviceService(api)
	ctx := context.Background()

	cases := []struct {
		name    string
		planFn  func() error
		applyFn func() error
		cmd     string
		op      string
	}{
		{
			name: "locate",
			planFn: func() error {
				_, _, err := svc.Locate(ctx, "ap1")
				return err
			},
			applyFn: func() error {
				_, err := svc.ApplyLocate(ctx, "ap1")
				return err
			},
			cmd: "set-locate",
			op:  "update",
		},
		{
			name: "upgrade",
			planFn: func() error {
				_, _, err := svc.Upgrade(ctx, "ap1")
				return err
			},
			applyFn: func() error {
				_, err := svc.ApplyUpgrade(ctx, "ap1")
				return err
			},
			cmd: "upgrade",
			op:  "update",
		},
		{
			name: "adopt",
			planFn: func() error {
				_, _, err := svc.Adopt(ctx, "ap1")
				return err
			},
			applyFn: func() error {
				_, err := svc.ApplyAdopt(ctx, "ap1")
				return err
			},
			cmd: "adopt",
			op:  "update",
		},
		{
			name: "forget",
			planFn: func() error {
				_, _, err := svc.Forget(ctx, "ap1")
				return err
			},
			applyFn: func() error {
				_, err := svc.ApplyForget(ctx, "ap1")
				return err
			},
			cmd: "delete-device",
			op:  "delete",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api.calls = nil
			if err := tc.planFn(); err != nil {
				t.Fatal(err)
			}
			if err := tc.applyFn(); err != nil {
				t.Fatal(err)
			}
			last := api.calls[len(api.calls)-1]
			if last.method != http.MethodPost || last.path != "/proxy/network/api/s/default/cmd/devmgr" {
				t.Fatalf("call = %+v", last)
			}
			body, _ := last.body.(map[string]any)
			if body["cmd"] != tc.cmd || body["mac"] != "aa:bb:cc:dd:ee:02" {
				t.Fatalf("body = %+v want cmd=%s", body, tc.cmd)
			}
		})
	}
}

func TestDeviceForgetPlanIsDelete(t *testing.T) {
	api := &mutateDeviceAPI{devices: fixtureDevices(t)}
	svc := domain.NewDeviceService(api)
	p, d, err := svc.Forget(context.Background(), "sw1")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != "sw1" {
		t.Fatalf("device: %+v", d)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "delete" {
		t.Fatalf("plan: %+v", p)
	}
}
