package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"sort"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

// DNSAPI is the transport for official DNS/network reads and legacy
// networkconf resolver mutations.
type DNSAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
	IntegrationSitePath(ctx context.Context, parts ...string) (string, error)
}

// DNSRecord is a normalized official DNS policy. Name and IP remain internal
// compatibility aliases for existing human output and resolver behavior;
// normalized JSON retains the official policy fields in snake_case.
type DNSRecord struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Domain           string `json:"domain"`
	Enabled          bool   `json:"enabled"`
	IPv4Address      string `json:"ipv4_address,omitempty"`
	IPv6Address      string `json:"ipv6_address,omitempty"`
	TargetDomain     string `json:"target_domain,omitempty"`
	MailServerDomain string `json:"mail_server_domain,omitempty"`
	Text             string `json:"text,omitempty"`
	ServerDomain     string `json:"server_domain,omitempty"`
	IPAddress        string `json:"ip_address,omitempty"`
	TTLSeconds       int    `json:"ttl_seconds,omitempty"`
	Priority         int    `json:"priority,omitempty"`
	Service          string `json:"service,omitempty"`
	Protocol         string `json:"protocol,omitempty"`
	Port             int    `json:"port,omitempty"`
	Weight           int    `json:"weight,omitempty"`
	Name             string `json:"-"`
	IP               string `json:"-"`
}

const defaultDNSTTLSeconds = 300

func (r DNSRecord) GetID() string   { return r.ID }
func (r DNSRecord) GetMAC() string  { return "" }
func (r DNSRecord) GetName() string { return r.Name }

// MarshalJSON emits the fields applicable to each official policy type. This
// preserves meaningful zero values such as SRV priority or weight without
// adding unrelated zero-valued fields to every policy.
func (r DNSRecord) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"id":      r.ID,
		"type":    r.Type,
		"domain":  r.Domain,
		"enabled": r.Enabled,
	}
	withTTL := func() { out["ttl_seconds"] = r.TTLSeconds }
	switch r.Type {
	case "A_RECORD":
		out["ipv4_address"] = r.IPv4Address
		withTTL()
	case "AAAA_RECORD":
		out["ipv6_address"] = r.IPv6Address
		withTTL()
	case "CNAME_RECORD":
		out["target_domain"] = r.TargetDomain
		withTTL()
	case "MX_RECORD":
		out["mail_server_domain"] = r.MailServerDomain
		out["priority"] = r.Priority
		withTTL()
	case "TXT_RECORD":
		out["text"] = r.Text
		withTTL()
	case "SRV_RECORD":
		out["server_domain"] = r.ServerDomain
		out["priority"] = r.Priority
		out["service"] = r.Service
		out["protocol"] = r.Protocol
		out["port"] = r.Port
		out["weight"] = r.Weight
		withTTL()
	case "FORWARD_DOMAIN":
		out["server_domain"] = r.ServerDomain
		out["ip_address"] = r.IPAddress
	default:
		addDNSNonzeroFields(out, r)
	}
	return json.Marshal(out)
}

func addDNSNonzeroFields(out map[string]any, r DNSRecord) {
	strings := map[string]string{
		"ipv4_address":       r.IPv4Address,
		"ipv6_address":       r.IPv6Address,
		"target_domain":      r.TargetDomain,
		"mail_server_domain": r.MailServerDomain,
		"text":               r.Text,
		"server_domain":      r.ServerDomain,
		"ip_address":         r.IPAddress,
		"service":            r.Service,
		"protocol":           r.Protocol,
	}
	for key, value := range strings {
		if value != "" {
			out[key] = value
		}
	}
	if r.TTLSeconds != 0 {
		out["ttl_seconds"] = r.TTLSeconds
	}
	if r.Priority != 0 {
		out["priority"] = r.Priority
	}
	if r.Port != 0 {
		out["port"] = r.Port
	}
	if r.Weight != 0 {
		out["weight"] = r.Weight
	}
}

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
	SetName    bool
	IP         string
	SetIP      bool
	Enabled    bool
	SetEnabled bool // when true, Enabled is applied; otherwise preserve existing
	TTLSeconds int
	SetTTL     bool
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
	record, err := resolve.One(items, id)
	if err != nil {
		return DNSRecord{}, err
	}
	if record.ID == "" {
		return DNSRecord{}, apperr.New(apperr.Internal, "controller returned a DNS policy without an ID")
	}
	return record, nil
}

func (s *DNSService) Create(ctx context.Context, in DNSInput) (plan.Plan, error) {
	_ = ctx
	if err := validateDNSCreateInput(in); err != nil {
		return plan.Plan{}, err
	}
	enabled := in.Enabled
	if !in.SetEnabled {
		enabled = true
	}
	ttlSeconds := effectiveDNSTTL(in)
	p := plan.Create("dns", in.Name,
		fmt.Sprintf("create dns record %s → %s", in.Name, in.IP),
		dnsRecordSnapshot(DNSRecord{Type: "A_RECORD", Domain: in.Name, Name: in.Name, IPv4Address: in.IP, IP: in.IP, Enabled: enabled, TTLSeconds: ttlSeconds}),
	)
	return p, nil
}

func (s *DNSService) ApplyCreate(ctx context.Context, in DNSInput) (DNSRecord, error) {
	if err := validateDNSCreateInput(in); err != nil {
		return DNSRecord{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies")
	if err != nil {
		return DNSRecord{}, err
	}
	if !in.SetEnabled {
		in.Enabled = true
		in.SetEnabled = true
	}
	body := dnsInputBody(in)
	var raw map[string]any
	if err := s.api.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "create dns record")
	}
	id := strField(raw, "id", "_id")
	if id == "" {
		return DNSRecord{}, apperr.New(apperr.Internal, "controller did not return an ID for the created DNS policy")
	}
	created, err := s.getByID(ctx, id)
	if err != nil {
		return DNSRecord{}, verificationError("created DNS policy could not be verified", err)
	}
	if !dnsCreateMatches(created, in) {
		return DNSRecord{}, apperr.New(apperr.Conflict, "created DNS policy does not match the requested fields")
	}
	return created, nil
}

func (s *DNSService) Update(ctx context.Context, id string, in DNSInput) (plan.Plan, DNSRecord, error) {
	rec, before, after, err := s.prepareUpdate(ctx, id, in)
	if err != nil {
		return plan.Plan{}, DNSRecord{}, err
	}
	p := plan.Update("dns", rec.ID, rec.Name,
		fmt.Sprintf("update dns record %s", rec.Name),
		before,
		after,
	)
	return p, rec, nil
}

func (s *DNSService) ApplyUpdate(ctx context.Context, id string, in DNSInput) (DNSRecord, error) {
	rec, _, _, err := s.prepareUpdate(ctx, id, in)
	if err != nil {
		return DNSRecord{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies", rec.ID)
	if err != nil {
		return DNSRecord{}, err
	}
	body := dnsInputBodyMerged(rec, in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "update dns record")
	}
	updated, err := s.getByID(ctx, rec.ID)
	if err != nil {
		return DNSRecord{}, verificationError("updated DNS policy could not be verified", err)
	}
	if !dnsUpdateMatches(updated, in) {
		return DNSRecord{}, apperr.New(apperr.Conflict, "updated DNS policy does not match the requested fields")
	}
	return updated, nil
}

func (s *DNSService) prepareUpdate(ctx context.Context, id string, in DNSInput) (DNSRecord, map[string]any, map[string]any, error) {
	if err := validateDNSUpdateInput(in); err != nil {
		return DNSRecord{}, nil, nil, err
	}
	rec, err := s.Get(ctx, id)
	if err != nil {
		return DNSRecord{}, nil, nil, err
	}
	if err := requireARecord(rec, "update"); err != nil {
		return DNSRecord{}, nil, nil, err
	}
	before := dnsRecordSnapshot(rec)
	after := mergeDNSAfter(rec, in)
	if reflect.DeepEqual(before, after) {
		return DNSRecord{}, nil, nil, apperr.New(apperr.ValidationFailed, "DNS update does not change the current policy")
	}
	return rec, before, after, nil
}

func (s *DNSService) Delete(ctx context.Context, id string) (plan.Plan, DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return plan.Plan{}, DNSRecord{}, err
	}
	if err := requireARecord(rec, "delete"); err != nil {
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
	if err := requireARecord(rec, "delete"); err != nil {
		return DNSRecord{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies", rec.ID)
	if err != nil {
		return DNSRecord{}, err
	}
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "delete dns record")
	}
	if _, err := s.getByID(ctx, rec.ID); err == nil {
		return DNSRecord{}, apperr.New(apperr.Conflict, "deleted DNS policy is still available by ID")
	} else if !apperr.Is(err, apperr.NotFound) {
		return DNSRecord{}, verificationError("deleted DNS policy ID could not be verified", err)
	}
	items, err := s.List(ctx)
	if err != nil {
		return DNSRecord{}, verificationError("deleted DNS policy name could not be verified", err)
	}
	for _, item := range items {
		if item.Domain == rec.Domain {
			return DNSRecord{}, apperr.New(apperr.Conflict, "a DNS policy with the deleted exact name still exists")
		}
	}
	return rec, nil
}

func (s *DNSService) ListResolvers(ctx context.Context) ([]DNSResolver, error) {
	overviews, official, err := fetchOfficialSite(s.api, ctx, "networks")
	if err != nil {
		return nil, err
	}
	if !official {
		return nil, apperr.New(apperr.Internal, "official network transport is required to list DNS resolvers")
	}
	details, err := fetchOfficialSiteDetails(ctx, s.api, overviews, "networks")
	if err != nil {
		return nil, err
	}
	out := make([]DNSResolver, 0, len(details))
	for _, m := range details {
		out = append(out, NormalizeDNSResolver(m))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NetworkName != out[j].NetworkName {
			return out[i].NetworkName < out[j].NetworkName
		}
		return out[i].NetworkID < out[j].NetworkID
	})
	return out, nil
}

func (s *DNSService) SetResolvers(ctx context.Context, networkQuery string, servers []string) (plan.Plan, DNSResolver, error) {
	if err := validateResolvers(servers); err != nil {
		return plan.Plan{}, DNSResolver{}, err
	}
	r, err := s.getResolver(ctx, networkQuery)
	if err != nil {
		return plan.Plan{}, DNSResolver{}, err
	}
	if reflect.DeepEqual(r.DNS, servers) {
		return plan.Plan{}, DNSResolver{}, apperr.New(apperr.ValidationFailed, "resolver update would not change controller state")
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
	if err := validateResolvers(servers); err != nil {
		return DNSResolver{}, err
	}
	r, err := s.getResolver(ctx, networkQuery)
	if err != nil {
		return DNSResolver{}, err
	}
	if reflect.DeepEqual(r.DNS, servers) {
		return DNSResolver{}, apperr.New(apperr.ValidationFailed, "resolver update would not change controller state")
	}
	path := s.api.SitePath(client.PathRestNetwork, r.NetworkID)
	body := resolverSetBody(r, servers)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return DNSResolver{}, err
	}
	observed, err := s.getResolver(ctx, r.NetworkID)
	if err != nil {
		return DNSResolver{}, verificationError("updated DNS resolvers could not be verified", err)
	}
	if !reflect.DeepEqual(observed.DNS, servers) {
		return DNSResolver{}, apperr.New(apperr.Conflict, "DNS resolver verification failed: observed servers differ from requested state")
	}
	return observed, nil
}

type resolverIdent struct {
	DNSResolver
}

func (r resolverIdent) GetID() string   { return r.NetworkID }
func (r resolverIdent) GetMAC() string  { return "" }
func (r resolverIdent) GetName() string { return r.NetworkName }

func (s *DNSService) getResolver(ctx context.Context, networkQuery string) (DNSResolver, error) {
	items, err := s.listLegacyResolvers(ctx)
	if err != nil {
		return DNSResolver{}, err
	}
	idents := make([]resolverIdent, len(items))
	for i, it := range items {
		idents[i] = resolverIdent{it}
	}
	if hit, ok := findExactID(idents, networkQuery); ok {
		return hit.DNSResolver, nil
	}
	if !looksLikeUUID(networkQuery) {
		hit, err := resolve.One(idents, networkQuery)
		if err != nil {
			return DNSResolver{}, err
		}
		return hit.DNSResolver, nil
	}

	raw, official, err := fetchOfficialSite(s.api, ctx, "networks")
	if err != nil {
		return DNSResolver{}, err
	}
	if !official {
		hit, err := resolve.One(idents, networkQuery)
		if err != nil {
			return DNSResolver{}, err
		}
		return hit.DNSResolver, nil
	}
	officialIdents := make([]resolverIdent, 0, len(raw))
	for _, item := range raw {
		officialIdents = append(officialIdents, resolverIdent{NormalizeDNSResolver(item)})
	}
	hit, err := resolveLegacyMutationTarget(idents, officialIdents, networkQuery, "DNS resolver network", func(a, b resolverIdent) bool {
		return sameName(a, b)
	})
	if err != nil {
		return DNSResolver{}, err
	}
	return hit.DNSResolver, nil
}

func (s *DNSService) listLegacyResolvers(ctx context.Context) ([]DNSResolver, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathRestNetwork)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]DNSResolver, 0, len(raw))
	for _, item := range raw {
		out = append(out, NormalizeDNSResolver(item))
	}
	return out, nil
}

func (s *DNSService) fetchRecords(ctx context.Context) ([]map[string]any, error) {
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies")
	if err != nil {
		return nil, err
	}
	if fetcher, ok := s.api.(interface {
		FetchOfficialObjects(context.Context, string) ([]map[string]any, error)
	}); ok {
		raw, err := fetcher.FetchOfficialObjects(ctx, path)
		if err != nil {
			return nil, mapDNSEndpointErr(err, "list dns records")
		}
		return raw, nil
	}
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, mapDNSEndpointErr(err, "list dns records")
	}
	return raw, nil
}

func mapDNSEndpointErr(err error, op string) error {
	if apperr.Is(err, apperr.NotFound) {
		return apperr.WithHint(
			apperr.Newf(apperr.NotImplemented, "dns records endpoint unavailable on this controller (%s)", op),
			"controller returned 404 for the official DNS policies API; upgrade UniFi Network to a version that exposes DNS policies",
		)
	}
	return err
}

func NormalizeDNSRecord(m map[string]any) DNSRecord {
	domain := strField(m, "domain", "key", "name", "host_name", "hostname")
	ipv4Address := strField(m, "ipv4Address", "value", "ip", "content", "record_value")
	return DNSRecord{
		ID:               strField(m, "_id", "id"),
		Type:             strField(m, "type"),
		Domain:           domain,
		Enabled:          boolFieldDefault(m, "enabled", true),
		IPv4Address:      ipv4Address,
		IPv6Address:      strField(m, "ipv6Address"),
		TargetDomain:     strField(m, "targetDomain"),
		MailServerDomain: strField(m, "mailServerDomain"),
		Text:             strField(m, "text"),
		ServerDomain:     strField(m, "serverDomain"),
		IPAddress:        strField(m, "ipAddress"),
		TTLSeconds:       intField(m, "ttlSeconds", "ttl_seconds"),
		Priority:         intField(m, "priority"),
		Service:          strField(m, "service"),
		Protocol:         strField(m, "protocol"),
		Port:             intField(m, "port"),
		Weight:           intField(m, "weight"),
		Name:             domain,
		IP:               ipv4Address,
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
	if ipv4, ok := m["ipv4Configuration"].(map[string]any); ok {
		if dhcp, ok := ipv4["dhcpConfiguration"].(map[string]any); ok {
			if servers := anyStringSlice(dhcp["dnsServerIpAddressesOverride"]); len(servers) > 0 {
				return servers
			}
		}
	}
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
		"enabled":    enabled,
		"type":       "A_RECORD",
		"ttlSeconds": effectiveDNSTTL(in),
	}
	if in.Name != "" {
		body["domain"] = in.Name
	}
	if in.IP != "" {
		body["ipv4Address"] = in.IP
	}
	return body
}

func dnsInputBodyMerged(rec DNSRecord, in DNSInput) map[string]any {
	name := rec.Domain
	if inputSetsName(in) {
		name = in.Name
	}
	ip := rec.IPv4Address
	if inputSetsIP(in) {
		ip = in.IP
	}
	enabled := rec.Enabled
	if in.SetEnabled {
		enabled = in.Enabled
	}
	ttlSeconds := rec.TTLSeconds
	if inputSetsTTL(in) {
		ttlSeconds = in.TTLSeconds
	}
	if ttlSeconds <= 0 {
		ttlSeconds = defaultDNSTTLSeconds
	}
	return map[string]any{
		"type":        "A_RECORD",
		"domain":      name,
		"ipv4Address": ip,
		"enabled":     enabled,
		"ttlSeconds":  ttlSeconds,
	}
}

func dnsRecordSnapshot(r DNSRecord) map[string]any {
	return map[string]any{
		"id":          r.ID,
		"type":        r.Type,
		"name":        r.Domain,
		"ip":          r.IPv4Address,
		"enabled":     r.Enabled,
		"ttl_seconds": r.TTLSeconds,
	}
}

func mergeDNSAfter(r DNSRecord, in DNSInput) map[string]any {
	after := dnsRecordSnapshot(r)
	if inputSetsName(in) {
		after["name"] = in.Name
	}
	if inputSetsIP(in) {
		after["ip"] = in.IP
	}
	if in.SetEnabled {
		after["enabled"] = in.Enabled
	}
	if inputSetsTTL(in) {
		after["ttl_seconds"] = in.TTLSeconds
	}
	return after
}

func (s *DNSService) getByID(ctx context.Context, id string) (DNSRecord, error) {
	if id == "" {
		return DNSRecord{}, apperr.New(apperr.Internal, "DNS policy ID is required")
	}
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies", id)
	if err != nil {
		return DNSRecord{}, err
	}
	var raw map[string]any
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return DNSRecord{}, err
	}
	record := NormalizeDNSRecord(raw)
	if record.ID == "" {
		return DNSRecord{}, apperr.New(apperr.Internal, "controller returned a DNS policy without an ID")
	}
	if record.ID != id {
		return DNSRecord{}, apperr.New(apperr.Conflict, "controller returned a different DNS policy ID during verification")
	}
	return record, nil
}

func requireARecord(record DNSRecord, operation string) error {
	if record.Type != "A_RECORD" {
		return apperr.Newf(apperr.ValidationFailed, "DNS %s supports only A_RECORD policies", operation)
	}
	return nil
}

func validateDNSCreateInput(in DNSInput) error {
	if err := validateDNSName(in.Name); err != nil {
		return err
	}
	if err := validateDNSIPv4(in.IP); err != nil {
		return err
	}
	if inputSetsTTL(in) && in.TTLSeconds <= 0 {
		return apperr.New(apperr.ValidationFailed, "DNS TTL must be positive")
	}
	return nil
}

func validateDNSUpdateInput(in DNSInput) error {
	if !inputSetsName(in) && !inputSetsIP(in) && !in.SetEnabled && !inputSetsTTL(in) {
		return apperr.New(apperr.ValidationFailed, "DNS update requires at least one changed field")
	}
	if inputSetsName(in) {
		if err := validateDNSName(in.Name); err != nil {
			return err
		}
	}
	if inputSetsIP(in) {
		if err := validateDNSIPv4(in.IP); err != nil {
			return err
		}
	}
	if inputSetsTTL(in) && in.TTLSeconds <= 0 {
		return apperr.New(apperr.ValidationFailed, "DNS TTL must be positive")
	}
	return nil
}

func validateDNSName(name string) error {
	if len(name) == 0 || len(name) > 253 {
		return apperr.New(apperr.ValidationFailed, "DNS name must contain 1 to 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return apperr.New(apperr.ValidationFailed, "DNS labels must contain 1 to 63 characters")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return apperr.New(apperr.ValidationFailed, "DNS labels must not begin or end with a hyphen")
		}
		for _, char := range label {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
				continue
			}
			return apperr.New(apperr.ValidationFailed, "DNS labels may contain only letters, digits, and hyphens")
		}
	}
	return nil
}

func validateDNSIPv4(value string) error {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return apperr.New(apperr.ValidationFailed, "DNS address must be a valid IPv4 address")
	}
	return nil
}

func inputSetsName(in DNSInput) bool { return in.SetName || in.Name != "" }
func inputSetsIP(in DNSInput) bool   { return in.SetIP || in.IP != "" }
func inputSetsTTL(in DNSInput) bool  { return in.SetTTL || in.TTLSeconds != 0 }

func effectiveDNSTTL(in DNSInput) int {
	if inputSetsTTL(in) {
		return in.TTLSeconds
	}
	return defaultDNSTTLSeconds
}

func dnsCreateMatches(record DNSRecord, in DNSInput) bool {
	enabled := true
	if in.SetEnabled {
		enabled = in.Enabled
	}
	return record.Type == "A_RECORD" && record.Domain == in.Name && record.IPv4Address == in.IP &&
		record.Enabled == enabled && record.TTLSeconds == effectiveDNSTTL(in)
}

func dnsUpdateMatches(record DNSRecord, in DNSInput) bool {
	if record.Type != "A_RECORD" {
		return false
	}
	if inputSetsName(in) && record.Domain != in.Name {
		return false
	}
	if inputSetsIP(in) && record.IPv4Address != in.IP {
		return false
	}
	if in.SetEnabled && record.Enabled != in.Enabled {
		return false
	}
	if inputSetsTTL(in) && record.TTLSeconds != in.TTLSeconds {
		return false
	}
	return true
}

func verificationError(message string, cause error) error {
	return apperr.WithCause(apperr.New(apperr.Conflict, message), cause)
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
		// Keep dns_nameservers in sync: list prefers it when non-empty.
		body["dns_nameservers"] = append([]string(nil), servers...)
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
	// Keep dns_nameservers in sync: list prefers it when non-empty.
	body["dns_nameservers"] = append([]string(nil), servers...)
	return body
}

func validateResolvers(servers []string) error {
	if len(servers) < 1 || len(servers) > 4 {
		return apperr.New(apperr.ValidationFailed, "DNS resolvers require 1 to 4 IP addresses")
	}
	for _, server := range servers {
		if _, err := netip.ParseAddr(server); err != nil {
			return apperr.Newf(apperr.ValidationFailed, "DNS resolver %q must be a valid IP address", server)
		}
	}
	return nil
}
