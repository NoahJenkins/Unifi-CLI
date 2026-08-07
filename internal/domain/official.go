package domain

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

// wireDocumentsEqual compares JSON-shaped controller documents after a JSON
// round trip so integer inputs and decoded JSON numbers have the same meaning.
func wireDocumentsEqual(a, b any) bool {
	return wireDocumentsEqualAtPaths(a, b, nil)
}

func wireDocumentsEqualAtPaths(a, b any, setPaths map[string]struct{}) bool {
	left, leftOK := normalizeWireDocument(a, "", setPaths)
	right, rightOK := normalizeWireDocument(b, "", setPaths)
	return leftOK && rightOK && reflect.DeepEqual(left, right)
}

func wireDocumentContains(observed, requested any, setPaths map[string]struct{}) bool {
	actual, actualOK := normalizeWireDocument(observed, "", setPaths)
	want, wantOK := normalizeWireDocument(requested, "", setPaths)
	return actualOK && wantOK && wireValueContains(actual, want)
}

func normalizeWireDocument(value any, path string, setPaths map[string]struct{}) (any, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, false
	}
	return normalizeWireValue(normalized, path, setPaths), true
}

func normalizeWireValue(value any, path string, setPaths map[string]struct{}) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			out[key] = normalizeWireValue(item, childPath, setPaths)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		childPath := "*"
		if path != "" {
			childPath = path + ".*"
		}
		for i, item := range typed {
			out[i] = normalizeWireValue(item, childPath, setPaths)
		}
		if _, isSet := setPaths[path]; isSet {
			sort.Slice(out, func(i, j int) bool {
				left, _ := json.Marshal(out[i])
				right, _ := json.Marshal(out[j])
				return string(left) < string(right)
			})
		}
		return out
	default:
		return typed
	}
}

func wireValueContains(observed, requested any) bool {
	switch want := requested.(type) {
	case map[string]any:
		actual, ok := observed.(map[string]any)
		if !ok {
			return false
		}
		for key, expectedValue := range want {
			actualValue, exists := actual[key]
			if !exists || !wireValueContains(actualValue, expectedValue) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(observed, requested)
	}
}

type officialCollectionAPI interface {
	FetchOfficialObjects(context.Context, string) ([]map[string]any, error)
}

type officialSiteCollectionAPI interface {
	officialCollectionAPI
	IntegrationSitePath(context.Context, ...string) (string, error)
}

type officialDetailAPI interface {
	DoOfficial(context.Context, string, string, any, any) error
}

type officialMutationAPI interface {
	officialSiteCollectionAPI
	officialDetailAPI
}

const officialDetailConcurrencyLimit = 4

func fetchOfficialGlobal(api any, ctx context.Context, parts ...string) ([]map[string]any, bool, error) {
	fetcher, ok := api.(officialCollectionAPI)
	if !ok {
		return nil, false, nil
	}
	items, err := fetcher.FetchOfficialObjects(ctx, client.OfficialPath(parts...))
	return items, true, err
}

func fetchOfficialSite(api any, ctx context.Context, parts ...string) ([]map[string]any, bool, error) {
	fetcher, ok := api.(officialSiteCollectionAPI)
	if !ok {
		return nil, false, nil
	}
	path, err := fetcher.IntegrationSitePath(ctx, parts...)
	if err != nil {
		return nil, true, err
	}
	items, err := fetcher.FetchOfficialObjects(ctx, path)
	return items, true, err
}

func fetchOfficialSiteDetail(api any, ctx context.Context, id string, parts ...string) (map[string]any, error) {
	fetcher, collectionOK := api.(officialSiteCollectionAPI)
	detailer, detailOK := api.(officialDetailAPI)
	if !collectionOK || !detailOK {
		return nil, apperr.New(apperr.Internal, "official resource detail transport is unavailable")
	}
	pathParts := append(append([]string(nil), parts...), id)
	path, err := fetcher.IntegrationSitePath(ctx, pathParts...)
	if err != nil {
		return nil, err
	}
	var item map[string]any
	if err := detailer.DoOfficial(ctx, http.MethodGet, path, nil, &item); err != nil {
		return nil, err
	}
	return item, nil
}

func fetchOfficialSiteDetails(ctx context.Context, api any, overviews []map[string]any, parts ...string) ([]map[string]any, error) {
	if len(overviews) == 0 {
		return []map[string]any{}, nil
	}
	for _, overview := range overviews {
		if strField(overview, "id") == "" {
			return nil, apperr.New(apperr.Internal, "official resource overview is missing an ID")
		}
	}

	type detailJob struct {
		index int
		id    string
	}
	// The official API exposes several stable fields only on detail routes.
	// Keep fan-out small and fixed; one failed detail cancels the batch and the
	// caller receives no partial result.
	workerCount := officialDetailConcurrencyLimit
	if len(overviews) < workerCount {
		workerCount = len(overviews)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan detailJob)
	results := make([]map[string]any, len(overviews))
	var workers sync.WaitGroup
	var firstErr error
	var errOnce sync.Once

	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-workCtx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					detail, err := fetchOfficialSiteDetail(api, workCtx, job.id, parts...)
					if err != nil {
						errOnce.Do(func() {
							firstErr = err
							cancel()
						})
						return
					}
					results[job.index] = detail
				}
			}
		}()
	}

sendJobs:
	for index, overview := range overviews {
		job := detailJob{index: index, id: strField(overview, "id")}
		select {
		case <-workCtx.Done():
			break sendJobs
		case jobs <- job:
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func supportsOfficialDetails(api any) bool {
	_, collectionOK := api.(officialSiteCollectionAPI)
	_, detailOK := api.(officialDetailAPI)
	return collectionOK && detailOK
}

func requireOfficialMutationAPI(api any) (officialMutationAPI, error) {
	transport, ok := api.(officialMutationAPI)
	if !ok {
		return nil, apperr.New(apperr.Internal, "official mutation transport is unavailable")
	}
	return transport, nil
}

func deepCloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = deepCloneValue(value)
	}
	return out
}

func deepCloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return deepCloneMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = deepCloneValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}

func resolveLegacyMutationTarget[T resolve.Identifiable](legacy, official []T, query, resource string, sameIdentity func(T, T) bool) (T, error) {
	var zero T
	if item, ok := findExactID(legacy, query); ok {
		return item, nil
	}
	if !looksLikeUUID(query) {
		return resolve.One(legacy, query)
	}

	var officialTarget T
	foundOfficial := false
	for _, item := range official {
		if item.GetID() == query {
			officialTarget = item
			foundOfficial = true
			break
		}
	}
	if !foundOfficial {
		return resolve.One(legacy, query)
	}

	matches := make([]T, 0, 1)
	for _, item := range legacy {
		if sameIdentity(officialTarget, item) {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return zero, apperr.Newf(apperr.NotFound, "official %s %q has no matching legacy object", resource, query)
	default:
		return zero, apperr.Newf(apperr.AmbiguousID, "official %s %q matches multiple legacy objects", resource, query)
	}
}

func findExactID[T resolve.Identifiable](items []T, id string) (T, bool) {
	var zero T
	for _, item := range items {
		if item.GetID() == id {
			return item, true
		}
	}
	return zero, false
}

func sameMAC(a, b resolve.Identifiable) bool {
	return a.GetMAC() != "" && b.GetMAC() != "" && resolve.NormalizeMAC(a.GetMAC()) == resolve.NormalizeMAC(b.GetMAC())
}

func sameName(a, b resolve.Identifiable) bool {
	return a.GetName() != "" && a.GetName() == b.GetName()
}

func looksLikeUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range strings.ToLower(value) {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return false
			}
		}
	}
	return true
}
