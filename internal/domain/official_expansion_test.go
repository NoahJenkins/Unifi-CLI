package domain_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

func TestSwitchingOfficialListAndGet(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		parts      []string
		query      string
		listAndGet func(*domain.SwitchingService) (any, any, error)
	}{
		{
			name: "LAG", fixture: "switching-lags.json", parts: []string{"switching", "lags"}, query: "10000000-0000-4000-8000-000000000001",
			listAndGet: func(s *domain.SwitchingService) (any, any, error) {
				items, err := s.ListLAGs(context.Background())
				if err != nil {
					return nil, nil, err
				}
				item, err := s.GetLAG(context.Background(), "10000000-0000-4000-8000-000000000001")
				return items, item, err
			},
		},
		{
			name: "MC-LAG", fixture: "switching-mc-lag-domains.json", parts: []string{"switching", "mc-lag-domains"}, query: "Core",
			listAndGet: func(s *domain.SwitchingService) (any, any, error) {
				items, err := s.ListMCLAGDomains(context.Background())
				if err != nil {
					return nil, nil, err
				}
				item, err := s.GetMCLAGDomain(context.Background(), "Core")
				return items, item, err
			},
		},
		{
			name: "switch stack", fixture: "switching-switch-stacks.json", parts: []string{"switching", "switch-stacks"}, query: "Stack A",
			listAndGet: func(s *domain.SwitchingService) (any, any, error) {
				items, err := s.ListSwitchStacks(context.Background())
				if err != nil {
					return nil, nil, err
				}
				item, err := s.GetSwitchStack(context.Background(), "Stack A")
				return items, item, err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := officialFixtureData(t, tt.fixture)
			overview := items[0]
			collectionPath := client.OfficialPath(append([]string{"sites", officialSiteID}, tt.parts...)...)
			detailPath := collectionPath + "/" + overview["id"].(string)
			api := &officialReadAPI{collections: map[string][]map[string]any{collectionPath: items}, details: map[string]map[string]any{detailPath: overview}, errs: map[string]error{}}
			list, got, err := tt.listAndGet(domain.NewSwitchingService(api))
			if err != nil {
				t.Fatal(err)
			}
			if reflect.ValueOf(list).Len() != 1 || reflect.ValueOf(got).FieldByName("ID").String() != overview["id"] {
				t.Fatalf("list=%#v get=%#v", list, got)
			}
			for _, call := range api.calls {
				if strings.Contains(call, "LEGACY") {
					t.Fatalf("stable read used legacy API: %s", call)
				}
			}
		})
	}
}

func TestRadiusProfilesOfficialListAndGet(t *testing.T) {
	path := client.OfficialPath("sites", officialSiteID, "radius", "profiles")
	api := &officialReadAPI{collections: map[string][]map[string]any{path: officialFixtureData(t, "radius-profiles.json")}, errs: map[string]error{}}
	svc := domain.NewRadiusService(api)
	items, err := svc.ListProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	item, err := svc.GetProfile(context.Background(), "Corporate")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || item.ID != "40000000-0000-4000-8000-000000000004" || item.Origin != "USER" {
		t.Fatalf("items=%#v item=%#v", items, item)
	}
}
