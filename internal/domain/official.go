package domain

import (
	"context"
	"net/http"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
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
