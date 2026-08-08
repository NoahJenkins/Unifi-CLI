package domain_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type systemOfficialAPI struct {
	info        map[string]any
	infoErr     error
	collections map[string][]map[string]any
	calls       []string
}

func (f *systemOfficialAPI) Do(_ context.Context, method, path string, _ any, out any) error {
	f.calls = append(f.calls, method+" "+path)
	if method != http.MethodGet || path != client.OfficialPath("info") {
		return errors.New("unexpected direct request")
	}
	if f.infoErr != nil {
		return f.infoErr
	}
	b, err := json.Marshal(f.info)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (*systemOfficialAPI) SitePath(parts ...string) string {
	return "/legacy/" + strings.Join(parts, "/")
}

func (*systemOfficialAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return client.OfficialPath(append([]string{"sites", officialSiteID}, parts...)...), nil
}

func (f *systemOfficialAPI) FetchOfficialObjects(_ context.Context, path string) ([]map[string]any, error) {
	f.calls = append(f.calls, "LIST "+path)
	return append([]map[string]any(nil), f.collections[path]...), nil
}

func TestSystemHealthIncludesOfficialApplicationVersionAndAdoptedDeviceStatus(t *testing.T) {
	api := &systemOfficialAPI{
		info: map[string]any{"applicationVersion": "10.4.57"},
		collections: map[string][]map[string]any{
			client.OfficialPath("sites", officialSiteID, "devices"): {
				{"id": officialGatewayID, "name": "Gateway", "state": "ONLINE", "supported": true},
				{"id": officialSwitchID, "name": "Switch", "state": "OFFLINE", "supported": true},
			},
			client.OfficialPath("sites", officialSiteID, "clients"): {
				{"id": officialWirelessID, "name": "Client", "type": "WIRELESS"},
			},
		},
	}
	h, err := domain.NewSystemService(api).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.ApplicationVersion != "10.4.57" {
		t.Fatalf("application_version = %q", h.ApplicationVersion)
	}
	if h.Status != "degraded" || h.DeviceTotal != 2 || h.DeviceConnected != 1 || h.ClientTotal != 1 {
		t.Fatalf("health = %+v", h)
	}
	if len(api.calls) != 3 || api.calls[0] != "GET "+client.OfficialPath("info") {
		t.Fatalf("calls = %v, want info before device and client collections", api.calls)
	}
}

func TestSystemHealthRejectsMissingOrInvalidOfficialApplicationVersion(t *testing.T) {
	for _, tt := range []struct {
		name string
		info map[string]any
	}{
		{name: "missing", info: map[string]any{}},
		{name: "non-string", info: map[string]any{"applicationVersion": 10.4}},
		{name: "empty", info: map[string]any{"applicationVersion": ""}},
		{name: "whitespace", info: map[string]any{"applicationVersion": "  "}},
		{name: "oversized", info: map[string]any{"applicationVersion": strings.Repeat("1", 129)}},
		{name: "control character", info: map[string]any{"applicationVersion": "10.4.57\nforged"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &systemOfficialAPI{info: tt.info, collections: map[string][]map[string]any{}}
			if _, err := domain.NewSystemService(api).Health(context.Background()); err == nil {
				t.Fatalf("invalid application info accepted: %#v", tt.info)
			}
			if len(api.calls) != 1 {
				t.Fatalf("invalid info continued to inventory calls: %v", api.calls)
			}
		})
	}
}

func TestSystemHealthPropagatesOfficialApplicationInfoFailure(t *testing.T) {
	want := errors.New("application info unavailable")
	api := &systemOfficialAPI{infoErr: want}
	if _, err := domain.NewSystemService(api).Health(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
