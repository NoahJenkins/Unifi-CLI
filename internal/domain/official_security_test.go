package domain

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
)

type detailBudgetAPI struct {
	requests      int
	response      map[string]any
	responseBytes int
	echoPathID    bool
}

func (f *detailBudgetAPI) DoOfficialSized(ctx context.Context, method, path string, in, out any) (int, error) {
	if err := f.DoOfficial(ctx, method, path, in, out); err != nil {
		return 0, err
	}
	return f.responseBytes, nil
}

func (f *detailBudgetAPI) IntegrationSitePath(_ context.Context, parts ...string) (string, error) {
	return client.OfficialPath(append([]string{"sites", "site-id"}, parts...)...), nil
}

func (f *detailBudgetAPI) FetchOfficialObjects(context.Context, string) ([]map[string]any, error) {
	return nil, nil
}

func (f *detailBudgetAPI) DoOfficial(_ context.Context, _ string, path string, _ any, out any) error {
	f.requests++
	if f.echoPathID {
		item := out.(*map[string]any)
		*item = map[string]any{"id": path[strings.LastIndex(path, "/")+1:]}
	} else if f.response != nil {
		item := out.(*map[string]any)
		*item = deepCloneMap(f.response)
	}
	return nil
}

func TestFetchOfficialSiteDetailsRejectsExcessiveFanoutBeforeRequests(t *testing.T) {
	api := &detailBudgetAPI{}
	overviews := make([]map[string]any, 1001)
	for i := range overviews {
		overviews[i] = map[string]any{"id": fmt.Sprintf("resource-%d", i)}
	}
	_, err := fetchOfficialSiteDetails(context.Background(), api, overviews, "devices")
	if !apperr.Is(err, apperr.Internal) || err == nil {
		t.Fatalf("error = %v, want typed fanout-budget failure", err)
	}
	if api.requests != 0 {
		t.Fatalf("detail requests = %d, want 0", api.requests)
	}
}

func TestFetchOfficialSiteDetailsRejectsMismatchedDetailIdentity(t *testing.T) {
	api := &detailBudgetAPI{response: map[string]any{"id": "wrong-resource"}}
	_, err := fetchOfficialSiteDetails(context.Background(), api, []map[string]any{{"id": "expected-resource"}}, "devices")
	if !apperr.Is(err, apperr.Internal) {
		t.Fatalf("error = %v, want typed identity failure", err)
	}
	if api.requests != 1 {
		t.Fatalf("detail requests = %d, want 1", api.requests)
	}
}

func TestFetchOfficialSiteDetailsChargesRawResponseBytes(t *testing.T) {
	api := &detailBudgetAPI{responseBytes: 12 << 20, echoPathID: true}
	overviews := make([]map[string]any, 3)
	for i := range overviews {
		overviews[i] = map[string]any{"id": fmt.Sprintf("resource-%d", i)}
	}
	// Return the requested ID while retaining a tiny decoded object. The fake's
	// raw response size models whitespace or duplicate-field padding.
	_, err := fetchOfficialSiteDetails(context.Background(), api, overviews, "devices")
	if !apperr.Is(err, apperr.Internal) || err == nil {
		t.Fatalf("error = %v, want raw aggregate-byte failure", err)
	}
}
