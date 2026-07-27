package domain

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

// DNSAPI is the transport for local DNS records and networkconf resolvers.
// Endpoint discovery (dnsrecord vs future fallbacks) lives in DNSService.
type DNSAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

// DNSRecord is a local name→IP mapping.
type DNSRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Enabled bool   `json:"enabled"`
}

func (r DNSRecord) GetID() string   { return r.ID }
func (r DNSRecord) GetMAC() string  { return "" }
func (r DNSRecord) GetName() string { return r.Name }

// DNSResolver is per-network upstream/DHCP DNS servers.
type DNSResolver struct {
	NetworkID   string   `json:"network_id"`
	NetworkName string   `json:"network_name"`
	DNS         []string `json:"dns"`
	WAN         bool     `json:"wan"`
}

// DNSInput is create/update payload for local records.
type DNSInput struct {
	Name       string
	IP         string
	Enabled    bool
	SetEnabled bool // when true, Enabled is applied; otherwise preserve existing
}

type DNSService struct {
	api DNSAPI
}

func NewDNSService(api DNSAPI) *DNSService {
	return &DNSService{api: api}
}

func (s *DNSService) List(ctx context.Context) ([]DNSRecord, error) {
	raw, err := s.fetchRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DNSRecord, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeDNSRecord(m))
	}
	return out, nil
}

func (s *DNSService) Get(ctx context.Context, id string) (DNSRecord, error) {
	items, err := s.List(ctx)
	if err != nil {
		return DNSRecord{}, err
	}
	return resolve.One(items, id)
}

func (s *DNSService) Create(ctx context.Context, in DNSInput) (plan.Plan, error) {
	_ = ctx
	enabled := in.Enabled
	if !in.SetEnabled {
		enabled = true
	}
	p := plan.Create("dns", in.Name,
		fmt.Sprintf("create dns record %s → %s", in.Name, in.IP),
		dnsRecordSnapshot(DNSRecord{Name: in.Name, IP: in.IP, Enabled: enabled}),
	)
	return p, nil
}

func (s *DNSService) ApplyCreate(ctx context.Context, in DNSInput) (DNSRecord, error) {
	path := s.api.SitePath(client.PathRestDNSRecord)
	if !in.SetEnabled {
		in.Enabled = true
		in.SetEnabled = true
	}
	body := dnsInputBody(in)
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "create dns record")
	}
	if len(raw) > 0 {
		return NormalizeDNSRecord(raw[0]), nil
	}
	return DNSRecord{Name: in.Name, IP: in.IP, Enabled: in.Enabled}, nil
}

func (s *DNSService) Update(ctx context.Context, id string, in DNSInput) (plan.Plan, DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return plan.Plan{}, DNSRecord{}, err
	}
	before := dnsRecordSnapshot(rec)
	after := mergeDNSAfter(rec, in)
	p := plan.Update("dns", rec.ID, rec.Name,
		fmt.Sprintf("update dns record %s", rec.Name),
		before,
		after,
	)
	return p, rec, nil
}

func (s *DNSService) ApplyUpdate(ctx context.Context, id string, in DNSInput) (DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return DNSRecord{}, err
	}
	path := s.api.SitePath(client.PathRestDNSRecord, rec.ID)
	body := dnsInputBodyMerged(rec, in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "update dns record")
	}
	if in.Name != "" {
		rec.Name = in.Name
	}
	if in.IP != "" {
		rec.IP = in.IP
	}
	if in.SetEnabled {
		rec.Enabled = in.Enabled
	}
	return rec, nil
}

func (s *DNSService) Delete(ctx context.Context, id string) (plan.Plan, DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return plan.Plan{}, DNSRecord{}, err
	}
	p := plan.Delete("dns", rec.ID, rec.Name,
		fmt.Sprintf("delete dns record %s", rec.Name),
		dnsRecordSnapshot(rec),
	)
	return p, rec, nil
}

func (s *DNSService) ApplyDelete(ctx context.Context, id string) (DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return DNSRecord{}, err
	}
	path := s.api.SitePath(client.PathRestDNSRecord, rec.ID)
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "delete dns record")
	}
	return rec, nil
}

func (s *DNSService) ListResolvers(ctx context.Context) ([]DNSResolver, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathRestNetwork)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]DNSResolver, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeDNSResolver(m))
	}
	return out, nil
}

func (s *DNSService) SetResolvers(ctx context.Context, networkQuery string, servers []string) (plan.Plan, DNSResolver, error) {
	r, err := s.getResolver(ctx, networkQuery)
	if err != nil {
		return plan.Plan{}, DNSResolver{}, err
	}
	before := resolverSnapshot(r)
	after := resolverSnapshot(DNSResolver{
		NetworkID:   r.NetworkID,
		NetworkName: r.NetworkName,
		DNS:         append([]string(nil), servers...),
		WAN:         r.WAN,
	})
	p := plan.Update("dns_resolver", r.NetworkID, r.NetworkName,
		fmt.Sprintf("set DNS resolvers on %s to %s", r.NetworkName, strings.Join(servers, ",")),
		before,
		after,
	)
	return p, r, nil
}

func (s *DNSService) ApplySetResolvers(ctx context.Context, networkQuery string, servers []string) (DNSResolver, error) {
	r, err := s.getResolver(ctx, networkQuery)
	if err != nil {
		return DNSResolver{}, err
	}
	path := s.api.SitePath(client.PathRestNetwork, r.NetworkID)
	body := resolverSetBody(r, servers)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return DNSResolver{}, err
	}
	r.DNS = append([]string(nil), servers...)
	return r, nil
}

type resolverIdent struct {
	DNSResolver
}

func (r resolverIdent) GetID() string   { return r.NetworkID }
func (r resolverIdent) GetMAC() string  { return "" }
func (r resolverIdent) GetName() string { return r.NetworkName }

func (s *DNSService) getResolver(ctx context.Context, networkQuery string) (DNSResolver, error) {
	items, err := s.ListResolvers(ctx)
	if err != nil {
		return DNSResolver{}, err
	}
	idents := make([]resolverIdent, len(items))
	for i, it := range items {
		idents[i] = resolverIdent{it}
	}
	hit, err := resolve.One(idents, networkQuery)
	if err != nil {
		return DNSResolver{}, err
	}
	return hit.DNSResolver, nil
}

func (s *DNSService) fetchRecords(ctx context.Context) ([]map[string]any, error) {
	// Prefer rest/dnsrecord. No silent empty success on 404.
	var raw []map[string]any
	path := s.api.SitePath(client.PathRestDNSRecord)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, mapDNSEndpointErr(err, "list dns records")
	}
	return raw, nil
}

func mapDNSEndpointErr(err error, op string) error {
	if apperr.Is(err, apperr.NotFound) {
		return apperr.WithHint(
			apperr.Newf(apperr.NotImplemented, "dns records endpoint unavailable on this controller (%s)", op),
			"controller returned 404 for rest/dnsrecord; upgrade controller mapping or use a firmware that exposes local DNS REST",
		)
	}
	return err
}

func NormalizeDNSRecord(m map[string]any) DNSRecord {
	return DNSRecord{
		ID:      strField(m, "_id", "id"),
		Name:    strField(m, "key", "name", "host_name", "hostname"),
		IP:      strField(m, "value", "ip", "content", "record_value"),
		Enabled: boolFieldDefault(m, "enabled", true),
	}
}

func NormalizeDNSResolver(m map[string]any) DNSResolver {
	n := NormalizeNetwork(m)
	return DNSResolver{
		NetworkID:   n.ID,
		NetworkName: n.Name,
		DNS:         extractNetworkDNS(m, n.WAN),
		WAN:         n.WAN,
	}
}

func extractNetworkDNS(m map[string]any, wan bool) []string {
	// Prefer dns_nameservers array when present.
	if v, ok := m["dns_nameservers"]; ok {
		if ss := anyStringSlice(v); len(ss) > 0 {
			return ss
		}
	}
	var out []string
	if wan {
		for _, k := range []string{"wan_dns1", "wan_dns2", "wan_dns3", "wan_dns4"} {
			if s := strField(m, k); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	for _, k := range []string{"dhcpd_dns_1", "dhcpd_dns_2", "dhcpd_dns_3", "dhcpd_dns_4"} {
		if s := strField(m, k); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func anyStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, el := range t {
			if s := anyToString(el); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func dnsInputBody(in DNSInput) map[string]any {
	enabled := in.Enabled
	if !in.SetEnabled {
		enabled = true
	}
	body := map[string]any{
		"enabled":     enabled,
		"record_type": "A",
	}
	if in.Name != "" {
		body["key"] = in.Name
		body["name"] = in.Name
	}
	if in.IP != "" {
		body["value"] = in.IP
		body["ip"] = in.IP
	}
	return body
}

func dnsInputBodyMerged(rec DNSRecord, in DNSInput) map[string]any {
	name := rec.Name
	if in.Name != "" {
		name = in.Name
	}
	ip := rec.IP
	if in.IP != "" {
		ip = in.IP
	}
	enabled := rec.Enabled
	if in.SetEnabled {
		enabled = in.Enabled
	}
	return map[string]any{
		"key":         name,
		"name":        name,
		"value":       ip,
		"ip":          ip,
		"enabled":     enabled,
		"record_type": "A",
	}
}

func dnsRecordSnapshot(r DNSRecord) map[string]any {
	return map[string]any{
		"id":      r.ID,
		"name":    r.Name,
		"ip":      r.IP,
		"enabled": r.Enabled,
	}
}

func mergeDNSAfter(r DNSRecord, in DNSInput) map[string]any {
	after := dnsRecordSnapshot(r)
	if in.Name != "" {
		after["name"] = in.Name
	}
	if in.IP != "" {
		after["ip"] = in.IP
	}
	if in.SetEnabled {
		after["enabled"] = in.Enabled
	}
	return after
}

func resolverSnapshot(r DNSResolver) map[string]any {
	return map[string]any{
		"network_id":   r.NetworkID,
		"network_name": r.NetworkName,
		"dns":          append([]string(nil), r.DNS...),
		"wan":          r.WAN,
	}
}

func resolverSetBody(r DNSResolver, servers []string) map[string]any {
	body := map[string]any{}
	if r.WAN {
		keys := []string{"wan_dns1", "wan_dns2", "wan_dns3", "wan_dns4"}
		for i, k := range keys {
			if i < len(servers) {
				body[k] = servers[i]
			} else {
				body[k] = ""
			}
		}
		return body
	}
	body["dhcpd_dns_enabled"] = len(servers) > 0
	keys := []string{"dhcpd_dns_1", "dhcpd_dns_2", "dhcpd_dns_3", "dhcpd_dns_4"}
	for i, k := range keys {
		if i < len(servers) {
			body[k] = servers[i]
		} else {
			body[k] = ""
		}
	}
	return body
}
