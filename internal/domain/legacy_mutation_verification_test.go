package domain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/domain"
)

type legacyVerificationAPI struct {
	items []map[string]any
	calls []mutateCall
}

func (f *legacyVerificationAPI) SitePath(parts ...string) string {
	return "/proxy/network/api/s/default/" + strings.Join(parts, "/")
}

func (f *legacyVerificationAPI) Do(_ context.Context, method, path string, in, out any) error {
	f.calls = append(f.calls, mutateCall{method: method, path: path, body: in})
	if method == http.MethodGet {
		b, _ := json.Marshal(f.items)
		return json.Unmarshal(b, out)
	}
	return nil
}

func TestLegacyClientObservableMutationRejectsPostWriteMismatch(t *testing.T) {
	api := &legacyVerificationAPI{items: []map[string]any{{"_id": "client-1", "mac": "00:11:22:33:44:55", "name": "Laptop", "blocked": false}}}
	_, err := domain.NewClientService(api).ApplyBlock(context.Background(), "client-1")
	if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("error = %v, want verification conflict", err)
	}
	posts := 0
	for _, call := range api.calls {
		if call.method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("POST count = %d, want 1", posts)
	}
}

func TestLegacyClientReconnectReportsAcceptance(t *testing.T) {
	api := &legacyVerificationAPI{items: []map[string]any{{"_id": "client-1", "mac": "00:11:22:33:44:55", "name": "Laptop"}}}
	got, err := domain.NewClientService(api).ApplyReconnect(context.Background(), "client-1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"accepted":true}` {
		t.Fatalf("action data = %s", b)
	}
}

func TestLegacyPortAndResolverRejectPostWriteMismatch(t *testing.T) {
	t.Run("port", func(t *testing.T) {
		api := &fakePortAPI{devices: devicesWithPorts(), ignorePuts: true}
		_, err := domain.NewPortService(api).ApplyUpdate(context.Background(), "Switch-Core", 12, domain.PortInput{POE: "off", SetPOE: true})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("error = %v, want verification conflict", err)
		}
		puts := 0
		for _, call := range api.calls {
			if call.method == http.MethodPut {
				puts++
			}
		}
		if puts != 1 {
			t.Fatalf("PUT count = %d, want 1", puts)
		}
	})

	t.Run("resolver", func(t *testing.T) {
		api := &fakeDNSAPI{networks: fixtureNetworks(t), ignoreResolverPuts: true}
		_, err := domain.NewDNSService(api).ApplySetResolvers(context.Background(), "LAN", []string{"1.1.1.1"})
		if !apperr.Is(err, apperr.Conflict) || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("error = %v, want verification conflict", err)
		}
		puts := 0
		for _, call := range api.calls {
			if call.method == http.MethodPut {
				puts++
			}
		}
		if puts != 1 {
			t.Fatalf("PUT count = %d, want 1", puts)
		}
	})
}
