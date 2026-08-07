package domain

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

// FirewallAPI is the transport for classic rest/firewallrule.
type FirewallAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

// FirewallRule is a classic UniFi firewall rule.
type FirewallRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Action   string `json:"action"`
	Ruleset  string `json:"ruleset"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	Protocol string `json:"protocol"`
	Index    int    `json:"index"`
}

func (r FirewallRule) GetID() string   { return r.ID }
func (r FirewallRule) GetMAC() string  { return "" }
func (r FirewallRule) GetName() string { return r.Name }

// FirewallInput is create/update payload from CLI flags.
type FirewallInput struct {
	Name        string
	SetName     bool
	Enabled     bool
	SetEnabled  bool
	Action      string
	SetAction   bool
	Ruleset     string
	SetRuleset  bool
	Src         string
	SetSrc      bool
	ClearSrc    bool
	Dst         string
	SetDst      bool
	ClearDst    bool
	Protocol    string
	SetProtocol bool
	Index       int
	SetIndex    bool
}

// FirewallReorder selects full-order (--ids) or single-move (--id + --index).
type FirewallReorder struct {
	IDs      []string
	ID       string
	Index    int
	SetIndex bool
}

type FirewallService struct {
	api FirewallAPI
}

func NewFirewallService(api FirewallAPI) *FirewallService {
	return &FirewallService{api: api}
}

func (s *FirewallService) List(ctx context.Context) ([]FirewallRule, error) {
	raw, err := s.fetchRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FirewallRule, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeFirewallRule(m))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *FirewallService) Get(ctx context.Context, id string) (FirewallRule, error) {
	items, err := s.List(ctx)
	if err != nil {
		return FirewallRule{}, err
	}
	return resolve.One(items, id)
}

func (s *FirewallService) listLegacy(ctx context.Context) ([]FirewallRule, error) {
	raw, err := s.fetchLegacyRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FirewallRule, 0, len(raw))
	for _, item := range raw {
		out = append(out, NormalizeFirewallRule(item))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *FirewallService) getLegacy(ctx context.Context, id string) (FirewallRule, error) {
	items, err := s.listLegacy(ctx)
	if err != nil {
		return FirewallRule{}, err
	}
	if item, ok := findExactID(items, id); ok {
		return item, nil
	}
	if !looksLikeUUID(id) {
		return resolve.One(items, id)
	}
	raw, official, err := fetchOfficialSite(s.api, ctx, "firewall", "policies")
	if err != nil {
		return FirewallRule{}, err
	}
	if !official {
		return resolve.One(items, id)
	}
	officialItems := make([]FirewallRule, 0, len(raw))
	for _, item := range raw {
		officialItems = append(officialItems, NormalizeFirewallRule(item))
	}
	return resolveLegacyMutationTarget(items, officialItems, id, "firewall policy", func(a, b FirewallRule) bool { return sameName(a, b) })
}

func (s *FirewallService) Create(ctx context.Context, in FirewallInput) (plan.Plan, error) {
	_ = ctx
	if err := validateFirewallCreate(in); err != nil {
		return plan.Plan{}, err
	}
	if !in.SetEnabled {
		in.Enabled = true
	}
	p := plan.Create("firewall", in.Name,
		fmt.Sprintf("create firewall rule %s", in.Name),
		firewallSnapshotFromInput(in),
	)
	return p, nil
}

func (s *FirewallService) ApplyCreate(ctx context.Context, in FirewallInput) (FirewallRule, error) {
	if err := validateFirewallCreate(in); err != nil {
		return FirewallRule{}, err
	}
	path := s.api.SitePath(client.PathRestFirewall)
	if !in.SetEnabled {
		in.Enabled = true
		in.SetEnabled = true
	}
	body := firewallInputBody(in)
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return FirewallRule{}, err
	}
	if len(raw) > 0 {
		return NormalizeFirewallRule(raw[0]), nil
	}
	return FirewallRule{
		Name: in.Name, Enabled: in.Enabled, Action: in.Action, Ruleset: in.Ruleset,
		Src: in.Src, Dst: in.Dst, Protocol: in.Protocol, Index: in.Index,
	}, nil
}

func (s *FirewallService) Update(ctx context.Context, id string, in FirewallInput) (plan.Plan, FirewallRule, error) {
	if err := validateFirewallUpdate(in); err != nil {
		return plan.Plan{}, FirewallRule{}, err
	}
	r, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, FirewallRule{}, err
	}
	before := firewallSnapshot(r)
	after := mergeFirewallAfter(r, in)
	p := plan.Update("firewall", r.ID, r.Name,
		fmt.Sprintf("update firewall rule %s", r.Name),
		before,
		after,
	)
	return p, r, nil
}

func (s *FirewallService) ApplyUpdate(ctx context.Context, id string, in FirewallInput) (FirewallRule, error) {
	if err := validateFirewallUpdate(in); err != nil {
		return FirewallRule{}, err
	}
	r, err := s.getLegacy(ctx, id)
	if err != nil {
		return FirewallRule{}, err
	}
	path := s.api.SitePath(client.PathRestFirewall, r.ID)
	body := firewallInputBodyMerged(r, in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return FirewallRule{}, err
	}
	return applyFirewallInput(r, in), nil
}

func (s *FirewallService) Delete(ctx context.Context, id string) (plan.Plan, FirewallRule, error) {
	r, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, FirewallRule{}, err
	}
	p := plan.Delete("firewall", r.ID, r.Name,
		fmt.Sprintf("delete firewall rule %s", r.Name),
		firewallSnapshot(r),
	)
	return p, r, nil
}

func (s *FirewallService) ApplyDelete(ctx context.Context, id string) (FirewallRule, error) {
	r, err := s.getLegacy(ctx, id)
	if err != nil {
		return FirewallRule{}, err
	}
	path := s.api.SitePath(client.PathRestFirewall, r.ID)
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return FirewallRule{}, err
	}
	return r, nil
}

func (s *FirewallService) Reorder(ctx context.Context, ro FirewallReorder) (plan.Plan, error) {
	order, before, err := s.resolveReorder(ctx, ro)
	if err != nil {
		return plan.Plan{}, err
	}
	p := plan.Update("firewall", "", "rules",
		fmt.Sprintf("reorder firewall rules (%d)", len(order)),
		map[string]any{"order": before},
		map[string]any{"order": order},
	)
	return p, nil
}

func (s *FirewallService) ApplyReorder(ctx context.Context, ro FirewallReorder) error {
	order, _, err := s.resolveReorder(ctx, ro)
	if err != nil {
		return err
	}
	items, err := s.listLegacy(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]FirewallRule, len(items))
	for _, r := range items {
		byID[r.ID] = r
	}
	// Preserve relative index spacing when possible: use sequential indices
	// starting at the minimum existing index among the ordered set.
	base := 2000
	if len(items) > 0 {
		base = items[0].Index
	}
	for i, id := range order {
		r, ok := byID[id]
		if !ok {
			return apperr.Newf(apperr.NotFound, "firewall rule %q not found", id)
		}
		newIndex := base + i*10
		if r.Index == newIndex {
			continue
		}
		path := s.api.SitePath(client.PathRestFirewall, r.ID)
		body := map[string]any{
			"name":       r.Name,
			"enabled":    r.Enabled,
			"action":     r.Action,
			"ruleset":    r.Ruleset,
			"protocol":   r.Protocol,
			"rule_index": newIndex,
		}
		if r.Src != "" {
			body["src_address"] = r.Src
		}
		if r.Dst != "" {
			body["dst_address"] = r.Dst
		}
		if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *FirewallService) resolveReorder(ctx context.Context, ro FirewallReorder) (order []string, before []string, err error) {
	items, err := s.listLegacy(ctx)
	if err != nil {
		return nil, nil, err
	}
	before = make([]string, 0, len(items))
	for _, r := range items {
		before = append(before, r.ID)
	}

	switch {
	case len(ro.IDs) > 0:
		if ro.SetIndex || ro.ID != "" {
			return nil, nil, apperr.New(apperr.ValidationFailed, "use either --ids or --id/--index, not both")
		}
		seen := make(map[string]struct{}, len(ro.IDs))
		byID := make(map[string]struct{}, len(items))
		for _, r := range items {
			byID[r.ID] = struct{}{}
		}
		for _, id := range ro.IDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := byID[id]; !ok {
				// allow resolve by name
				hit, err := resolve.One(items, id)
				if err != nil {
					return nil, nil, err
				}
				id = hit.ID
			}
			if _, dup := seen[id]; dup {
				return nil, nil, apperr.Newf(apperr.ValidationFailed, "duplicate id in --ids: %s", id)
			}
			seen[id] = struct{}{}
			order = append(order, id)
		}
		if len(order) == 0 {
			return nil, nil, apperr.New(apperr.ValidationFailed, "--ids requires at least one rule id")
		}
		for _, id := range before {
			if _, ok := seen[id]; ok {
				continue
			}
			order = append(order, id)
		}
		return order, before, nil

	case ro.SetIndex && ro.ID != "":
		if ro.Index < 0 {
			return nil, nil, apperr.New(apperr.ValidationFailed, "--index must be >= 0")
		}
		hit, err := resolve.One(items, ro.ID)
		if err != nil {
			return nil, nil, err
		}
		// Build order from current sorted list, move hit to position Index.
		rest := make([]string, 0, len(items)-1)
		for _, r := range items {
			if r.ID == hit.ID {
				continue
			}
			rest = append(rest, r.ID)
		}
		idx := ro.Index
		if idx > len(rest) {
			idx = len(rest)
		}
		order = make([]string, 0, len(items))
		order = append(order, rest[:idx]...)
		order = append(order, hit.ID)
		order = append(order, rest[idx:]...)
		return order, before, nil

	default:
		return nil, nil, apperr.New(apperr.ValidationFailed, "reorder requires --ids id1,id2,... or --id X --index N")
	}
}

func (s *FirewallService) fetchRules(ctx context.Context) ([]map[string]any, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "firewall", "policies")
	if err != nil {
		return nil, err
	}
	if !official {
		path := s.api.SitePath(client.PathRestFirewall)
		if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

func (s *FirewallService) fetchLegacyRules(ctx context.Context) ([]map[string]any, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestFirewall), nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func NormalizeFirewallRule(m map[string]any) FirewallRule {
	r := FirewallRule{
		ID: strField(m, "_id", "id"), Name: strField(m, "name"), Enabled: boolFieldDefault(m, "enabled", true),
		Action: strField(m, "action"), Ruleset: strField(m, "ruleset"),
		Src:      strField(m, "src_address", "src", "src_ip", "src_networkconf_id"),
		Dst:      strField(m, "dst_address", "dst", "dst_ip", "dst_networkconf_id"),
		Protocol: strField(m, "protocol"), Index: intField(m, "rule_index", "index"),
	}
	if action, ok := m["action"].(map[string]any); ok {
		switch strings.ToUpper(strField(action, "type")) {
		case "ALLOW":
			r.Action = "accept"
		case "BLOCK":
			r.Action = "drop"
		case "REJECT":
			r.Action = "reject"
		}
	}
	if scope, ok := m["ipProtocolScope"].(map[string]any); ok {
		r.Protocol = normalizeOfficialFirewallProtocol(scope)
	}
	return r
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
		if named, ok := filter["protocol"].(map[string]any); ok {
			protocol = strings.ToLower(strField(named, "name"))
		}
	case "PRESET":
		if preset, ok := filter["preset"].(map[string]any); ok {
			protocol = strings.ToLower(strField(preset, "name"))
		}
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

func intField(m map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		if n, ok := asInt(v); ok {
			return n
		}
	}
	return 0
}

func firewallInputBody(in FirewallInput) map[string]any {
	enabled := in.Enabled
	if !in.SetEnabled {
		enabled = true
	}
	body := map[string]any{
		"enabled": enabled,
	}
	if inputSetsFirewallName(in) {
		body["name"] = in.Name
	}
	if inputSetsFirewallAction(in) {
		body["action"] = in.Action
	}
	if inputSetsFirewallRuleset(in) {
		body["ruleset"] = in.Ruleset
	}
	if inputSetsFirewallSrc(in) {
		body["src_address"] = in.Src
	}
	if inputSetsFirewallDst(in) {
		body["dst_address"] = in.Dst
	}
	if inputSetsFirewallProtocol(in) {
		body["protocol"] = in.Protocol
	}
	if in.SetIndex || in.Index != 0 {
		body["rule_index"] = in.Index
	}
	return body
}

func firewallInputBodyMerged(r FirewallRule, in FirewallInput) map[string]any {
	merged := applyFirewallInput(r, in)
	body := map[string]any{
		"name":       merged.Name,
		"enabled":    merged.Enabled,
		"action":     merged.Action,
		"ruleset":    merged.Ruleset,
		"protocol":   merged.Protocol,
		"rule_index": merged.Index,
	}
	if merged.Src != "" {
		body["src_address"] = merged.Src
	}
	if merged.Dst != "" {
		body["dst_address"] = merged.Dst
	}
	return body
}

func applyFirewallInput(r FirewallRule, in FirewallInput) FirewallRule {
	if inputSetsFirewallName(in) {
		r.Name = in.Name
	}
	if in.SetEnabled {
		r.Enabled = in.Enabled
	}
	if inputSetsFirewallAction(in) {
		r.Action = in.Action
	}
	if inputSetsFirewallRuleset(in) {
		r.Ruleset = in.Ruleset
	}
	if inputSetsFirewallSrc(in) {
		r.Src = in.Src
	}
	if in.ClearSrc {
		r.Src = ""
	}
	if inputSetsFirewallDst(in) {
		r.Dst = in.Dst
	}
	if in.ClearDst {
		r.Dst = ""
	}
	if inputSetsFirewallProtocol(in) {
		r.Protocol = in.Protocol
	}
	if in.SetIndex {
		r.Index = in.Index
	}
	return r
}

func firewallSnapshot(r FirewallRule) map[string]any {
	return map[string]any{
		"id": r.ID, "name": r.Name, "enabled": r.Enabled, "action": r.Action,
		"ruleset": r.Ruleset, "src": r.Src, "dst": r.Dst, "protocol": r.Protocol, "index": r.Index,
	}
}

func firewallSnapshotFromInput(in FirewallInput) map[string]any {
	enabled := in.Enabled
	if !in.SetEnabled {
		enabled = true
	}
	return map[string]any{
		"name": in.Name, "enabled": enabled, "action": in.Action, "ruleset": in.Ruleset,
		"src": in.Src, "dst": in.Dst, "protocol": in.Protocol, "index": in.Index,
	}
}

func mergeFirewallAfter(r FirewallRule, in FirewallInput) map[string]any {
	return firewallSnapshot(applyFirewallInput(r, in))
}

func validateFirewallCreate(in FirewallInput) error {
	if err := validateRequired("firewall name", in.Name); err != nil {
		return err
	}
	if err := validateRequired("firewall action", in.Action); err != nil {
		return err
	}
	if err := validateRequired("firewall ruleset", in.Ruleset); err != nil {
		return err
	}
	return validateFirewallFields(in)
}

func validateFirewallUpdate(in FirewallInput) error {
	if !inputSetsFirewallName(in) && !in.SetEnabled && !inputSetsFirewallAction(in) && !inputSetsFirewallRuleset(in) &&
		!inputSetsFirewallSrc(in) && !in.ClearSrc && !inputSetsFirewallDst(in) && !in.ClearDst &&
		!inputSetsFirewallProtocol(in) && !in.SetIndex {
		return apperr.New(apperr.ValidationFailed, "firewall update requires at least one changed field")
	}
	if (in.ClearSrc && inputSetsFirewallSrc(in)) || (in.ClearDst && inputSetsFirewallDst(in)) {
		return apperr.New(apperr.ValidationFailed, "set and clear flags are mutually exclusive")
	}
	if inputSetsFirewallName(in) {
		if err := validateRequired("firewall name", in.Name); err != nil {
			return err
		}
	}
	return validateFirewallFields(in)
}

func validateFirewallFields(in FirewallInput) error {
	if err := validateEnum("firewall action", in.Action, "accept", "drop", "reject"); err != nil {
		return err
	}
	if err := validateEnum("firewall protocol", in.Protocol, "all", "tcp", "udp", "tcp_udp", "icmp", "icmpv6"); err != nil {
		return err
	}
	if err := validateIPOrCIDR("firewall source", in.Src); err != nil {
		return err
	}
	if err := validateIPOrCIDR("firewall destination", in.Dst); err != nil {
		return err
	}
	if in.SetIndex && in.Index < 0 {
		return apperr.New(apperr.ValidationFailed, "firewall index must be at least 0")
	}
	return nil
}

func inputSetsFirewallName(in FirewallInput) bool     { return in.SetName || in.Name != "" }
func inputSetsFirewallAction(in FirewallInput) bool   { return in.SetAction || in.Action != "" }
func inputSetsFirewallRuleset(in FirewallInput) bool  { return in.SetRuleset || in.Ruleset != "" }
func inputSetsFirewallSrc(in FirewallInput) bool      { return in.SetSrc || in.Src != "" }
func inputSetsFirewallDst(in FirewallInput) bool      { return in.SetDst || in.Dst != "" }
func inputSetsFirewallProtocol(in FirewallInput) bool { return in.SetProtocol || in.Protocol != "" }
