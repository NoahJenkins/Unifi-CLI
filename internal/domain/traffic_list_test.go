package domain_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func TestTrafficListFixtureBackedOfficialListAndGet(t *testing.T) {
	collection := client.OfficialPath("sites", mutationSiteID, "traffic-matching-lists")
	items := officialFixtureData(t, "traffic-matching-lists.json")
	id := items[0]["id"].(string)
	detail := collection + "/" + id
	api := &officialMutationAPI{collections: map[string][]map[string]any{collection: items}, details: map[string]map[string]any{detail: items[0]}}
	svc := domain.NewTrafficListService(api)
	listed, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(context.Background(), "Web ports")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || got.ID != id || got.Type != "PORTS" || len(got.Items) != 2 {
		t.Fatalf("listed=%#v got=%#v", listed, got)
	}
	if len(api.legacy) != 0 {
		t.Fatalf("stable traffic-list read used legacy API: %#v", api.legacy)
	}
}

func TestTrafficListCreateBuildsEveryOfficialType(t *testing.T) {
	for _, tt := range []struct {
		name     string
		in       domain.TrafficListInput
		wantBody map[string]any
	}{
		{name: "ports", in: domain.TrafficListInput{Type: "ports", Name: "Web ports", Items: []string{"80", "8000-8080"}}, wantBody: map[string]any{"type": "PORTS", "name": "Web ports", "items": []any{map[string]any{"type": "PORT_NUMBER", "value": 80.0}, map[string]any{"type": "PORT_NUMBER_RANGE", "start": 8000.0, "stop": 8080.0}}}},
		{name: "IPv4", in: domain.TrafficListInput{Type: "ipv4-addresses", Name: "IPv4", Items: []string{"192.0.2.1", "192.0.2.0/24", "192.0.2.10-192.0.2.20"}}, wantBody: map[string]any{"type": "IPV4_ADDRESSES", "name": "IPv4", "items": []any{map[string]any{"type": "IP_ADDRESS", "value": "192.0.2.1"}, map[string]any{"type": "SUBNET", "value": "192.0.2.0/24"}, map[string]any{"type": "IP_ADDRESS_RANGE", "start": "192.0.2.10", "stop": "192.0.2.20"}}}},
		{name: "IPv6", in: domain.TrafficListInput{Type: "ipv6-addresses", Name: "IPv6", Items: []string{"2001:db8::1", "2001:db8::/64"}}, wantBody: map[string]any{"type": "IPV6_ADDRESSES", "name": "IPv6", "items": []any{map[string]any{"type": "IP_ADDRESS", "value": "2001:db8::1"}, map[string]any{"type": "SUBNET", "value": "2001:db8::/64"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			collection := client.OfficialPath("sites", mutationSiteID, "traffic-matching-lists")
			id := "50000000-0000-4000-8000-000000000005"
			detail := collection + "/" + id
			observed := cloneMapWithID(t, tt.wantBody, id)
			api := &officialMutationAPI{collections: map[string][]map[string]any{collection: {}}, details: map[string]map[string]any{detail: observed}}
			api.mutate = func(method, path string, in, out any) error {
				if method == http.MethodPost {
					return copyTestJSON(map[string]any{"id": id}, out)
				}
				return nil
			}
			got, err := domain.NewTrafficListService(api).ApplyCreate(context.Background(), tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != id || len(api.official) != 2 || !reflect.DeepEqual(api.official[0].body, tt.wantBody) {
				t.Fatalf("got=%#v calls=%#v want body=%#v", got, api.official, tt.wantBody)
			}
		})
	}
}

func TestTrafficListCreatePlanIncludesCanonicalItems(t *testing.T) {
	svc := domain.NewTrafficListService(&officialMutationAPI{})
	got, err := svc.Create(context.Background(), domain.TrafficListInput{Type: "ports", Name: "Web", Items: []string{"443"}})
	if err != nil {
		t.Fatal(err)
	}
	after := got.Changes[0].After.(map[string]any)
	items := after["items"].([]domain.TrafficListItem)
	if len(items) != 1 || items[0].Type != "PORT_NUMBER" || items[0].Value != 443 {
		t.Fatalf("plan items = %#v", items)
	}
}

func TestTrafficListUpdatePreservesCompleteTypeAndVerifiesExactly(t *testing.T) {
	collection := client.OfficialPath("sites", mutationSiteID, "traffic-matching-lists")
	id := "50000000-0000-4000-8000-000000000005"
	detail := collection + "/" + id
	before := map[string]any{"id": id, "type": "PORTS", "name": "Web", "items": []any{map[string]any{"type": "PORT_NUMBER", "value": 80.0}}}
	after := map[string]any{"id": id, "type": "PORTS", "name": "Web and TLS", "items": []any{map[string]any{"type": "PORT_NUMBER", "value": 80.0}}}
	api := &officialMutationAPI{collections: map[string][]map[string]any{collection: {before}}, details: map[string]map[string]any{detail: before}}
	api.mutate = func(method, path string, in, out any) error {
		if method == http.MethodPut {
			api.details[detail] = after
		}
		return nil
	}
	got, err := domain.NewTrafficListService(api).ApplyUpdate(context.Background(), id, domain.TrafficListInput{Name: "Web and TLS", SetName: true})
	if err != nil {
		t.Fatal(err)
	}
	wantBody := map[string]any{"type": "PORTS", "name": "Web and TLS", "items": []any{map[string]any{"type": "PORT_NUMBER", "value": 80.0}}}
	if got.Name != "Web and TLS" || len(api.official) != 3 || !reflect.DeepEqual(api.official[1].body, wantBody) {
		t.Fatalf("got=%#v calls=%#v", got, api.official)
	}
}

func cloneMapWithID(t *testing.T, source map[string]any, id string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := copyTestJSON(source, &out); err != nil {
		t.Fatal(err)
	}
	out["id"] = id
	return out
}
