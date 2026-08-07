package domain

import (
	"context"
	"net/http"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

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

func supportsOfficialDetails(api any) bool {
	_, collectionOK := api.(officialSiteCollectionAPI)
	_, detailOK := api.(officialDetailAPI)
	return collectionOK && detailOK
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
