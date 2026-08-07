package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

const (
	integrationBasePath = "/proxy/network/integration/v1"
	officialPageSize    = 100
	maxOfficialItems    = 100000
	maxOfficialPages    = maxOfficialItems / officialPageSize
)

// OfficialPage is the complete pagination envelope returned by the official
// local Network API.
type OfficialPage[T any] struct {
	Offset     int `json:"offset"`
	Limit      int `json:"limit"`
	Count      int `json:"count"`
	TotalCount int `json:"totalCount"`
	Data       []T `json:"data"`
}

type officialPageWire[T any] struct {
	Offset     *int `json:"offset"`
	Limit      *int `json:"limit"`
	Count      *int `json:"count"`
	TotalCount *int `json:"totalCount"`
	Data       *[]T `json:"data"`
}

type integrationSite struct {
	ID                string `json:"id"`
	InternalReference string `json:"internalReference"`
	Name              string `json:"name"`
}

// OfficialPath builds a local Network API path and escapes every supplied
// dynamic segment independently.
func OfficialPath(parts ...string) string {
	path := integrationBasePath
	for _, part := range parts {
		if part == "" {
			continue
		}
		path += "/" + url.PathEscape(part)
	}
	return path
}

// FetchOfficialAll fetches complete typed collections from the official local
// Network API. It always requests 100 items and advances only by the metadata
// returned by a well-formed, progressing page.
func FetchOfficialAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	offset := 0
	expectedTotal := -1
	pages := 0
	var all []T

	for {
		if pages >= maxOfficialPages {
			return nil, apperr.Newf(apperr.Internal, "official API pagination exceeds maximum of %d pages", maxOfficialPages)
		}
		pages++
		pagePath, err := officialPagePath(path, offset)
		if err != nil {
			return nil, apperr.Newf(apperr.Internal, "build official API page query: %v", err)
		}
		var page officialPageWire[T]
		if err := c.DoOfficial(ctx, http.MethodGet, pagePath, nil, &page); err != nil {
			return nil, err
		}
		if err := validateOfficialPage(page, offset); err != nil {
			return nil, err
		}

		pageOffset := *page.Offset
		pageCount := *page.Count
		totalCount := *page.TotalCount
		if expectedTotal == -1 {
			expectedTotal = totalCount
		} else if totalCount != expectedTotal {
			return nil, apperr.Newf(apperr.Internal, "official API totalCount changed from %d to %d", expectedTotal, totalCount)
		}
		if len(all) > maxOfficialItems-pageCount {
			return nil, apperr.Newf(apperr.Internal, "official API items exceed maximum of %d", maxOfficialItems)
		}
		all = append(all, (*page.Data)...)

		nextOffset := pageOffset + pageCount
		if nextOffset >= totalCount {
			return all, nil
		}
		if nextOffset <= offset {
			return nil, apperr.Newf(apperr.Internal, "official API page at offset %d did not advance", offset)
		}
		offset = nextOffset
	}
}

// FetchOfficialObjects exposes the paginated official collection reader to
// domain services that normalize API objects into their public DTOs.
func (c *Client) FetchOfficialObjects(ctx context.Context, path string) ([]map[string]any, error) {
	return FetchOfficialAll[map[string]any](ctx, c, path)
}

func officialPagePath(path string, offset int) (string, error) {
	u, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	if u.IsAbs() || u.Host != "" || u.Path == "" {
		return "", fmt.Errorf("path must be controller-relative")
	}
	query := u.Query()
	query.Set("limit", strconv.Itoa(officialPageSize))
	query.Set("offset", strconv.Itoa(offset))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func validateOfficialPage[T any](page officialPageWire[T], expectedOffset int) error {
	if page.Offset == nil || page.Limit == nil || page.Count == nil || page.TotalCount == nil || page.Data == nil {
		return apperr.New(apperr.Internal, "official API page is missing required pagination fields")
	}
	offset := *page.Offset
	limit := *page.Limit
	count := *page.Count
	totalCount := *page.TotalCount
	if offset < 0 || limit <= 0 || count < 0 || totalCount < 0 {
		return apperr.New(apperr.Internal, "official API page contains invalid negative or zero pagination values")
	}
	if totalCount > maxOfficialItems {
		return apperr.Newf(apperr.Internal, "official API totalCount %d exceeds maximum of %d", totalCount, maxOfficialItems)
	}
	if offset != expectedOffset {
		return apperr.Newf(apperr.Internal, "official API page offset %d does not match requested offset %d", offset, expectedOffset)
	}
	if count > limit {
		return apperr.Newf(apperr.Internal, "official API page count %d exceeds returned limit %d", count, limit)
	}
	if limit != officialPageSize {
		return apperr.Newf(apperr.Internal, "official API page limit %d does not match requested limit %d", limit, officialPageSize)
	}
	if count != len(*page.Data) {
		return apperr.Newf(apperr.Internal, "official API page count %d does not match data length %d", count, len(*page.Data))
	}
	if offset > totalCount || count > totalCount-offset {
		return apperr.Newf(apperr.Internal, "official API page range %d exceeds totalCount %d", offset+count, totalCount)
	}
	if offset+count < totalCount && count == 0 {
		return apperr.Newf(apperr.Internal, "official API page at offset %d did not advance", offset)
	}
	return nil
}

// IntegrationSitePath resolves the configured site selector to the UUID
// required by the official local Network API and caches successful resolution
// for this Client instance.
func (c *Client) IntegrationSitePath(ctx context.Context, parts ...string) (string, error) {
	c.integrationSiteMu.Lock()
	defer c.integrationSiteMu.Unlock()

	if c.integrationSiteID == "" {
		sites, err := FetchOfficialAll[integrationSite](ctx, c, OfficialPath("sites"))
		if err != nil {
			return "", err
		}
		matches := make(map[string]struct{})
		for _, site := range sites {
			if site.ID != c.cfg.Site && site.InternalReference != c.cfg.Site && site.Name != c.cfg.Site {
				continue
			}
			if site.ID == "" {
				return "", apperr.New(apperr.Internal, "official API site match has no ID")
			}
			matches[site.ID] = struct{}{}
		}
		switch len(matches) {
		case 0:
			return "", apperr.Newf(apperr.NotFound, "site %q is unavailable through the official Network API", c.cfg.Site)
		case 1:
			for id := range matches {
				c.integrationSiteID = id
			}
		default:
			return "", apperr.Newf(apperr.AmbiguousID, "site selector %q matches multiple sites", c.cfg.Site)
		}
	}

	pathParts := make([]string, 0, len(parts)+2)
	pathParts = append(pathParts, "sites", c.integrationSiteID)
	pathParts = append(pathParts, parts...)
	return OfficialPath(pathParts...), nil
}
