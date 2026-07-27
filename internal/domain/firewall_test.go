package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func fixtureFirewallRules(t *testing.T) []map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "client", "fixtures", "rest_firewallrule.json")
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

func TestNormalizeFirewallRule(t *testing.T) {
	raw := fixtureFirewallRules(t)
	if len(raw) < 3 {
		t.Fatalf("fixture = %d, want >= 3", len(raw))
	}

	r1 := domain.NormalizeFirewallRule(raw[0])
	if r1.ID != "fw1" || r1.Name != "Allow LAN DNS" || !r1.Enabled {
		t.Fatalf("r1: %+v", r1)
	}
	if r1.Action != "accept" || r1.Ruleset != "LAN_IN" {
		t.Fatalf("r1 action/ruleset: %+v", r1)
	}
	if r1.Src != "192.168.1.0/24" || r1.Dst != "192.168.1.1" || r1.Protocol != "udp" {
		t.Fatalf("r1 endpoints: %+v", r1)
	}
	if r1.Index != 2000 {
		t.Fatalf("r1 index = %d", r1.Index)
	}

	r2 := domain.NormalizeFirewallRule(raw[1])
	if r2.ID != "fw2" || r2.Name != "Block IoT WAN" || r2.Enabled {
		t.Fatalf("r2: %+v", r2)
	}
	if r2.Action != "drop" || r2.Src != "10.0.20.0/24" || r2.Dst != "any" {
		t.Fatalf("r2 fields: %+v", r2)
	}
	if r2.Index != 2010 {
		t.Fatalf("r2 index = %d", r2.Index)
	}

	r3 := domain.NormalizeFirewallRule(raw[2])
	if r3.Src != "any" || r3.Dst != "192.168.1.50" || r3.Protocol != "tcp" || r3.Index != 3000 {
		t.Fatalf("r3: %+v", r3)
	}
}

type fakeFirewallAPI struct {
	rules     []map[string]any
	calls     []fwCall
	errByPath map[string]error
	err       error
}

type fwCall struct {
	method string
	path   string
	body   any
}

func (f *fakeFirewallAPI) Do(ctx context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, fwCall{method: method, path: path, body: in})
	if f.err != nil {
		return f.err
	}
	if f.errByPath != nil {
		if err, ok := f.errByPath[path]; ok {
			return err
		}
	}
	switch {
	case method == http.MethodGet && strings.Contains(path, "rest/firewallrule") && !hasTrailingID(path, "rest/firewallrule"):
		return decodeInto(f.rules, out)
	case method == http.MethodPost && strings.Contains(path, "rest/firewallrule") && out != nil:
		created := map[string]any{
			"_id":        "fw-new",
			"name":       "new-rule",
			"enabled":    true,
			"action":     "accept",
			"ruleset":    "LAN_IN",
			"protocol":   "all",
			"rule_index": 2100,
		}
		if body, ok := in.(map[string]any); ok {
			for k, v := range body {
				created[k] = v
			}
			created["_id"] = "fw-new"
		}
		return decodeInto([]map[string]any{created}, out)
	default:
		if out != nil {
			_ = json.Unmarshal([]byte(`[]`), out)
		}
		return nil
	}
}

func (f *fakeFirewallAPI) SitePath(parts ...string) string {
	p := "/proxy/network/api/s/default"
	for _, part := range parts {
		p += "/" + part
	}
	return p
}

func hasTrailingID(path, base string) bool {
	// true when path ends with /rest/firewallrule/<id>
	idx := strings.LastIndex(path, base)
	if idx < 0 {
		return false
	}
	rest := path[idx+len(base):]
	return strings.HasPrefix(rest, "/") && len(rest) > 1
}

func TestFirewallServiceList(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
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
	if api.calls[0].path != "/proxy/network/api/s/default/rest/firewallrule" {
		t.Fatalf("path = %q", api.calls[0].path)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Name != "Allow LAN DNS" || got[0].Index != 2000 {
		t.Fatalf("got[0]: %+v", got[0])
	}
}

func TestFirewallServiceGet(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)

	byID, err := svc.Get(context.Background(), "fw1")
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "Allow LAN DNS" {
		t.Fatalf("by id: %+v", byID)
	}

	byName, err := svc.Get(context.Background(), "Block IoT WAN")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != "fw2" {
		t.Fatalf("by name: %+v", byName)
	}

	_, err = svc.Get(context.Background(), "missing")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestFirewallCreatePlanAndApply(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
	ctx := context.Background()

	in := domain.FirewallInput{
		Name:     "Drop Guest",
		Enabled:  true,
		Action:   "drop",
		Ruleset:  "GUEST_IN",
		Src:      "any",
		Dst:      "192.168.1.0/24",
		Protocol: "all",
		Index:    2500,
	}
	p, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "create" {
		t.Fatalf("plan: %+v", p)
	}
	if p.Changes[0].Name != "Drop Guest" {
		t.Fatalf("plan name = %q", p.Changes[0].Name)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	if after["action"] != "drop" || after["ruleset"] != "GUEST_IN" {
		t.Fatalf("after: %+v", after)
	}

	got, err := svc.ApplyCreate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fw-new" && got.Name != "Drop Guest" {
		t.Fatalf("created: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPost {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/firewallrule" {
		t.Fatalf("path = %q", last.path)
	}
	body, _ := last.body.(map[string]any)
	if body["name"] != "Drop Guest" || body["action"] != "drop" {
		t.Fatalf("body: %+v", body)
	}
	if body["ruleset"] != "GUEST_IN" {
		t.Fatalf("body ruleset: %+v", body)
	}
}

func TestFirewallUpdatePlanAndApply(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
	ctx := context.Background()

	in := domain.FirewallInput{
		Name:       "Allow LAN DNS v2",
		Action:     "accept",
		SetEnabled: true,
		Enabled:    false,
	}
	p, rec, err := svc.Update(ctx, "fw1", in)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "Allow LAN DNS" {
		t.Fatalf("preview: %+v", rec)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" || p.Changes[0].ID != "fw1" {
		t.Fatalf("plan: %+v", p)
	}
	before, _ := p.Changes[0].Before.(map[string]any)
	after, _ := p.Changes[0].After.(map[string]any)
	if before["name"] != "Allow LAN DNS" || after["name"] != "Allow LAN DNS v2" {
		t.Fatalf("before/after: %+v %+v", before, after)
	}
	if after["enabled"] != false {
		t.Fatalf("after enabled: %+v", after["enabled"])
	}

	got, err := svc.ApplyUpdate(ctx, "fw1", in)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fw1" {
		t.Fatalf("updated: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodPut {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/firewallrule/fw1" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestFirewallDeletePlanAndApply(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
	ctx := context.Background()

	p, rec, err := svc.Delete(ctx, "Allow LAN DNS")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "fw1" {
		t.Fatalf("record: %+v", rec)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "delete" {
		t.Fatalf("plan: %+v", p)
	}

	got, err := svc.ApplyDelete(ctx, "Allow LAN DNS")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "fw1" {
		t.Fatalf("deleted: %+v", got)
	}
	last := api.calls[len(api.calls)-1]
	if last.method != http.MethodDelete {
		t.Fatalf("method = %q", last.method)
	}
	if last.path != "/proxy/network/api/s/default/rest/firewallrule/fw1" {
		t.Fatalf("path = %q", last.path)
	}
}

func TestFirewallReorderByIDs(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
	ctx := context.Background()

	// New order: fw2, fw1, fw3
	ids := []string{"fw2", "fw1", "fw3"}
	p, err := svc.Reorder(ctx, domain.FirewallReorder{IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].Op != "update" {
		t.Fatalf("plan: %+v", p)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	order, _ := after["order"].([]string)
	if len(order) != 3 || order[0] != "fw2" || order[1] != "fw1" || order[2] != "fw3" {
		// allow []any
		if arr, ok := after["order"].([]any); ok {
			if len(arr) != 3 || arr[0] != "fw2" || arr[1] != "fw1" || arr[2] != "fw3" {
				t.Fatalf("after order: %+v", after["order"])
			}
		} else if len(order) != 3 {
			t.Fatalf("after order: %+v", after["order"])
		}
	}

	if err := svc.ApplyReorder(ctx, domain.FirewallReorder{IDs: ids}); err != nil {
		t.Fatal(err)
	}
	// Expect three PUTs updating rule_index
	puts := 0
	for _, c := range api.calls {
		if c.method == http.MethodPut && strings.Contains(c.path, "rest/firewallrule/") {
			puts++
			body, _ := c.body.(map[string]any)
			if _, ok := body["rule_index"]; !ok {
				t.Fatalf("put body missing rule_index: %+v path=%s", body, c.path)
			}
		}
	}
	if puts != 3 {
		t.Fatalf("puts = %d, want 3; calls=%+v", puts, api.calls)
	}
}

func TestFirewallReorderByIndex(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
	ctx := context.Background()

	// Move fw3 to index 0 (first among the three)
	p, err := svc.Reorder(ctx, domain.FirewallReorder{ID: "fw3", Index: 0, SetIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := p.Changes[0].After.(map[string]any)
	// Expected full order: fw3, fw1, fw2 (sorted by original index, then move)
	var order []string
	switch v := after["order"].(type) {
	case []string:
		order = v
	case []any:
		for _, el := range v {
			order = append(order, el.(string))
		}
	default:
		t.Fatalf("order type %T: %+v", after["order"], after["order"])
	}
	if len(order) != 3 || order[0] != "fw3" {
		t.Fatalf("order after move to 0: %+v", order)
	}

	if err := svc.ApplyReorder(ctx, domain.FirewallReorder{ID: "fw3", Index: 0, SetIndex: true}); err != nil {
		t.Fatal(err)
	}
}

func TestFirewallReorderValidation(t *testing.T) {
	api := &fakeFirewallAPI{rules: fixtureFirewallRules(t)}
	svc := domain.NewFirewallService(api)
	ctx := context.Background()

	_, err := svc.Reorder(ctx, domain.FirewallReorder{})
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("empty reorder err = %v", err)
	}

	_, err = svc.Reorder(ctx, domain.FirewallReorder{IDs: []string{"fw1", "missing"}})
	if !apperr.Is(err, apperr.NotFound) && !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatalf("bad id err = %v", err)
	}
}
