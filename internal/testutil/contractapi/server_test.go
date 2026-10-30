package contractapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	p, _ := ProfileFor("10.4.57")
	v, _ := NewValidator(p, "")
	return NewServer(t, p, v)
}
func request(t *testing.T, s *Server, method, path string, body []byte) (*http.Response, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, s.HTTP.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-API-Key", "synthetic-test-key")
	r.Header.Set("Content-Type", "application/json")
	res, err := s.HTTP.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, data
}
func TestServerReplacementAndInvalidWriteAtomicity(t *testing.T) {
	s := testServer(t)
	seed := NetworkSeed("GATEWAY")
	s.Seed("network", seed)
	path := BasePath + "/sites/" + SiteID + "/networks/" + NetworkID
	for _, raw := range []string{`{`, `{} {}`, `null`, `{"name":"bad"}`} {
		before := s.Snapshot("network", NetworkID)
		res, _ := request(t, s, "PUT", path, []byte(raw))
		if res.StatusCode != 400 {
			t.Fatal("bad request accepted")
		}
		if !reflect.DeepEqual(before, s.Snapshot("network", NetworkID)) {
			t.Fatal("invalid write changed state")
		}
	}
	body := Writable("network", seed)
	delete(body, "mdnsForwardingEnabled")
	body["name"] = "renamed"
	b, _ := json.Marshal(body)
	// Omission is legal in a request but required in a response. The server must
	// not silently recover fields from the previous writable document.
	res, _ := request(t, s, "PUT", path, b)
	if res.StatusCode != 500 {
		t.Fatal("unrepresentable response must fail closed")
	}
	if !reflect.DeepEqual(seed, s.Snapshot("network", NetworkID)) {
		t.Fatal("unrepresentable response committed")
	}
	body = Writable("network", seed)
	body["name"] = "renamed"
	delete(body["ipv4Configuration"].(map[string]any)["dhcpConfiguration"].(map[string]any), "domainName")
	b, _ = json.Marshal(body)
	res, _ = request(t, s, "PUT", path, b)
	if res.StatusCode != 200 {
		t.Fatal("valid update rejected")
	}
	got := s.Snapshot("network", NetworkID)
	if _, ok := got["ipv4Configuration"].(map[string]any)["dhcpConfiguration"].(map[string]any)["domainName"]; ok {
		t.Fatal("PUT merged old optional field")
	}
	expected := Clone(seed)
	expected["name"] = "renamed"
	if reflect.DeepEqual(expected, got) {
		t.Fatal("independent preservation comparison failed to catch omission")
	}
}
func TestServerFirewallLifecycleAndAmbiguousResponse(t *testing.T) {
	s := testServer(t)
	body := Writable("firewall", FirewallSeed())
	b, _ := json.Marshal(body)
	path := BasePath + "/sites/" + SiteID + "/firewall/policies"
	s.SetFault(Fault{Resource: "firewall", Kind: "corrupt-write-response"})
	res, data := request(t, s, "POST", path, b)
	if res.StatusCode != 201 || json.Valid(data) {
		t.Fatal("fault did not corrupt accepted response")
	}
	if s.Attempts() != 1 || s.Snapshot("firewall", PolicyID) == nil {
		t.Fatal("accepted write not stored exactly once")
	}
	res, _ = request(t, s, "DELETE", path+"/"+PolicyID, nil)
	if res.StatusCode != 200 || s.Snapshot("firewall", PolicyID) != nil {
		t.Fatal("delete failed")
	}
	res, _ = request(t, s, "GET", path+"/"+PolicyID, nil)
	if res.StatusCode != 404 {
		t.Fatal("deleted detail still present")
	}
}
func TestServerPaginationAndCopies(t *testing.T) {
	s := testServer(t)
	seed := NetworkSeed("UNMANAGED")
	s.Seed("network", seed)
	seed["name"] = "external mutation"
	other := NetworkSeed("UNMANAGED")
	other["id"] = OtherNetworkID
	s.Seed("network", other)
	path := BasePath + "/sites/" + SiteID + "/networks?offset=1&limit=1"
	res, b := request(t, s, "GET", path, nil)
	var page struct {
		Offset, Limit, Count, TotalCount int
		Data                             []map[string]any
	}
	_ = json.Unmarshal(b, &page)
	if res.StatusCode != 200 || page.Offset != 1 || page.Limit != 1 || page.Count != 1 || page.TotalCount != 2 {
		t.Fatal("bad pagination")
	}
	copy := s.Snapshot("network", NetworkID)
	if copy["name"] == "external mutation" {
		t.Fatal("seed shared state")
	}
	copy["name"] = "snapshot mutation"
	if s.Snapshot("network", NetworkID)["name"] == "snapshot mutation" {
		t.Fatal("snapshot shared state")
	}
}

func TestServerCreatePreservesSeed(t *testing.T) {
	s := testServer(t)
	seed := FirewallSeed()
	s.Seed("firewall", seed)
	body := Writable("firewall", seed)
	body["name"] = "New policy"
	raw, _ := json.Marshal(body)
	path := BasePath + "/sites/" + SiteID + "/firewall/policies"
	ids := map[string]bool{PolicyID: true}
	for range 2 {
		res, data := request(t, s, "POST", path, raw)
		var created map[string]any
		if res.StatusCode != 201 || json.Unmarshal(data, &created) != nil {
			t.Fatal("create failed")
		}
		id, _ := created["id"].(string)
		if id == "" || ids[id] {
			t.Fatal("create reused an existing identity")
		}
		ids[id] = true
		if !reflect.DeepEqual(seed, s.Snapshot("firewall", PolicyID)) {
			t.Fatal("create overwrote seed")
		}
	}
}
