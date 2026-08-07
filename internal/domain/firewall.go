package domain

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

// FirewallAPI is the official local Network API transport required by the
// modern firewall policy and zone endpoints.
type FirewallAPI interface {
	FetchOfficialObjects(context.Context, string) ([]map[string]any, error)
	IntegrationSitePath(context.Context, ...string) (string, error)
	DoOfficial(context.Context, string, string, any, any) error
}

// FirewallZone is the normalized 10.3.58 firewall-zone document.
type FirewallZone struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	NetworkIDs   []string `json:"network_ids"`
	Origin       string   `json:"origin"`
	Configurable bool     `json:"configurable"`
}

func (z FirewallZone) GetID() string   { return z.ID }
func (z FirewallZone) GetMAC() string  { return "" }
func (z FirewallZone) GetName() string { return z.Name }

// FirewallRule preserves the stable policy identifiers while exposing the
// zone-aware fields from the official firewall policy schema.
type FirewallRule struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Enabled           bool   `json:"enabled"`
	Action            string `json:"action"`
	SourceZoneID      string `json:"source_zone_id"`
	DestinationZoneID string `json:"destination_zone_id"`
	Protocol          string `json:"protocol"`
	LoggingEnabled    bool   `json:"logging_enabled"`
	Index             int    `json:"index"`
	Origin            string `json:"origin"`
}

func (r FirewallRule) GetID() string   { return r.ID }
func (r FirewallRule) GetMAC() string  { return "" }
func (r FirewallRule) GetName() string { return r.Name }

// FirewallInput is the supported create/update surface for official policies.
// Optional official fields not exposed here are preserved verbatim on update.
type FirewallInput struct {
	Name                  string
	SetName               bool
	Description           string
	SetDescription        bool
	ClearDescription      bool
	Enabled               bool
	SetEnabled            bool
	Action                string
	SetAction             bool
	AllowReturnTraffic    bool
	SetAllowReturnTraffic bool
	SourceZone            string
	SetSourceZone         bool
	DestinationZone       string
	SetDestinationZone    bool
	IPVersion             string
	SetIPVersion          bool
	Protocol              string
	SetProtocol           bool
	LoggingEnabled        bool
	SetLoggingEnabled     bool
}

// FirewallReorder is the complete user-defined ordering for one zone pair.
type FirewallReorder struct {
	SourceZone          string
	DestinationZone     string
	BeforeSystemDefined []string
	AfterSystemDefined  []string
}

// FirewallOrdering mirrors the official atomic ordering document.
type FirewallOrdering struct {
	BeforeSystemDefined []string `json:"before_system_defined"`
	AfterSystemDefined  []string `json:"after_system_defined"`
}

type firewallOrderingWire struct {
	OrderedFirewallPolicyIDs struct {
		BeforeSystemDefined []string `json:"beforeSystemDefined"`
		AfterSystemDefined  []string `json:"afterSystemDefined"`
	} `json:"orderedFirewallPolicyIds"`
}

type firewallPolicyDocument struct {
	normalized FirewallRule
	wire       map[string]any
}

type resolvedFirewallReorder struct {
	sourceZoneID      string
	destinationZoneID string
	before            FirewallOrdering
	after             FirewallOrdering
}

type FirewallService struct {
	api FirewallAPI
}

func NewFirewallService(api FirewallAPI) *FirewallService {
	return &FirewallService{api: api}
}

func (s *FirewallService) ListZones(ctx context.Context) ([]FirewallZone, error) {
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "zones")
	if err != nil {
		return nil, err
	}
	raw, err := s.api.FetchOfficialObjects(ctx, path)
	if err != nil {
		return nil, err
	}
	zones := make([]FirewallZone, 0, len(raw))
	for _, item := range raw {
		zones = append(zones, NormalizeFirewallZone(item))
	}
	sort.SliceStable(zones, func(i, j int) bool {
		if zones[i].Name != zones[j].Name {
			return zones[i].Name < zones[j].Name
		}
		return zones[i].ID < zones[j].ID
	})
	return zones, nil
}

func (s *FirewallService) GetZone(ctx context.Context, query string) (FirewallZone, error) {
	zones, err := s.ListZones(ctx)
	if err != nil {
		return FirewallZone{}, err
	}
	zone, err := resolve.One(zones, query)
	if err != nil {
		return FirewallZone{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "zones", zone.ID)
	if err != nil {
		return FirewallZone{}, err
	}
	var raw map[string]any
	if err := s.api.DoOfficial(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return FirewallZone{}, err
	}
	return NormalizeFirewallZone(raw), nil
}

func NormalizeFirewallZone(m map[string]any) FirewallZone {
	metadata, _ := m["metadata"].(map[string]any)
	return FirewallZone{
		ID:           strField(m, "id"),
		Name:         strField(m, "name"),
		NetworkIDs:   firewallStringSlice(m["networkIds"]),
		Origin:       strField(metadata, "origin"),
		Configurable: boolField(metadata, "configurable"),
	}
}

func (s *FirewallService) List(ctx context.Context) ([]FirewallRule, error) {
	docs, err := s.listPolicyDocuments(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]FirewallRule, 0, len(docs))
	for _, doc := range docs {
		items = append(items, doc.normalized)
	}
	return items, nil
}

func (s *FirewallService) Get(ctx context.Context, query string) (FirewallRule, error) {
	doc, err := s.resolvePolicyDocument(ctx, query)
	if err != nil {
		return FirewallRule{}, err
	}
	return doc.normalized, nil
}

func (s *FirewallService) Create(ctx context.Context, in FirewallInput) (plan.Plan, error) {
	if err := validateFirewallCreate(in); err != nil {
		return plan.Plan{}, err
	}
	source, destination, err := s.resolveZonePair(ctx, in.SourceZone, in.DestinationZone)
	if err != nil {
		return plan.Plan{}, err
	}
	body := firewallCreateBody(in, source.ID, destination.ID)
	item := NormalizeFirewallRule(body)
	p := plan.Create("firewall", in.Name,
		fmt.Sprintf("create firewall policy %s", in.Name),
		firewallSnapshot(item),
	)
	return p, nil
}

func (s *FirewallService) ApplyCreate(ctx context.Context, in FirewallInput) (FirewallRule, error) {
	if err := validateFirewallCreate(in); err != nil {
		return FirewallRule{}, err
	}
	source, destination, err := s.resolveZonePair(ctx, in.SourceZone, in.DestinationZone)
	if err != nil {
		return FirewallRule{}, err
	}
	body := firewallCreateBody(in, source.ID, destination.ID)
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "policies")
	if err != nil {
		return FirewallRule{}, err
	}
	var created map[string]any
	if err := s.api.DoOfficial(ctx, http.MethodPost, path, body, &created); err != nil {
		return FirewallRule{}, err
	}
	id := strField(created, "id")
	if id == "" {
		return FirewallRule{}, apperr.New(apperr.Conflict, "firewall create result is unverified: controller response is missing the policy ID")
	}
	observed, observedRaw, err := s.readPolicyDetail(ctx, id)
	if err != nil {
		return FirewallRule{}, verificationError("created firewall policy could not be verified", err)
	}
	if !reflect.DeepEqual(firewallWritableDocument(observedRaw), body) {
		return FirewallRule{}, apperr.New(apperr.Conflict, "created firewall policy verification failed: observed writable document differs from requested state")
	}
	return observed, nil
}

func (s *FirewallService) Update(ctx context.Context, query string, in FirewallInput) (plan.Plan, FirewallRule, error) {
	doc, body, err := s.prepareUpdate(ctx, query, in)
	if err != nil {
		return plan.Plan{}, FirewallRule{}, err
	}
	after := NormalizeFirewallRule(firewallPolicyResponseView(body, doc.normalized))
	p := plan.Update("firewall", doc.normalized.ID, doc.normalized.Name,
		fmt.Sprintf("update firewall policy %s", doc.normalized.Name),
		firewallSnapshot(doc.normalized), firewallSnapshot(after),
	)
	return p, doc.normalized, nil
}

func (s *FirewallService) ApplyUpdate(ctx context.Context, query string, in FirewallInput) (FirewallRule, error) {
	doc, body, err := s.prepareUpdate(ctx, query, in)
	if err != nil {
		return FirewallRule{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "policies", doc.normalized.ID)
	if err != nil {
		return FirewallRule{}, err
	}
	var updated map[string]any
	if err := s.api.DoOfficial(ctx, http.MethodPut, path, body, &updated); err != nil {
		return FirewallRule{}, err
	}
	observed, observedRaw, err := s.readPolicyDetail(ctx, doc.normalized.ID)
	if err != nil {
		return FirewallRule{}, verificationError("updated firewall policy could not be verified", err)
	}
	if !reflect.DeepEqual(firewallWritableDocument(observedRaw), body) {
		return FirewallRule{}, apperr.New(apperr.Conflict, "updated firewall policy verification failed: observed writable document differs from requested state")
	}
	return observed, nil
}

func (s *FirewallService) Delete(ctx context.Context, query string) (plan.Plan, FirewallRule, error) {
	doc, err := s.resolvePolicyDocument(ctx, query)
	if err != nil {
		return plan.Plan{}, FirewallRule{}, err
	}
	p := plan.Delete("firewall", doc.normalized.ID, doc.normalized.Name,
		fmt.Sprintf("delete firewall policy %s", doc.normalized.Name),
		firewallSnapshot(doc.normalized),
	)
	return p, doc.normalized, nil
}

func (s *FirewallService) ApplyDelete(ctx context.Context, query string) (FirewallRule, error) {
	doc, err := s.resolvePolicyDocument(ctx, query)
	if err != nil {
		return FirewallRule{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "policies", doc.normalized.ID)
	if err != nil {
		return FirewallRule{}, err
	}
	if err := s.api.DoOfficial(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return FirewallRule{}, err
	}
	if _, _, err := s.readPolicyDetail(ctx, doc.normalized.ID); err == nil {
		return FirewallRule{}, apperr.New(apperr.Conflict, "firewall delete verification failed: deleted policy is still present")
	} else if !apperr.Is(err, apperr.NotFound) {
		return FirewallRule{}, verificationError("deleted firewall policy could not be verified", err)
	}
	return doc.normalized, nil
}

func (s *FirewallService) Reorder(ctx context.Context, in FirewallReorder) (plan.Plan, error) {
	resolved, err := s.resolveReorder(ctx, in)
	if err != nil {
		return plan.Plan{}, err
	}
	p := plan.Update("firewall", resolved.sourceZoneID+":"+resolved.destinationZoneID, "policy ordering",
		"reorder firewall policies",
		firewallOrderingSnapshot(resolved.sourceZoneID, resolved.destinationZoneID, resolved.before),
		firewallOrderingSnapshot(resolved.sourceZoneID, resolved.destinationZoneID, resolved.after),
	)
	return p, nil
}

func (s *FirewallService) ApplyReorder(ctx context.Context, in FirewallReorder) (FirewallOrdering, error) {
	resolved, err := s.resolveReorder(ctx, in)
	if err != nil {
		return FirewallOrdering{}, err
	}
	path, err := s.orderingPath(ctx, resolved.sourceZoneID, resolved.destinationZoneID)
	if err != nil {
		return FirewallOrdering{}, err
	}
	body := firewallOrderingBody(resolved.after)
	var response firewallOrderingWire
	if err := s.api.DoOfficial(ctx, http.MethodPut, path, body, &response); err != nil {
		return FirewallOrdering{}, err
	}
	observed, err := s.readOrdering(ctx, resolved.sourceZoneID, resolved.destinationZoneID)
	if err != nil {
		return FirewallOrdering{}, err
	}
	if !reflect.DeepEqual(observed, resolved.after) {
		return FirewallOrdering{}, apperr.New(apperr.Conflict, "firewall policy ordering verification mismatch")
	}
	return observed, nil
}

func (s *FirewallService) prepareUpdate(ctx context.Context, query string, in FirewallInput) (firewallPolicyDocument, map[string]any, error) {
	if err := validateFirewallUpdate(in); err != nil {
		return firewallPolicyDocument{}, nil, err
	}
	doc, err := s.resolvePolicyDocument(ctx, query)
	if err != nil {
		return firewallPolicyDocument{}, nil, err
	}
	body := firewallWritableDocument(doc.wire)
	if in.SetAllowReturnTraffic {
		action := doc.normalized.Action
		if inputSetsFirewallAction(in) {
			action = in.Action
		}
		if action != "allow" {
			return firewallPolicyDocument{}, nil, apperr.New(apperr.ValidationFailed, "allow-return-traffic applies only to action allow")
		}
	}

	if inputSetsFirewallName(in) {
		body["name"] = in.Name
	}
	if inputSetsFirewallDescription(in) {
		body["description"] = in.Description
	}
	if in.ClearDescription {
		delete(body, "description")
	}
	if in.SetEnabled {
		body["enabled"] = in.Enabled
	}
	if inputSetsFirewallAction(in) || in.SetAllowReturnTraffic {
		action := doc.normalized.Action
		if inputSetsFirewallAction(in) {
			action = in.Action
		}
		allowReturn := false
		if existing, ok := body["action"].(map[string]any); ok && action == "allow" {
			allowReturn = boolField(existing, "allowReturnTraffic")
		}
		if in.SetAllowReturnTraffic {
			allowReturn = in.AllowReturnTraffic
		}
		body["action"] = firewallActionBody(action, allowReturn)
	}
	if inputSetsFirewallSourceZone(in) || inputSetsFirewallDestinationZone(in) {
		var source, destination FirewallZone
		zones, err := s.ListZones(ctx)
		if err != nil {
			return firewallPolicyDocument{}, nil, err
		}
		if inputSetsFirewallSourceZone(in) {
			source, err = resolve.One(zones, in.SourceZone)
			if err != nil {
				return firewallPolicyDocument{}, nil, err
			}
		}
		if inputSetsFirewallDestinationZone(in) {
			destination, err = resolve.One(zones, in.DestinationZone)
			if err != nil {
				return firewallPolicyDocument{}, nil, err
			}
		}
		if source.ID != "" {
			endpoint, err := firewallEndpointForUpdate(body, "source")
			if err != nil {
				return firewallPolicyDocument{}, nil, err
			}
			endpoint["zoneId"] = source.ID
			body["source"] = endpoint
		}
		if destination.ID != "" {
			endpoint, err := firewallEndpointForUpdate(body, "destination")
			if err != nil {
				return firewallPolicyDocument{}, nil, err
			}
			endpoint["zoneId"] = destination.ID
			body["destination"] = endpoint
		}
	}
	if inputSetsFirewallIPVersion(in) || inputSetsFirewallProtocol(in) {
		if inputSetsFirewallIPVersion(in) && !inputSetsFirewallProtocol(in) {
			// Changing only the discriminator must not reconstruct (and thereby
			// narrow) an official protocol-number or future protocol filter.
			_, protocol := firewallProtocolParts(doc.wire)
			if err := validateFirewallIPProtocol(in.IPVersion, protocol); err != nil {
				return firewallPolicyDocument{}, nil, err
			}
			scope := deepCloneFirewallMap(mapField(doc.wire, "ipProtocolScope"))
			scope["ipVersion"] = strings.ToUpper(in.IPVersion)
			body["ipProtocolScope"] = scope
		} else {
			ipVersion, protocol := firewallProtocolParts(doc.wire)
			if inputSetsFirewallIPVersion(in) {
				ipVersion = in.IPVersion
			}
			if inputSetsFirewallProtocol(in) {
				protocol = in.Protocol
			}
			if err := validateFirewallIPProtocol(ipVersion, protocol); err != nil {
				return firewallPolicyDocument{}, nil, err
			}
			body["ipProtocolScope"] = firewallProtocolBody(ipVersion, protocol)
		}
	}
	if in.SetLoggingEnabled {
		body["loggingEnabled"] = in.LoggingEnabled
	}
	if reflect.DeepEqual(body, firewallWritableDocument(doc.wire)) {
		return firewallPolicyDocument{}, nil, apperr.New(apperr.ValidationFailed, "firewall update would not change controller state")
	}
	return doc, body, nil
}

func firewallEndpointForUpdate(body map[string]any, field string) (map[string]any, error) {
	endpoint, ok := body[field].(map[string]any)
	if !ok || endpoint == nil || strField(endpoint, "zoneId") == "" {
		return nil, apperr.Newf(apperr.Internal, "controller firewall policy has malformed %s endpoint", field)
	}
	return deepCloneFirewallMap(endpoint), nil
}

func (s *FirewallService) resolveReorder(ctx context.Context, in FirewallReorder) (resolvedFirewallReorder, error) {
	if err := validateRequired("source firewall zone", in.SourceZone); err != nil {
		return resolvedFirewallReorder{}, err
	}
	if err := validateRequired("destination firewall zone", in.DestinationZone); err != nil {
		return resolvedFirewallReorder{}, err
	}
	if len(in.BeforeSystemDefined)+len(in.AfterSystemDefined) == 0 {
		return resolvedFirewallReorder{}, apperr.New(apperr.ValidationFailed, "firewall reorder requires a complete policy order")
	}
	source, destination, err := s.resolveZonePair(ctx, in.SourceZone, in.DestinationZone)
	if err != nil {
		return resolvedFirewallReorder{}, err
	}
	current, err := s.readOrdering(ctx, source.ID, destination.ID)
	if err != nil {
		return resolvedFirewallReorder{}, err
	}
	docs, err := s.listPolicyDocuments(ctx)
	if err != nil {
		return resolvedFirewallReorder{}, err
	}
	currentIDs := append(append([]string(nil), current.BeforeSystemDefined...), current.AfterSystemDefined...)
	allowed := make(map[string]struct{}, len(currentIDs))
	for _, id := range currentIDs {
		allowed[id] = struct{}{}
	}
	candidates := make([]FirewallRule, 0, len(currentIDs))
	for _, doc := range docs {
		if _, ok := allowed[doc.normalized.ID]; ok && doc.normalized.SourceZoneID == source.ID && doc.normalized.DestinationZoneID == destination.ID {
			candidates = append(candidates, doc.normalized)
		}
	}
	resolveSegment := func(queries []string, seen map[string]struct{}) ([]string, error) {
		ids := make([]string, 0, len(queries))
		for _, query := range queries {
			item, err := resolve.One(candidates, query)
			if err != nil {
				return nil, err
			}
			if _, duplicate := seen[item.ID]; duplicate {
				return nil, apperr.Newf(apperr.ValidationFailed, "duplicate firewall policy in order: %s", item.ID)
			}
			seen[item.ID] = struct{}{}
			ids = append(ids, item.ID)
		}
		return ids, nil
	}
	seen := make(map[string]struct{}, len(currentIDs))
	before, err := resolveSegment(in.BeforeSystemDefined, seen)
	if err != nil {
		return resolvedFirewallReorder{}, err
	}
	after, err := resolveSegment(in.AfterSystemDefined, seen)
	if err != nil {
		return resolvedFirewallReorder{}, err
	}
	if len(seen) != len(allowed) {
		return resolvedFirewallReorder{}, apperr.New(apperr.ValidationFailed, "firewall reorder must include every user-defined policy in the zone pair exactly once")
	}
	for id := range allowed {
		if _, ok := seen[id]; !ok {
			return resolvedFirewallReorder{}, apperr.New(apperr.ValidationFailed, "firewall reorder must include every user-defined policy in the zone pair exactly once")
		}
	}
	desired := FirewallOrdering{BeforeSystemDefined: before, AfterSystemDefined: after}
	if reflect.DeepEqual(current, desired) {
		return resolvedFirewallReorder{}, apperr.New(apperr.ValidationFailed, "firewall reorder would not change controller state")
	}
	return resolvedFirewallReorder{
		sourceZoneID: source.ID, destinationZoneID: destination.ID,
		before: current, after: desired,
	}, nil
}

func (s *FirewallService) readOrdering(ctx context.Context, sourceZoneID, destinationZoneID string) (FirewallOrdering, error) {
	path, err := s.orderingPath(ctx, sourceZoneID, destinationZoneID)
	if err != nil {
		return FirewallOrdering{}, err
	}
	var wire firewallOrderingWire
	if err := s.api.DoOfficial(ctx, http.MethodGet, path, nil, &wire); err != nil {
		return FirewallOrdering{}, err
	}
	return FirewallOrdering{
		BeforeSystemDefined: append([]string(nil), wire.OrderedFirewallPolicyIDs.BeforeSystemDefined...),
		AfterSystemDefined:  append([]string(nil), wire.OrderedFirewallPolicyIDs.AfterSystemDefined...),
	}, nil
}

func (s *FirewallService) orderingPath(ctx context.Context, sourceZoneID, destinationZoneID string) (string, error) {
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "policies", "ordering")
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("sourceFirewallZoneId", sourceZoneID)
	values.Set("destinationFirewallZoneId", destinationZoneID)
	return path + "?" + values.Encode(), nil
}

func (s *FirewallService) resolveZonePair(ctx context.Context, sourceQuery, destinationQuery string) (FirewallZone, FirewallZone, error) {
	zones, err := s.ListZones(ctx)
	if err != nil {
		return FirewallZone{}, FirewallZone{}, err
	}
	source, err := resolve.One(zones, sourceQuery)
	if err != nil {
		return FirewallZone{}, FirewallZone{}, err
	}
	destination, err := resolve.One(zones, destinationQuery)
	if err != nil {
		return FirewallZone{}, FirewallZone{}, err
	}
	return source, destination, nil
}

func (s *FirewallService) listPolicyDocuments(ctx context.Context) ([]firewallPolicyDocument, error) {
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "policies")
	if err != nil {
		return nil, err
	}
	raw, err := s.api.FetchOfficialObjects(ctx, path)
	if err != nil {
		return nil, err
	}
	docs := make([]firewallPolicyDocument, 0, len(raw))
	for _, item := range raw {
		docs = append(docs, firewallPolicyDocument{normalized: NormalizeFirewallRule(item), wire: deepCloneFirewallMap(item)})
	}
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].normalized.Index != docs[j].normalized.Index {
			return docs[i].normalized.Index < docs[j].normalized.Index
		}
		if docs[i].normalized.Name != docs[j].normalized.Name {
			return docs[i].normalized.Name < docs[j].normalized.Name
		}
		return docs[i].normalized.ID < docs[j].normalized.ID
	})
	return docs, nil
}

func (s *FirewallService) resolvePolicyDocument(ctx context.Context, query string) (firewallPolicyDocument, error) {
	docs, err := s.listPolicyDocuments(ctx)
	if err != nil {
		return firewallPolicyDocument{}, err
	}
	items := make([]FirewallRule, 0, len(docs))
	byID := make(map[string]firewallPolicyDocument, len(docs))
	for _, doc := range docs {
		items = append(items, doc.normalized)
		byID[doc.normalized.ID] = doc
	}
	item, err := resolve.One(items, query)
	if err != nil {
		return firewallPolicyDocument{}, err
	}
	return byID[item.ID], nil
}

func (s *FirewallService) readPolicyDetail(ctx context.Context, id string) (FirewallRule, map[string]any, error) {
	path, err := s.api.IntegrationSitePath(ctx, "firewall", "policies", id)
	if err != nil {
		return FirewallRule{}, nil, err
	}
	var raw map[string]any
	if err := s.api.DoOfficial(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return FirewallRule{}, nil, err
	}
	return NormalizeFirewallRule(raw), raw, nil
}

func NormalizeFirewallRule(m map[string]any) FirewallRule {
	action, _ := m["action"].(map[string]any)
	source, _ := m["source"].(map[string]any)
	destination, _ := m["destination"].(map[string]any)
	metadata, _ := m["metadata"].(map[string]any)
	return FirewallRule{
		ID:                strField(m, "id"),
		Name:              strField(m, "name"),
		Description:       strField(m, "description"),
		Enabled:           boolField(m, "enabled"),
		Action:            strings.ToLower(strField(action, "type")),
		SourceZoneID:      strField(source, "zoneId"),
		DestinationZoneID: strField(destination, "zoneId"),
		Protocol:          normalizeOfficialFirewallProtocol(mapField(m, "ipProtocolScope")),
		LoggingEnabled:    boolField(m, "loggingEnabled"),
		Index:             intField(m, "index"),
		Origin:            strField(metadata, "origin"),
	}
}

func normalizeOfficialFirewallProtocol(scope map[string]any) string {
	ipVersion := strings.ToLower(strField(scope, "ipVersion"))
	filter, ok := scope["protocolFilter"].(map[string]any)
	if !ok {
		return ipVersion
	}
	var protocol string
	switch strings.ToUpper(strField(filter, "type")) {
	case "NAMED_PROTOCOL":
		protocol = strings.ToLower(strField(mapField(filter, "protocol"), "name"))
	case "PRESET":
		protocol = strings.ToLower(strField(mapField(filter, "preset"), "name"))
	case "PROTOCOL_NUMBER":
		protocol = strField(filter, "protocolNumber")
	}
	if protocol == "" {
		return ipVersion
	}
	if boolField(filter, "matchOpposite") {
		protocol = "not(" + protocol + ")"
	}
	if ipVersion == "" {
		return protocol
	}
	return ipVersion + ":" + protocol
}

func firewallCreateBody(in FirewallInput, sourceZoneID, destinationZoneID string) map[string]any {
	ipVersion := in.IPVersion
	if ipVersion == "" {
		ipVersion = "ipv4_and_ipv6"
	}
	protocol := in.Protocol
	if protocol == "" {
		protocol = "all"
	}
	enabled := true
	if in.SetEnabled {
		enabled = in.Enabled
	}
	body := map[string]any{
		"name":            in.Name,
		"enabled":         enabled,
		"action":          firewallActionBody(in.Action, in.AllowReturnTraffic),
		"source":          map[string]any{"zoneId": sourceZoneID},
		"destination":     map[string]any{"zoneId": destinationZoneID},
		"ipProtocolScope": firewallProtocolBody(ipVersion, protocol),
		"loggingEnabled":  in.LoggingEnabled,
	}
	if inputSetsFirewallDescription(in) {
		body["description"] = in.Description
	}
	return body
}

func firewallActionBody(action string, allowReturnTraffic bool) map[string]any {
	body := map[string]any{"type": strings.ToUpper(action)}
	if action == "allow" {
		body["allowReturnTraffic"] = allowReturnTraffic
	}
	return body
}

func firewallProtocolBody(ipVersion, protocol string) map[string]any {
	scope := map[string]any{"ipVersion": strings.ToUpper(ipVersion)}
	switch protocol {
	case "", "all":
		return scope
	case "tcp_udp":
		scope["protocolFilter"] = map[string]any{"type": "PRESET", "preset": map[string]any{"name": "TCP_UDP"}}
	default:
		scope["protocolFilter"] = map[string]any{
			"type": "NAMED_PROTOCOL", "protocol": map[string]any{"name": protocol}, "matchOpposite": false,
		}
	}
	return scope
}

func firewallProtocolParts(wire map[string]any) (string, string) {
	scope := mapField(wire, "ipProtocolScope")
	ipVersion := strings.ToLower(strField(scope, "ipVersion"))
	protocol := "all"
	if filter, ok := scope["protocolFilter"].(map[string]any); ok {
		switch strings.ToUpper(strField(filter, "type")) {
		case "NAMED_PROTOCOL":
			protocol = strings.ToLower(strField(mapField(filter, "protocol"), "name"))
		case "PRESET":
			protocol = strings.ToLower(strField(mapField(filter, "preset"), "name"))
		}
	}
	return ipVersion, protocol
}

func firewallWritableDocument(raw map[string]any) map[string]any {
	body := deepCloneFirewallMap(raw)
	delete(body, "id")
	delete(body, "index")
	delete(body, "metadata")
	return body
}

func firewallPolicyResponseView(body map[string]any, existing FirewallRule) map[string]any {
	view := deepCloneFirewallMap(body)
	view["id"] = existing.ID
	view["index"] = existing.Index
	view["metadata"] = map[string]any{"origin": existing.Origin}
	return view
}

func firewallOrderingBody(ordering FirewallOrdering) map[string]any {
	return map[string]any{"orderedFirewallPolicyIds": map[string]any{
		"beforeSystemDefined": append([]string(nil), ordering.BeforeSystemDefined...),
		"afterSystemDefined":  append([]string(nil), ordering.AfterSystemDefined...),
	}}
}

func firewallSnapshot(r FirewallRule) map[string]any {
	return map[string]any{
		"id": r.ID, "name": r.Name, "description": r.Description, "enabled": r.Enabled, "action": r.Action,
		"source_zone_id": r.SourceZoneID, "destination_zone_id": r.DestinationZoneID,
		"protocol": r.Protocol, "logging_enabled": r.LoggingEnabled, "index": r.Index, "origin": r.Origin,
	}
}

func firewallOrderingSnapshot(sourceZoneID, destinationZoneID string, ordering FirewallOrdering) map[string]any {
	return map[string]any{
		"source_zone_id": sourceZoneID, "destination_zone_id": destinationZoneID,
		"before_system_defined": append([]string(nil), ordering.BeforeSystemDefined...),
		"after_system_defined":  append([]string(nil), ordering.AfterSystemDefined...),
	}
}

func validateFirewallCreate(in FirewallInput) error {
	if err := validateRequired("firewall policy name", in.Name); err != nil {
		return err
	}
	if err := validateRequired("firewall policy action", in.Action); err != nil {
		return err
	}
	if err := validateRequired("source firewall zone", in.SourceZone); err != nil {
		return err
	}
	if err := validateRequired("destination firewall zone", in.DestinationZone); err != nil {
		return err
	}
	if (in.SetAllowReturnTraffic || in.AllowReturnTraffic) && in.Action != "allow" {
		return apperr.New(apperr.ValidationFailed, "allow-return-traffic applies only to action allow")
	}
	return validateFirewallFields(in, true)
}

func validateFirewallUpdate(in FirewallInput) error {
	if !inputSetsFirewallName(in) && !inputSetsFirewallDescription(in) && !in.ClearDescription && !in.SetEnabled &&
		!inputSetsFirewallAction(in) && !in.SetAllowReturnTraffic && !inputSetsFirewallSourceZone(in) &&
		!inputSetsFirewallDestinationZone(in) && !inputSetsFirewallIPVersion(in) && !inputSetsFirewallProtocol(in) && !in.SetLoggingEnabled {
		return apperr.New(apperr.ValidationFailed, "firewall update requires at least one changed field")
	}
	if in.ClearDescription && inputSetsFirewallDescription(in) {
		return apperr.New(apperr.ValidationFailed, "--description and --clear-description are mutually exclusive")
	}
	if inputSetsFirewallName(in) {
		if err := validateRequired("firewall policy name", in.Name); err != nil {
			return err
		}
	}
	if inputSetsFirewallSourceZone(in) {
		if err := validateRequired("source firewall zone", in.SourceZone); err != nil {
			return err
		}
	}
	if inputSetsFirewallDestinationZone(in) {
		if err := validateRequired("destination firewall zone", in.DestinationZone); err != nil {
			return err
		}
	}
	if in.SetAllowReturnTraffic && inputSetsFirewallAction(in) && in.Action != "allow" {
		return apperr.New(apperr.ValidationFailed, "allow-return-traffic applies only to action allow")
	}
	return validateFirewallFields(in, false)
}

func validateFirewallFields(in FirewallInput, create bool) error {
	if err := validateEnum("firewall action", in.Action, "allow", "block", "reject"); err != nil {
		return err
	}
	ipVersion := in.IPVersion
	protocol := in.Protocol
	if create {
		if ipVersion == "" {
			ipVersion = "ipv4_and_ipv6"
		}
		if protocol == "" {
			protocol = "all"
		}
	}
	if ipVersion != "" {
		if err := validateEnum("firewall IP version", ipVersion, "ipv4", "ipv6", "ipv4_and_ipv6"); err != nil {
			return err
		}
	}
	if protocol != "" {
		if err := validateEnum("firewall protocol", protocol, "all", "tcp", "udp", "tcp_udp", "icmp", "icmpv6"); err != nil {
			return err
		}
	}
	if ipVersion != "" && protocol != "" {
		return validateFirewallIPProtocol(ipVersion, protocol)
	}
	return nil
}

func validateFirewallIPProtocol(ipVersion, protocol string) error {
	switch {
	case ipVersion == "ipv4" && protocol == "icmpv6":
		return apperr.New(apperr.ValidationFailed, "icmpv6 is not valid for IPv4 firewall policies")
	case ipVersion == "ipv6" && protocol == "icmp":
		return apperr.New(apperr.ValidationFailed, "icmp is not valid for IPv6 firewall policies")
	case ipVersion == "ipv4_and_ipv6" && (protocol == "icmp" || protocol == "icmpv6"):
		return apperr.New(apperr.ValidationFailed, "ICMP protocols require a single IP version")
	}
	return nil
}

func inputSetsFirewallName(in FirewallInput) bool { return in.SetName || in.Name != "" }
func inputSetsFirewallDescription(in FirewallInput) bool {
	return in.SetDescription || in.Description != ""
}
func inputSetsFirewallAction(in FirewallInput) bool { return in.SetAction || in.Action != "" }
func inputSetsFirewallSourceZone(in FirewallInput) bool {
	return in.SetSourceZone || in.SourceZone != ""
}
func inputSetsFirewallDestinationZone(in FirewallInput) bool {
	return in.SetDestinationZone || in.DestinationZone != ""
}
func inputSetsFirewallIPVersion(in FirewallInput) bool { return in.SetIPVersion || in.IPVersion != "" }
func inputSetsFirewallProtocol(in FirewallInput) bool  { return in.SetProtocol || in.Protocol != "" }

func firewallStringSlice(value any) []string {
	if value == nil {
		return []string{}
	}
	values, ok := value.([]any)
	if !ok {
		if stringsValue, ok := value.([]string); ok {
			return append([]string(nil), stringsValue...)
		}
		return []string{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func mapField(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	value, _ := m[key].(map[string]any)
	return value
}

func intField(m map[string]any, keys ...string) int {
	for _, key := range keys {
		if value, ok := asInt(m[key]); ok {
			return value
		}
	}
	return 0
}

func deepCloneFirewallMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = deepCloneFirewallValue(value)
	}
	return out
}

func deepCloneFirewallValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return deepCloneFirewallMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = deepCloneFirewallValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return typed
	}
}
