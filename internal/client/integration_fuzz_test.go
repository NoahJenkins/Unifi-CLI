package client

import (
	"encoding/json"
	"net/url"
	"testing"
)

func FuzzOfficialPaginationJSON(f *testing.F) {
	f.Add([]byte(`{"offset":0,"limit":100,"count":1,"totalCount":1,"data":[{"id":"one"}]}`), 0)
	f.Add([]byte(`{"offset":0,"limit":100,"count":0,"totalCount":1,"data":[]}`), 0)
	f.Add([]byte(`null`), 0)
	f.Fuzz(func(t *testing.T, data []byte, expectedOffset int) {
		if len(data) > maxOfficialBytes || expectedOffset < 0 || expectedOffset > maxOfficialItems {
			t.Skip()
		}
		var page officialPageWire[map[string]any]
		if err := json.Unmarshal(data, &page); err == nil {
			_ = validateOfficialPage(page, expectedOffset)
		}
	})
}

func FuzzOfficialPathAndFilterEscaping(f *testing.F) {
	for _, seed := range []string{"default", "../admin", "name with spaces", "a/b?offset=900", "\x00"} {
		f.Add(seed, seed)
	}
	f.Fuzz(func(t *testing.T, segment, filter string) {
		if len(segment)+len(filter) > 4096 {
			t.Skip()
		}
		path := OfficialPath("sites", segment, "resources")
		withFilter := path + "?filter=" + url.QueryEscape(filter)
		page, err := officialPagePath(withFilter, 100)
		if err != nil {
			return
		}
		parsed, err := url.Parse(page)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Query().Get("offset") != "100" || parsed.Query().Get("filter") != filter {
			t.Fatalf("unsafe official path: %q parsed=%#v err=%v", page, parsed, err)
		}
	})
}
