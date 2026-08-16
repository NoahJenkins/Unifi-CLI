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
	Type                string
	Name                string
	SetName             bool
	IP                  string
	SetIP               bool
	IPv6Address         string
	SetIPv6Address      bool
	TargetDomain        string
	SetTargetDomain     bool
	MailServerDomain    string
	SetMailServerDomain bool
	Text                string
	SetText             bool
	ServerDomain        string
	SetServerDomain     bool
	ServerIP            string
	SetServerIP         bool
	Priority            int
	SetPriority         bool
	Service             string
	SetService          bool
	Protocol            string
	SetProtocol         bool
	Port                int
	SetPort             bool
	Weight              int
	SetWeight           bool
	Enabled             bool
	SetEnabled          bool // when true, Enabled is applied; otherwise preserve existing
	TTLSeconds          int
	SetTTL              bool
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
	expected, err := expectedDNSCreateRecord(in)
	if err != nil {
		return plan.Plan{}, err
	}
	p := plan.Create("dns", in.Name,
		fmt.Sprintf("create %s DNS policy %s", expected.Type, in.Name),
		dnsRecordSnapshot(expected),
	)
	return p, nil
}

func (s *DNSService) ApplyCreate(ctx context.Context, in DNSInput) (DNSRecord, error) {
	expected, err := expectedDNSCreateRecord(in)
	if err != nil {
		return DNSRecord{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies")
	if err != nil {
		return DNSRecord{}, err
	}
	body, err := dnsWritableBody(expected)
	if err != nil {
		return DNSRecord{}, err
	}
	var raw map[string]any
	if err := s.api.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "create dns record")
	}
	id := strField(raw, "id", "_id")
	if id == "" {
		return DNSRecord{}, apperr.New(apperr.Internal, "controller did not return an ID for the created DNS policy")
	}
	expected.ID = id
	created, err := s.getByID(ctx, id)
	if err != nil {
		return DNSRecord{}, verificationError("created DNS policy could not be verified", err)
	}
	if !dnsRecordsEqual(created, expected) {
		return DNSRecord{}, apperr.New(apperr.Conflict, "created DNS policy does not match the requested fields")
	}
	return created, nil
}

func (s *DNSService) Update(ctx context.Context, id string, in DNSInput) (plan.Plan, DNSRecord, error) {
	rec, _, before, after, err := s.prepareUpdate(ctx, id, in)
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
	return s.applyUpdate(ctx, id, in, nil)
}

func (s *DNSService) ApplyUpdatePrepared(ctx context.Context, target plan.Target, id string, in DNSInput) (DNSRecord, error) {
	return s.applyUpdate(ctx, id, in, &target)
}

func (s *DNSService) applyUpdate(ctx context.Context, id string, in DNSInput, target *plan.Target) (DNSRecord, error) {
	rec, expected, before, after, err := s.prepareUpdate(ctx, id, in)
	if err != nil {
		return DNSRecord{}, err
	}
	if target != nil {
		p := plan.Update("dns", rec.ID, rec.Name, fmt.Sprintf("update dns record %s", rec.Name), before, after)
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return DNSRecord{}, err
		}
	}
	path, err := s.api.IntegrationSitePath(ctx, "dns", "policies", rec.ID)
	if err != nil {
		return DNSRecord{}, err
	}
	body, err := dnsWritableBody(expected)
	if err != nil {
		return DNSRecord{}, err
	}
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return DNSRecord{}, mapDNSEndpointErr(err, "update dns record")
	}
	updated, err := s.getByID(ctx, rec.ID)
	if err != nil {
		return DNSRecord{}, verificationError("updated DNS policy could not be verified", err)
	}
	if !dnsRecordsEqual(updated, expected) {
		return DNSRecord{}, apperr.New(apperr.Conflict, "updated DNS policy does not match the complete requested state")
	}
	return updated, nil
}

func (s *DNSService) prepareUpdate(ctx context.Context, id string, in DNSInput) (DNSRecord, DNSRecord, map[string]any, map[string]any, error) {
	if err := validateDNSUpdateInput(in); err != nil {
		return DNSRecord{}, DNSRecord{}, nil, nil, err
	}
	rec, err := s.Get(ctx, id)
	if err != nil {
		return DNSRecord{}, DNSRecord{}, nil, nil, err
	}
	expected, err := expectedDNSRecord(rec, in)
	if err != nil {
		return DNSRecord{}, DNSRecord{}, nil, nil, err
	}
	before := dnsRecordSnapshot(rec)
	after := dnsRecordSnapshot(expected)
	if reflect.DeepEqual(before, after) {
		return DNSRecord{}, DNSRecord{}, nil, nil, apperr.New(apperr.ValidationFailed, "DNS update does not change the current policy")
	}
	return rec, expected, before, after, nil
}

func (s *DNSService) Delete(ctx context.Context, id string) (plan.Plan, DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return plan.Plan{}, DNSRecord{}, err
	}
	if err := requireSupportedDNSRecord(rec, "delete"); err != nil {
		return plan.Plan{}, DNSRecord{}, err
	}
	p := plan.Delete("dns", rec.ID, rec.Name,
		fmt.Sprintf("delete dns record %s", rec.Name),
		dnsRecordSnapshot(rec),
	)
	return p, rec, nil
}

func (s *DNSService) ApplyDelete(ctx context.Context, id string) (DNSRecord, error) {
	return s.applyDelete(ctx, id, nil)
}

func (s *DNSService) ApplyDeletePrepared(ctx context.Context, target plan.Target, id string) (DNSRecord, error) {
	return s.applyDelete(ctx, id, &target)
}

func (s *DNSService) applyDelete(ctx context.Context, id string, target *plan.Target) (DNSRecord, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return DNSRecord{}, err
	}
	if err := requireSupportedDNSRecord(rec, "delete"); err != nil {
		return DNSRecord{}, err
	}
	if target != nil {
		p := plan.Delete("dns", rec.ID, rec.Name, fmt.Sprintf("delete dns record %s", rec.Name), dnsRecordSnapshot(rec))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return DNSRecord{}, err
		}
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
		if item.Type == rec.Type && item.Domain == rec.Domain {
			return DNSRecord{}, apperr.New(apperr.Conflict, "a DNS policy with the deleted type and exact name still exists")
		}
	}
	return rec, nil
}

func (s *DNSService) ListResolvers(ctx context.Context) ([]DNSResolver, error) {
	ctx, cancel := officialOperationContext(ctx)
	defer cancel()
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
	return s.applySetResolvers(ctx, networkQuery, servers, nil)
}

func (s *DNSService) ApplySetResolversPrepared(ctx context.Context, target plan.Target, networkQuery string, servers []string) (DNSResolver, error) {
	return s.applySetResolvers(ctx, networkQuery, servers, &target)
}

func (s *DNSService) applySetResolvers(ctx context.Context, networkQuery string, servers []string, target *plan.Target) (DNSResolver, error) {
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
	if target != nil {
		before := resolverSnapshot(r)
		after := resolverSnapshot(DNSResolver{NetworkID: r.NetworkID, NetworkName: r.NetworkName, DNS: append([]string(nil), servers...), WAN: r.WAN})
		p := plan.Update("dns_resolver", r.NetworkID, r.NetworkName,
			fmt.Sprintf("set DNS resolvers on %s to %s", r.NetworkName, strings.Join(servers, ",")), before, after)
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return DNSResolver{}, err
		}
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

func expectedDNSCreateRecord(in DNSInput) (DNSRecord, error) {
	recordType, err := canonicalDNSType(in.Type)
	if err != nil {
		return DNSRecord{}, err
	}
	record := DNSRecord{
		Type:             recordType,
		Domain:           in.Name,
		Name:             in.Name,
		IPv4Address:      in.IP,
		IP:               in.IP,
		IPv6Address:      in.IPv6Address,
		TargetDomain:     in.TargetDomain,
		MailServerDomain: in.MailServerDomain,
		Text:             in.Text,
		ServerDomain:     in.ServerDomain,
		IPAddress:        in.ServerIP,
		Priority:         in.Priority,
		Service:          in.Service,
		Protocol:         in.Protocol,
		Port:             in.Port,
		Weight:           in.Weight,
		Enabled:          true,
	}
	if in.SetEnabled {
		record.Enabled = in.Enabled
	}
	if dnsTypeUsesTTL(recordType) {
		record.TTLSeconds = effectiveDNSTTL(in)
	}
	if err := validateDNSInputForType(in, recordType, true); err != nil {
		return DNSRecord{}, err
	}
	if err := validateDNSRecord(record); err != nil {
		return DNSRecord{}, err
	}
	return record, nil
}

func expectedDNSRecord(rec DNSRecord, in DNSInput) (DNSRecord, error) {
	if err := requireSupportedDNSRecord(rec, "update"); err != nil {
		return DNSRecord{}, err
	}
	if in.Type != "" {
		requestedType, err := canonicalDNSType(in.Type)
		if err != nil {
			return DNSRecord{}, err
		}
		if requestedType != rec.Type {
			return DNSRecord{}, apperr.New(apperr.ValidationFailed, "DNS policy type cannot be changed")
		}
	}
	if err := validateDNSInputForType(in, rec.Type, false); err != nil {
		return DNSRecord{}, err
	}
	expected := rec
	if inputSetsName(in) {
		expected.Domain = in.Name
		expected.Name = in.Name
	}
	if in.SetEnabled {
		expected.Enabled = in.Enabled
	}
	if inputSetsTTL(in) {
		expected.TTLSeconds = in.TTLSeconds
	}
	switch rec.Type {
	case "A_RECORD":
		if inputSetsIP(in) {
			expected.IPv4Address, expected.IP = in.IP, in.IP
		}
	case "AAAA_RECORD":
		if inputSetsIPv6(in) {
			expected.IPv6Address = in.IPv6Address
		}
	case "CNAME_RECORD":
		if inputSetsTargetDomain(in) {
			expected.TargetDomain = in.TargetDomain
		}
	case "MX_RECORD":
		if inputSetsMailServerDomain(in) {
			expected.MailServerDomain = in.MailServerDomain
		}
		if in.SetPriority {
			expected.Priority = in.Priority
		}
	case "TXT_RECORD":
		if inputSetsText(in) {
			expected.Text = in.Text
		}
	case "SRV_RECORD":
		if inputSetsServerDomain(in) {
			expected.ServerDomain = in.ServerDomain
		}
		if in.SetPriority {
			expected.Priority = in.Priority
		}
		if inputSetsService(in) {
			expected.Service = in.Service
		}
		if inputSetsProtocol(in) {
			expected.Protocol = in.Protocol
		}
		if in.SetPort {
			expected.Port = in.Port
		}
		if in.SetWeight {
			expected.Weight = in.Weight
		}
	case "FORWARD_DOMAIN":
		if inputSetsServerIP(in) {
			expected.IPAddress = in.ServerIP
		}
	}
	if err := validateDNSRecord(expected); err != nil {
		return DNSRecord{}, apperr.WithCause(
			apperr.WithHint(
				apperr.New(apperr.ValidationFailed, "DNS policy contains a value that cannot be preserved safely"),
				"update the invalid policy in UniFi Network before you retry",
			),
			err,
		)
	}
	return expected, nil
}

func dnsWritableBody(record DNSRecord) (map[string]any, error) {
	if err := validateDNSRecord(record); err != nil {
		return nil, err
	}
	body := map[string]any{
		"type":    record.Type,
		"domain":  record.Domain,
		"enabled": record.Enabled,
	}
	switch record.Type {
	case "A_RECORD":
		body["ipv4Address"] = record.IPv4Address
		body["ttlSeconds"] = record.TTLSeconds
	case "AAAA_RECORD":
		body["ipv6Address"] = record.IPv6Address
		body["ttlSeconds"] = record.TTLSeconds
	case "CNAME_RECORD":
		body["targetDomain"] = record.TargetDomain
		body["ttlSeconds"] = record.TTLSeconds
	case "MX_RECORD":
		body["mailServerDomain"] = record.MailServerDomain
		body["priority"] = record.Priority
	case "TXT_RECORD":
		body["text"] = record.Text
	case "SRV_RECORD":
		body["serverDomain"] = record.ServerDomain
		body["priority"] = record.Priority
		body["service"] = record.Service
		body["protocol"] = record.Protocol
		body["port"] = record.Port
		body["weight"] = record.Weight
	case "FORWARD_DOMAIN":
		body["ipAddress"] = record.IPAddress
	}
	return body, nil
}

func dnsRecordSnapshot(r DNSRecord) map[string]any {
	out := map[string]any{
		"id":      r.ID,
		"type":    r.Type,
		"name":    r.Domain,
		"enabled": r.Enabled,
	}
	switch r.Type {
	case "A_RECORD":
		out["ip"] = r.IPv4Address
		out["ttl_seconds"] = r.TTLSeconds
	case "AAAA_RECORD":
		out["ipv6_address"] = r.IPv6Address
		out["ttl_seconds"] = r.TTLSeconds
	case "CNAME_RECORD":
		out["target_domain"] = r.TargetDomain
		out["ttl_seconds"] = r.TTLSeconds
	case "MX_RECORD":
		out["mail_server_domain"] = r.MailServerDomain
		out["priority"] = r.Priority
	case "TXT_RECORD":
		out["text"] = r.Text
	case "SRV_RECORD":
		out["server_domain"] = r.ServerDomain
		out["priority"] = r.Priority
		out["service"] = r.Service
		out["protocol"] = r.Protocol
		out["port"] = r.Port
		out["weight"] = r.Weight
	case "FORWARD_DOMAIN":
		out["ip_address"] = r.IPAddress
	}
	return out
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

func requireSupportedDNSRecord(record DNSRecord, operation string) error {
	if !isSupportedDNSType(record.Type) {
		return apperr.Newf(apperr.ValidationFailed, "DNS %s does not support policy type %q", operation, record.Type)
	}
	return nil
}

func validateDNSUpdateInput(in DNSInput) error {
	if !inputSetsAnyDNSField(in) {
		return apperr.New(apperr.ValidationFailed, "DNS update requires at least one changed field")
	}
	return nil
}

func validateDNSName(name string) error {
	if len(name) == 0 || len(name) > 127 {
		return apperr.New(apperr.ValidationFailed, "DNS name must contain 1 to 127 characters")
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

func validateDNSIPv6(value string) error {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is6() {
		return apperr.New(apperr.ValidationFailed, "DNS address must be a valid IPv6 address")
	}
	return nil
}

func validateDNSIP(value string) error {
	if _, err := netip.ParseAddr(value); err != nil {
		return apperr.New(apperr.ValidationFailed, "DNS server address must be a valid IP address")
	}
	return nil
}

func canonicalDNSType(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "", "a", "a-record":
		return "A_RECORD", nil
	case "aaaa", "aaaa-record":
		return "AAAA_RECORD", nil
	case "cname", "cname-record":
		return "CNAME_RECORD", nil
	case "mx", "mx-record":
		return "MX_RECORD", nil
	case "txt", "txt-record":
		return "TXT_RECORD", nil
	case "srv", "srv-record":
		return "SRV_RECORD", nil
	case "forward-domain":
		return "FORWARD_DOMAIN", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "unsupported DNS policy type %q", value)
	}
}

func isSupportedDNSType(value string) bool {
	switch value {
	case "A_RECORD", "AAAA_RECORD", "CNAME_RECORD", "MX_RECORD", "TXT_RECORD", "SRV_RECORD", "FORWARD_DOMAIN":
		return true
	default:
		return false
	}
}

func dnsTypeUsesTTL(value string) bool {
	return value == "A_RECORD" || value == "AAAA_RECORD" || value == "CNAME_RECORD"
}

func validateDNSInputForType(in DNSInput, recordType string, create bool) error {
	usesA := inputSetsIP(in)
	usesAAAA := inputSetsIPv6(in)
	usesCNAME := inputSetsTargetDomain(in)
	usesMX := inputSetsMailServerDomain(in) || in.SetPriority || in.Priority != 0
	usesTXT := inputSetsText(in)
	usesSRV := inputSetsServerDomain(in) || inputSetsService(in) || inputSetsProtocol(in) || in.SetPort || in.Port != 0 || in.SetWeight || in.Weight != 0
	usesForward := inputSetsServerIP(in)

	if !dnsTypeUsesTTL(recordType) && inputSetsTTL(in) {
		return apperr.Newf(apperr.ValidationFailed, "DNS TTL is not writable for %s policies", recordType)
	}
	invalid := false
	switch recordType {
	case "A_RECORD":
		invalid = usesAAAA || usesCNAME || usesMX || usesTXT || usesSRV || usesForward
	case "AAAA_RECORD":
		invalid = usesA || usesCNAME || usesMX || usesTXT || usesSRV || usesForward
	case "CNAME_RECORD":
		invalid = usesA || usesAAAA || usesMX || usesTXT || usesSRV || usesForward
	case "MX_RECORD":
		invalid = usesA || usesAAAA || usesCNAME || usesTXT || usesSRV || usesForward
	case "TXT_RECORD":
		invalid = usesA || usesAAAA || usesCNAME || usesMX || usesSRV || usesForward
	case "SRV_RECORD":
		invalid = usesA || usesAAAA || usesCNAME || inputSetsMailServerDomain(in) || usesTXT || usesForward
	case "FORWARD_DOMAIN":
		invalid = usesA || usesAAAA || usesCNAME || usesMX || usesTXT || usesSRV
	default:
		return apperr.Newf(apperr.ValidationFailed, "unsupported DNS policy type %q", recordType)
	}
	if invalid {
		return apperr.Newf(apperr.ValidationFailed, "DNS input contains fields that do not apply to %s policies", recordType)
	}
	if !create {
		return nil
	}
	switch recordType {
	case "MX_RECORD":
		if !in.SetPriority && in.Priority == 0 {
			return apperr.New(apperr.ValidationFailed, "MX priority is required")
		}
	case "SRV_RECORD":
		if !in.SetPriority && in.Priority == 0 {
			return apperr.New(apperr.ValidationFailed, "SRV priority is required")
		}
		if !in.SetPort && in.Port == 0 {
			return apperr.New(apperr.ValidationFailed, "SRV port is required")
		}
		if !in.SetWeight && in.Weight == 0 {
			return apperr.New(apperr.ValidationFailed, "SRV weight is required")
		}
	}
	return nil
}

func validateDNSRecord(record DNSRecord) error {
	if !isSupportedDNSType(record.Type) {
		return apperr.Newf(apperr.ValidationFailed, "unsupported DNS policy type %q", record.Type)
	}
	if record.Type == "SRV_RECORD" {
		if err := validateDNSSRVName(record.Domain); err != nil {
			return err
		}
	} else if err := validateDNSName(record.Domain); err != nil {
		return err
	}
	switch record.Type {
	case "A_RECORD":
		if err := validateDNSIPv4(record.IPv4Address); err != nil {
			return err
		}
		return validateDNSTTL(record.TTLSeconds, 86400)
	case "AAAA_RECORD":
		if err := validateDNSIPv6(record.IPv6Address); err != nil {
			return err
		}
		return validateDNSTTL(record.TTLSeconds, 86400)
	case "CNAME_RECORD":
		if err := validateDNSName(record.TargetDomain); err != nil {
			return apperr.New(apperr.ValidationFailed, "CNAME target domain is invalid")
		}
		return validateDNSTTL(record.TTLSeconds, 604800)
	case "MX_RECORD":
		if err := validateDNSName(record.MailServerDomain); err != nil {
			return apperr.New(apperr.ValidationFailed, "MX mail server domain is invalid")
		}
		return validateDNSUint16("MX priority", record.Priority)
	case "TXT_RECORD":
		if len(record.Text) < 1 || len(record.Text) > 1024 {
			return apperr.New(apperr.ValidationFailed, "DNS TXT text must contain 1 to 1024 characters")
		}
	case "SRV_RECORD":
		if err := validateDNSName(record.ServerDomain); err != nil {
			return apperr.New(apperr.ValidationFailed, "SRV server domain is invalid")
		}
		if err := validateDNSUnderscoreToken("service", record.Service); err != nil {
			return err
		}
		if err := validateDNSUnderscoreToken("protocol", record.Protocol); err != nil {
			return err
		}
		for label, value := range map[string]int{"SRV priority": record.Priority, "SRV port": record.Port, "SRV weight": record.Weight} {
			if err := validateDNSUint16(label, value); err != nil {
				return err
			}
		}
	case "FORWARD_DOMAIN":
		return validateDNSIP(record.IPAddress)
	}
	return nil
}

func validateDNSTTL(value, maximum int) error {
	if value < 1 || value > maximum {
		return apperr.Newf(apperr.ValidationFailed, "DNS TTL must be between 1 and %d seconds", maximum)
	}
	return nil
}

func validateDNSUint16(label string, value int) error {
	if value < 0 || value > 65535 {
		return apperr.Newf(apperr.ValidationFailed, "%s must be between 0 and 65535", label)
	}
	return nil
}

func validateDNSUnderscoreToken(label, value string) error {
	if len(value) < 2 || len(value) > 63 || value[0] != '_' || value[len(value)-1] == '-' {
		return apperr.Newf(apperr.ValidationFailed, "SRV %s must start with an underscore and contain 2 to 63 characters", label)
	}
	for _, char := range value[1:] {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return apperr.Newf(apperr.ValidationFailed, "SRV %s contains an invalid character", label)
	}
	return nil
}

func validateDNSSRVName(name string) error {
	if len(name) == 0 || len(name) > 127 {
		return apperr.New(apperr.ValidationFailed, "DNS name must contain 1 to 127 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if strings.HasPrefix(label, "_") {
			if err := validateDNSUnderscoreToken("name label", label); err != nil {
				return err
			}
			continue
		}
		if err := validateDNSName(label); err != nil {
			return err
		}
	}
	return nil
}

func inputSetsAnyDNSField(in DNSInput) bool {
	return in.Type != "" || inputSetsName(in) || inputSetsIP(in) || inputSetsIPv6(in) ||
		inputSetsTargetDomain(in) || inputSetsMailServerDomain(in) || inputSetsText(in) ||
		inputSetsServerDomain(in) || inputSetsServerIP(in) || in.SetPriority || in.Priority != 0 ||
		inputSetsService(in) || inputSetsProtocol(in) || in.SetPort || in.Port != 0 ||
		in.SetWeight || in.Weight != 0 || in.SetEnabled || inputSetsTTL(in)
}

func inputSetsName(in DNSInput) bool         { return in.SetName || in.Name != "" }
func inputSetsIP(in DNSInput) bool           { return in.SetIP || in.IP != "" }
func inputSetsTTL(in DNSInput) bool          { return in.SetTTL || in.TTLSeconds != 0 }
func inputSetsIPv6(in DNSInput) bool         { return in.SetIPv6Address || in.IPv6Address != "" }
func inputSetsTargetDomain(in DNSInput) bool { return in.SetTargetDomain || in.TargetDomain != "" }
func inputSetsMailServerDomain(in DNSInput) bool {
	return in.SetMailServerDomain || in.MailServerDomain != ""
}
func inputSetsText(in DNSInput) bool         { return in.SetText || in.Text != "" }
func inputSetsServerDomain(in DNSInput) bool { return in.SetServerDomain || in.ServerDomain != "" }
func inputSetsServerIP(in DNSInput) bool     { return in.SetServerIP || in.ServerIP != "" }
func inputSetsService(in DNSInput) bool      { return in.SetService || in.Service != "" }
func inputSetsProtocol(in DNSInput) bool     { return in.SetProtocol || in.Protocol != "" }

func effectiveDNSTTL(in DNSInput) int {
	if inputSetsTTL(in) {
		return in.TTLSeconds
	}
	return defaultDNSTTLSeconds
}

func dnsRecordsEqual(record, expected DNSRecord) bool {
	if record.ID != expected.ID {
		return false
	}
	recordBody, recordErr := dnsWritableBody(record)
	expectedBody, expectedErr := dnsWritableBody(expected)
	return recordErr == nil && expectedErr == nil && reflect.DeepEqual(recordBody, expectedBody)
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
