package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type mutateClientAPI struct {
	sta   []map[string]any
	calls []mutateCall
	err   error
}

func (f *mutateClientAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, mutateCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	if method == http.MethodGet {
		b, err := json.Marshal(f.sta)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	if method == http.MethodPost {
		body, _ := in.(map[string]any)
		cmd, _ := body["cmd"].(string)
		blocked, observable := map[string]bool{"block-sta": true, "unblock-sta": false}[cmd]
		if observable {
			for _, client := range f.sta {
				if strFieldTest(client, "mac") == body["mac"] {
					client["blocked"] = blocked
				}
			}
		}
	}
	if out != nil {
		_ = json.Unmarshal([]byte(`[]`), out)
	}
	return nil
}

func (f *mutateClientAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func TestClientReconnectPlanAndApply(t *testing.T) {
	api := &mutateClientAPI{sta: fixtureSta(t)}
	svc := domain.NewClientService(api)
	ctx := context.Background()

	p, c, err := svc.Reconnect(ctx, "example-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if c.MAC != "11:22:33:44:55:01" {
		t.Fatalf("client: %+v", c)
	}
	if p.Summary == "" || len(p.Changes) != 1 {
		t.Fatalf("plan: %+v", p)
	}
	if p.Changes[0].Op != "update" || p.Changes[0].ID != "sta1" {
		t.Fatalf("change: %+v", p.Changes[0])
	}

	got, err := svc.ApplyReconnect(ctx, "example-laptop")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Accepted {
		t.Fatalf("apply result: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPost {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/cmd/stamgr" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["cmd"] != "kick-sta" || body["mac"] != "11:22:33:44:55:01" {
		t.Fatalf("body = %+v", body)
	}
}

func TestClientBlockPlanAndApply(t *testing.T) {
	api := &mutateClientAPI{sta: fixtureSta(t)}
	svc := domain.NewClientService(api)
	ctx := context.Background()

	p, c, err := svc.Block(ctx, "sta1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Blocked {
		t.Fatal("preview should show currently unblocked client")
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["blocked"] != false || after["blocked"] != true {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyBlock(ctx, "sta1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Blocked {
		t.Fatalf("apply should return blocked client: %+v", got)
	}
	var mutation mutateCall
	for _, call := range api.calls {
		if call.method == http.MethodPost {
			mutation = call
		}
	}
	if mutation.path != "/proxy/network/api/s/default/cmd/stamgr" {
		t.Fatalf("call = %+v", mutation)
	}
	body, _ := mutation.body.(map[string]any)
	if body["cmd"] != "block-sta" || body["mac"] != "11:22:33:44:55:01" {
		t.Fatalf("body = %+v", body)
	}
}

func TestClientUnblockPlanAndApply(t *testing.T) {
	api := &mutateClientAPI{sta: fixtureSta(t)}
	svc := domain.NewClientService(api)
	ctx := context.Background()

	p, c, err := svc.Unblock(ctx, "sta3")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Blocked {
		t.Fatal("preview should show currently blocked client")
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["blocked"] != true || after["blocked"] != false {
		t.Fatalf("before/after: %+v %+v", before, after)
	}

	got, err := svc.ApplyUnblock(ctx, "11:22:33:44:55:03")
	if err != nil {
		t.Fatal(err)
	}
	if got.Blocked {
		t.Fatalf("apply should return unblocked client: %+v", got)
	}
	var mutation mutateCall
	for _, call := range api.calls {
		if call.method == http.MethodPost {
			mutation = call
		}
	}
	if mutation.path != "/proxy/network/api/s/default/cmd/stamgr" {
		t.Fatalf("call = %+v", mutation)
	}
	body, _ := mutation.body.(map[string]any)
	if body["cmd"] != "unblock-sta" || body["mac"] != "11:22:33:44:55:03" {
		t.Fatalf("body = %+v", body)
	}
}
