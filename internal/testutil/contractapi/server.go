package contractapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const BasePath = "/proxy/network/integration/v1"

// Faults are explicit test conditions, never normal modeled controller behavior.
type Fault struct{ Resource, Kind string }
type Server struct {
	HTTP              *httptest.Server
	mu                sync.Mutex
	t                 testing.TB
	profile           Profile
	validators        []*Validator
	objects           map[string]map[string]map[string]any
	attempts, created int
	fault             Fault
	reads             map[string]int
}

func NewServer(t testing.TB, profile Profile, validator *Validator) *Server {
	t.Helper()
	if validator == nil || validator.profile != profile {
		t.Fatal("invalid emulator validator")
	}
	offline, err := NewValidator(profile, "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t, profile: profile, validators: []*Validator{offline}, objects: map[string]map[string]map[string]any{}, reads: map[string]int{}}
	if validator.engine != nil {
		s.validators = append(s.validators, validator)
	}
	for resource := range resourceSchemas {
		s.objects[resource] = map[string]map[string]any{}
	}
	s.HTTP = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.HTTP.Close)
	return s
}
func (s *Server) valid(resource string, response bool, doc map[string]any) error {
	for _, v := range s.validators {
		if err := v.Validate(resource, response, doc); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) Seed(resource string, doc map[string]any) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.valid(resource, true, doc); err != nil {
		s.t.Fatalf("invalid synthetic seed: %v", err)
	}
	id, _ := doc["id"].(string)
	s.objects[resource][id] = Clone(doc)
}
func (s *Server) Snapshot(resource, id string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.objects[resource][id]
	if doc == nil {
		return nil
	}
	return Clone(doc)
}
func (s *Server) Attempts() int { s.mu.Lock(); defer s.mu.Unlock(); return s.attempts }
func (s *Server) SetFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
	s.reads = map[string]int{}
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func rejected(w http.ResponseWriter, status int) {
	reply(w, status, map[string]any{"message": "synthetic contract request rejected"})
}
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("X-API-Key") != "synthetic-test-key" {
		rejected(w, 401)
		return
	}
	if r.Method != "GET" {
		s.attempts++
	}
	if r.Method == "GET" && r.URL.Path == BasePath+"/info" {
		reply(w, 200, map[string]any{"applicationVersion": s.profile.Version})
		return
	}
	if r.Method == "GET" && r.URL.Path == BasePath+"/sites" {
		s.page(w, r, []map[string]any{{"id": SiteID, "internalReference": "default", "name": "Synthetic site"}})
		return
	}
	prefix := BasePath + "/sites/" + SiteID + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		s.unexpected(w)
		return
	}
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	if tail == "firewall/zones" || strings.HasPrefix(tail, "firewall/zones/") {
		if r.Method != "GET" {
			s.unexpected(w)
			return
		}
		zones := []map[string]any{{"id": SourceZoneID, "name": "Internal", "networkIds": []any{NetworkID}, "metadata": metadata()}, {"id": DestinationZoneID, "name": "External", "networkIds": []any{}, "metadata": metadata()}}
		if tail == "firewall/zones" {
			s.page(w, r, zones)
			return
		}
		for _, zone := range zones {
			if strings.TrimPrefix(tail, "firewall/zones/") == zone["id"] {
				reply(w, 200, zone)
				return
			}
		}
		rejected(w, 404)
		return
	}
	var resource, id string
	for kind, route := range map[string]string{"firewall": "firewall/policies", "network": "networks", "wlan": "wifi/broadcasts"} {
		if tail == route {
			resource = kind
			break
		}
		if strings.HasPrefix(tail, route+"/") {
			resource = kind
			id = strings.TrimPrefix(tail, route+"/")
			break
		}
	}
	if resource == "" || strings.Contains(id, "/") {
		s.unexpected(w)
		return
	}
	objects := s.objects[resource]
	if r.Method == "GET" {
		if id == "" {
			s.reads[resource+"/collection"]++
			if resource == "firewall" && s.fault.Resource == resource && s.fault.Kind == "drift" && s.reads[resource+"/collection"] == 2 {
				for _, doc := range objects {
					doc["name"] = "Concurrent rename"
				}
			}
			keys := make([]string, 0, len(objects))
			for key := range objects {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			items := make([]map[string]any, 0, len(keys))
			for _, key := range keys {
				items = append(items, Clone(objects[key]))
			}
			s.page(w, r, items)
			return
		}
		doc := objects[id]
		if doc == nil {
			rejected(w, 404)
			return
		}
		s.reads[resource+id]++
		if s.fault.Resource == resource && s.fault.Kind == "drift" && s.reads[resource+id] == 2 {
			doc["name"] = "Concurrent rename"
		}
		if err := s.valid(resource, true, doc); err != nil {
			s.t.Errorf("stored response invalid: %v", err)
			rejected(w, 500)
			return
		}
		response := Clone(doc)
		if s.fault.Resource == resource {
			if s.fault.Kind == "mismatch-readback" && s.attempts > 0 {
				response["name"] = "Unexpected observed name"
			}
			if s.fault.Kind == "omit-mdns" && s.attempts == 0 {
				delete(response, "mdnsForwardingEnabled")
			}
		}
		reply(w, 200, response)
		return
	}
	if r.Method == "DELETE" && resource == "firewall" && id != "" {
		if objects[id] == nil {
			rejected(w, 404)
			return
		}
		delete(objects, id)
		reply(w, 200, map[string]any{})
		return
	}
	if (r.Method != "PUT" || id == "") && (r.Method != "POST" || resource != "firewall" || id != "") {
		s.unexpected(w)
		return
	}
	if r.Method == "PUT" && objects[id] == nil {
		rejected(w, 404)
		return
	}
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if decoder.Decode(&body) != nil || body == nil {
		rejected(w, 400)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		rejected(w, 400)
		return
	}
	if err := s.valid(resource, false, body); err != nil {
		rejected(w, 400)
		return
	}
	result := Clone(body)
	status := 200
	if r.Method == "POST" {
		status = 201
		for {
			id = PolicyID
			if s.created > 0 {
				id = fmt.Sprintf("44444444-4444-4444-8444-%012d", s.created)
			}
			if objects[id] == nil {
				break
			}
			s.created++
		}
		result["id"] = id
		result["metadata"] = metadata()
		result["index"] = float64(s.created)
	} else {
		for _, field := range []string{"id", "metadata", "default", "index"} {
			if value, ok := objects[id][field]; ok {
				result[field] = value
			}
		}
	}
	if err := s.valid(resource, true, result); err != nil {
		rejected(w, 500)
		return
	}
	objects[id] = Clone(result)
	if status == 201 {
		s.created++
	}
	if s.fault.Resource == resource && s.fault.Kind == "corrupt-write-response" {
		s.fault = Fault{}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "{")
		return
	}
	reply(w, status, result)
}
func (s *Server) unexpected(w http.ResponseWriter) {
	s.t.Errorf("unexpected emulator method or route")
	rejected(w, 404)
}
func (s *Server) page(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	offset, limit := 0, 100
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			rejected(w, 400)
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			rejected(w, 400)
			return
		}
	}
	start := min(offset, len(items))
	end := start + min(limit, len(items)-start)
	reply(w, 200, map[string]any{"offset": offset, "limit": limit, "count": end - start, "totalCount": len(items), "data": items[start:end]})
}
