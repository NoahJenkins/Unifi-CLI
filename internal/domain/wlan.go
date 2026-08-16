package domain

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type WlanAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Wlan struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Security  string `json:"security"`
	NetworkID string `json:"network_id"`
	Band      string `json:"band"`
	Guest     bool   `json:"guest"`
}

func (w Wlan) GetID() string   { return w.ID }
func (w Wlan) GetMAC() string  { return "" }
func (w Wlan) GetName() string { return w.Name }

// WlanInput is the create/update payload from CLI flags.
type WlanInput struct {
	Name                               string
	SetName                            bool
	Security                           string
	SetSecurity                        bool
	Network                            string
	SetNetwork                         bool
	Password                           string
	SetPassword                        bool
	Band                               string
	SetBand                            bool
	Guest                              bool
	SetGuest                           bool
	Enabled                            bool
	SetEnabled                         bool
	PMFMode                            string
	SetPMFMode                         bool
	SAEAnticloggingThresholdSeconds    int
	SetSAEAnticloggingThresholdSeconds bool
	SAESyncTimeSeconds                 int
	SetSAESyncTimeSeconds              bool
	FastRoamingEnabled                 bool
	SetFastRoamingEnabled              bool
	WPA3FastRoamingEnabled             bool
	SetWPA3FastRoamingEnabled          bool
	RadiusProfileID                    string
	SetRadiusProfileID                 bool
	RadiusNASIDSource                  string
	SetRadiusNASIDSource               bool
	RadiusNASID                        string
	SetRadiusNASID                     bool
	COAEnabled                         bool
	SetCOAEnabled                      bool
	WPA3SecurityMode                   string
	SetWPA3SecurityMode                bool
}

type WlanService struct {
	api WlanAPI
}

type wlanDocument struct {
	normalized Wlan
	wire       map[string]any
}

func NewWlanService(api WlanAPI) *WlanService {
	return &WlanService{api: api}
}

func (s *WlanService) List(ctx context.Context) ([]Wlan, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "wifi", "broadcasts")
	if err != nil {
		return nil, err
	}
	if !official {
		path := s.api.SitePath(client.PathRestWlan)
		if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
	}
	out := make([]Wlan, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeWlan(m))
	}
	return out, nil
}

func (s *WlanService) Get(ctx context.Context, id string) (Wlan, error) {
	items, err := s.List(ctx)
	if err != nil {
		return Wlan{}, err
	}
	overview, err := resolve.One(items, id)
	if err != nil {
		return Wlan{}, err
	}
	if !supportsOfficialDetails(s.api) || !looksLikeUUID(overview.ID) {
		return overview, nil
	}
	detail, err := fetchOfficialSiteDetail(s.api, ctx, overview.ID, "wifi", "broadcasts")
	if err != nil {
		return Wlan{}, err
	}
	return NormalizeWlan(detail), nil
}

func (s *WlanService) listLegacy(ctx context.Context) ([]Wlan, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestWlan), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Wlan, 0, len(raw))
	for _, item := range raw {
		out = append(out, NormalizeWlan(item))
	}
	return out, nil
}

func (s *WlanService) getLegacy(ctx context.Context, id string) (Wlan, error) {
	items, err := s.listLegacy(ctx)
	if err != nil {
		return Wlan{}, err
	}
	if item, ok := findExactID(items, id); ok {
		return item, nil
	}
	if !looksLikeUUID(id) {
		return resolve.One(items, id)
	}
	raw, official, err := fetchOfficialSite(s.api, ctx, "wifi", "broadcasts")
	if err != nil {
		return Wlan{}, err
	}
	if !official {
		return resolve.One(items, id)
	}
	officialItems := make([]Wlan, 0, len(raw))
	for _, item := range raw {
		officialItems = append(officialItems, NormalizeWlan(item))
	}
	return resolveLegacyMutationTarget(items, officialItems, id, "WLAN", func(a, b Wlan) bool { return sameName(a, b) })
}

func (s *WlanService) Create(ctx context.Context, in WlanInput) (plan.Plan, error) {
	_ = ctx
	if err := validateWlanCreate(in); err != nil {
		return plan.Plan{}, err
	}
	after := wlanPlanAfter(in)
	p := plan.Create("wlan", in.Name,
		fmt.Sprintf("create wlan %s", in.Name),
		after,
	)
	return p, nil
}

func (s *WlanService) ApplyCreate(ctx context.Context, in WlanInput) (Wlan, error) {
	if err := validateWlanCreate(in); err != nil {
		return Wlan{}, err
	}
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialCreate(ctx, in)
	}
	path := s.api.SitePath(client.PathRestWlan)
	body := wlanInputBody(in)
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodPost, path, body, &raw); err != nil {
		return Wlan{}, err
	}
	if len(raw) > 0 {
		return NormalizeWlan(raw[0]), nil
	}
	return Wlan{
		Name:      in.Name,
		Enabled:   true,
		Security:  in.Security,
		NetworkID: in.Network,
		Band:      in.Band,
		Guest:     in.Guest,
	}, nil
}

func (s *WlanService) Update(ctx context.Context, id string, in WlanInput) (plan.Plan, Wlan, error) {
	if err := validateWlanUpdate(in); err != nil {
		return plan.Plan{}, Wlan{}, err
	}
	if supportsOfficialDetails(s.api) {
		doc, body, err := s.prepareOfficialUpdate(ctx, id, in)
		if err != nil {
			return plan.Plan{}, Wlan{}, err
		}
		beforeSnapshot, afterSnapshot := officialWlanUpdateSnapshots(doc.wire, wlanResponseView(body, doc.wire), in)
		p := plan.Update("wlan", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update wlan %s", doc.normalized.Name), beforeSnapshot, afterSnapshot)
		return p, doc.normalized, nil
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Wlan{}, err
	}
	if err := validateWlanSecurityTransition(w, in); err != nil {
		return plan.Plan{}, Wlan{}, err
	}
	before := wlanSnapshot(w)
	after := mergeWlanAfter(w, in)
	p := plan.Update("wlan", w.ID, w.Name,
		fmt.Sprintf("update wlan %s", w.Name),
		before,
		after,
	)
	return p, w, nil
}

func (s *WlanService) ApplyUpdate(ctx context.Context, id string, in WlanInput) (Wlan, error) {
	return s.applyUpdate(ctx, id, in, nil)
}

func (s *WlanService) ApplyUpdatePrepared(ctx context.Context, target plan.Target, id string, in WlanInput) (Wlan, error) {
	return s.applyUpdate(ctx, id, in, &target)
}

func (s *WlanService) applyUpdate(ctx context.Context, id string, in WlanInput, target *plan.Target) (Wlan, error) {
	if err := validateWlanUpdate(in); err != nil {
		return Wlan{}, err
	}
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialUpdate(ctx, id, in, target)
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return Wlan{}, err
	}
	if err := validateWlanSecurityTransition(w, in); err != nil {
		return Wlan{}, err
	}
	if target != nil {
		p := plan.Update("wlan", w.ID, w.Name, fmt.Sprintf("update wlan %s", w.Name), wlanSnapshot(w), mergeWlanAfter(w, in))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Wlan{}, err
		}
	}
	path := s.api.SitePath(client.PathRestWlan, w.ID)
	body := wlanInputBody(in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Wlan{}, err
	}
	if inputSetsWlanName(in) {
		w.Name = in.Name
	}
	if inputSetsWlanSecurity(in) {
		w.Security = in.Security
	}
	if inputSetsWlanNetwork(in) {
		w.NetworkID = in.Network
	}
	if inputSetsWlanBand(in) {
		w.Band = in.Band
	}
	if in.SetGuest {
		w.Guest = in.Guest
	}
	return w, nil
}

func validateWlanSecurityTransition(current Wlan, in WlanInput) error {
	if strings.EqualFold(current.Security, "open") && inputSetsWlanSecurity(in) {
		target, err := canonicalOfficialWlanSecurity(in.Security)
		if err != nil {
			return err
		}
		if officialWlanSecurityIsPersonal(target) && in.Password == "" {
			return apperr.New(apperr.ValidationFailed, "securing an open WLAN with personal security requires a password")
		}
	}
	return nil
}

func (s *WlanService) Delete(ctx context.Context, id string) (plan.Plan, Wlan, error) {
	if supportsOfficialDetails(s.api) {
		doc, err := s.resolveOfficialDocument(ctx, id)
		if err != nil {
			return plan.Plan{}, Wlan{}, err
		}
		p := plan.Delete("wlan", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("delete wlan %s", doc.normalized.Name), wlanSnapshot(doc.normalized))
		return p, doc.normalized, nil
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Wlan{}, err
	}
	p := plan.Delete("wlan", w.ID, w.Name,
		fmt.Sprintf("delete wlan %s", w.Name),
		wlanSnapshot(w),
	)
	return p, w, nil
}

func (s *WlanService) ApplyDelete(ctx context.Context, id string) (Wlan, error) {
	return s.applyDelete(ctx, id, nil)
}

func (s *WlanService) ApplyDeletePrepared(ctx context.Context, target plan.Target, id string) (Wlan, error) {
	return s.applyDelete(ctx, id, &target)
}

func (s *WlanService) applyDelete(ctx context.Context, id string, target *plan.Target) (Wlan, error) {
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialDelete(ctx, id, target)
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return Wlan{}, err
	}
	if target != nil {
		p := plan.Delete("wlan", w.ID, w.Name, fmt.Sprintf("delete wlan %s", w.Name), wlanSnapshot(w))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Wlan{}, err
		}
	}
	path := s.api.SitePath(client.PathRestWlan, w.ID)
	if err := s.api.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return Wlan{}, err
	}
	return w, nil
}

func (s *WlanService) Enable(ctx context.Context, id string) (plan.Plan, Wlan, error) {
	return s.setEnabledPlan(ctx, id, true)
}

func (s *WlanService) ApplyEnable(ctx context.Context, id string) (Wlan, error) {
	return s.applySetEnabled(ctx, id, true, nil)
}

func (s *WlanService) ApplyEnablePrepared(ctx context.Context, target plan.Target, id string) (Wlan, error) {
	return s.applySetEnabled(ctx, id, true, &target)
}

func (s *WlanService) Disable(ctx context.Context, id string) (plan.Plan, Wlan, error) {
	return s.setEnabledPlan(ctx, id, false)
}

func (s *WlanService) ApplyDisable(ctx context.Context, id string) (Wlan, error) {
	return s.applySetEnabled(ctx, id, false, nil)
}

func (s *WlanService) ApplyDisablePrepared(ctx context.Context, target plan.Target, id string) (Wlan, error) {
	return s.applySetEnabled(ctx, id, false, &target)
}

func (s *WlanService) setEnabledPlan(ctx context.Context, id string, enabled bool) (plan.Plan, Wlan, error) {
	if supportsOfficialDetails(s.api) {
		p, w, err := s.Update(ctx, id, WlanInput{Enabled: enabled, SetEnabled: true})
		if err != nil {
			return plan.Plan{}, Wlan{}, err
		}
		action := "enable"
		if !enabled {
			action = "disable"
		}
		p.Summary = fmt.Sprintf("%s wlan %s", action, w.Name)
		return p, w, nil
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Wlan{}, err
	}
	action := "enable"
	if !enabled {
		action = "disable"
	}
	before := map[string]any{"enabled": w.Enabled}
	after := map[string]any{"enabled": enabled}
	p := plan.Update("wlan", w.ID, w.Name,
		fmt.Sprintf("%s wlan %s", action, w.Name),
		before,
		after,
	)
	return p, w, nil
}

func (s *WlanService) applySetEnabled(ctx context.Context, id string, enabled bool, target *plan.Target) (Wlan, error) {
	if supportsOfficialDetails(s.api) {
		return s.applyUpdate(ctx, id, WlanInput{Enabled: enabled, SetEnabled: true}, target)
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return Wlan{}, err
	}
	if target != nil {
		action := "enable"
		if !enabled {
			action = "disable"
		}
		p := plan.Update("wlan", w.ID, w.Name, fmt.Sprintf("%s wlan %s", action, w.Name),
			map[string]any{"enabled": w.Enabled}, map[string]any{"enabled": enabled})
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Wlan{}, err
		}
	}
	path := s.api.SitePath(client.PathRestWlan, w.ID)
	body := map[string]any{"enabled": enabled}
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Wlan{}, err
	}
	w.Enabled = enabled
	return w, nil
}

func NormalizeWlan(m map[string]any) Wlan {
	w := Wlan{
		ID:        strField(m, "_id", "id"),
		Name:      strField(m, "name"),
		Enabled:   boolField(m, "enabled"),
		Security:  strField(m, "security"),
		NetworkID: strField(m, "networkconf_id", "network_id"),
		Band:      strField(m, "wlan_band", "band"),
		Guest:     boolField(m, "is_guest"),
	}
	if security, ok := m["securityConfiguration"].(map[string]any); ok {
		w.Security = strings.ToLower(strField(security, "type"))
	}
	if network, ok := m["network"].(map[string]any); ok {
		w.NetworkID = strField(network, "networkId")
	}
	if frequencies := numberSlice(m["broadcastingFrequenciesGHz"]); len(frequencies) > 0 {
		w.Band = officialBand(frequencies)
	}
	if _, ok := m["hotspotConfiguration"].(map[string]any); ok {
		w.Guest = true
	}
	return w
}

func (s *WlanService) resolveOfficialDocument(ctx context.Context, query string) (wlanDocument, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "wifi", "broadcasts")
	if err != nil {
		return wlanDocument{}, err
	}
	if !official {
		return wlanDocument{}, apperr.New(apperr.Internal, "official WLAN transport is unavailable")
	}
	items := make([]Wlan, 0, len(raw))
	for _, item := range raw {
		items = append(items, NormalizeWlan(item))
	}
	selected, err := resolve.One(items, query)
	if err != nil {
		return wlanDocument{}, err
	}
	if !looksLikeUUID(selected.ID) {
		return wlanDocument{}, apperr.New(apperr.Conflict, "official WLAN target has an invalid ID")
	}
	wire, err := fetchOfficialSiteDetail(s.api, ctx, selected.ID, "wifi", "broadcasts")
	if err != nil {
		return wlanDocument{}, err
	}
	if strField(wire, "id") != selected.ID {
		return wlanDocument{}, apperr.New(apperr.Conflict, "official WLAN detail returned an ambiguous ID")
	}
	return wlanDocument{normalized: NormalizeWlan(wire), wire: deepCloneMap(wire)}, nil
}

func wlanWritableDocument(raw map[string]any) map[string]any {
	body := deepCloneMap(raw)
	delete(body, "id")
	delete(body, "metadata")
	return body
}

var officialWlanSetPaths = map[string]struct{}{
	"broadcastingFrequenciesGHz":                           {},
	"clientFilteringPolicy.macAddressFilter":               {},
	"broadcastingDeviceFilter.deviceIds":                   {},
	"broadcastingDeviceFilter.deviceTagIds":                {},
	"mdnsProxyConfiguration.policies.*.bridgingNetworkIds": {},
	"multicastFilteringPolicy.sourceMacAddressFilter":      {},
}

func wlanWireDocumentsEqual(a, b any) bool {
	return wireDocumentsEqualAtPaths(a, b, officialWlanSetPaths)
}

func wlanResponseView(body, existing map[string]any) map[string]any {
	view := deepCloneMap(body)
	for _, key := range []string{"id", "metadata"} {
		if value, ok := existing[key]; ok {
			view[key] = deepCloneValue(value)
		}
	}
	return view
}

func (s *WlanService) applyOfficialCreate(ctx context.Context, in WlanInput) (Wlan, error) {
	transport, err := requireOfficialMutationAPI(s.api)
	if err != nil {
		return Wlan{}, err
	}
	body, err := officialWlanCreateBody(in)
	if err != nil {
		return Wlan{}, err
	}
	path, err := transport.IntegrationSitePath(ctx, "wifi", "broadcasts")
	if err != nil {
		return Wlan{}, err
	}
	var created map[string]any
	if err := transport.DoOfficial(ctx, http.MethodPost, path, body, &created); err != nil {
		return Wlan{}, err
	}
	id := strField(created, "id")
	if !looksLikeUUID(id) {
		return Wlan{}, apperr.New(apperr.Conflict, "WLAN create result is unverified: controller response is missing a valid broadcast ID")
	}
	observed, err := fetchOfficialSiteDetail(s.api, ctx, id, "wifi", "broadcasts")
	if err != nil {
		return Wlan{}, verificationError("created WLAN could not be verified", err)
	}
	if err := requireObservedResourceID(observed, id, "WLAN create"); err != nil {
		return Wlan{}, err
	}
	if !wlanWireDocumentsEqual(wlanWritableDocument(observed), body) {
		return Wlan{}, apperr.New(apperr.Conflict, "WLAN create verification failed: observed writable document differs from requested state")
	}
	return NormalizeWlan(observed), nil
}

func (s *WlanService) prepareOfficialUpdate(ctx context.Context, query string, in WlanInput) (wlanDocument, map[string]any, error) {
	doc, err := s.resolveOfficialDocument(ctx, query)
	if err != nil {
		return wlanDocument{}, nil, err
	}
	if err := validateWlanSecurityTransition(doc.normalized, in); err != nil {
		return wlanDocument{}, nil, err
	}
	if inputSetsWlanPassword(in) {
		if err := validateOfficialWlanPassphrase(in.Password); err != nil {
			return wlanDocument{}, nil, err
		}
	}
	body := wlanWritableDocument(doc.wire)
	if inputSetsWlanName(in) {
		body["name"] = in.Name
	}
	if in.SetEnabled {
		body["enabled"] = in.Enabled
	}
	if inputSetsWlanNetwork(in) {
		body["network"] = map[string]any{"type": "SPECIFIC", "networkId": in.Network}
	}
	if inputSetsWlanBand(in) {
		frequencies, err := officialWlanFrequencies(in.Band)
		if err != nil {
			return wlanDocument{}, nil, err
		}
		body["broadcastingFrequenciesGHz"] = frequencies
	}
	if in.SetGuest {
		if in.Guest {
			body["hotspotConfiguration"] = map[string]any{"type": "CAPTIVE_PORTAL"}
		} else {
			delete(body, "hotspotConfiguration")
		}
	}
	if inputSetsWlanSecurity(in) || inputSetsWlanPassword(in) || inputSetsWlanAdvancedSecurity(in) {
		current, ok := body["securityConfiguration"].(map[string]any)
		if !ok || current == nil {
			return wlanDocument{}, nil, apperr.New(apperr.Conflict, "WLAN has a malformed security configuration")
		}
		security, err := updateOfficialWlanSecurity(current, in)
		if err != nil {
			return wlanDocument{}, nil, err
		}
		body["securityConfiguration"] = security
	}
	if wlanWireDocumentsEqual(body, wlanWritableDocument(doc.wire)) {
		return wlanDocument{}, nil, apperr.New(apperr.ValidationFailed, "WLAN update would not change controller state")
	}
	return doc, body, nil
}

func updateOfficialWlanSecurity(current map[string]any, in WlanInput) (map[string]any, error) {
	currentType := strings.ToUpper(strField(current, "type"))
	targetType := currentType
	if inputSetsWlanSecurity(in) {
		var err error
		targetType, err = canonicalOfficialWlanSecurity(in.Security)
		if err != nil {
			return nil, err
		}
		if targetType != currentType {
			return officialWlanSecurity(in)
		}
	}
	security := deepCloneMap(current)
	if inputSetsWlanPassword(in) {
		if !officialWlanSecurityIsPersonal(targetType) {
			return nil, apperr.New(apperr.ValidationFailed, "only personal WLAN security accepts a passphrase")
		}
		if err := validateOfficialWlanPassphrase(in.Password); err != nil {
			return nil, err
		}
		security["passphrase"] = in.Password
	}
	if in.SetPMFMode {
		if !officialWlanSecuritySupportsPMF(targetType) {
			return nil, apperr.New(apperr.ValidationFailed, "PMF mode is not configurable for this WLAN security type")
		}
		pmf, err := canonicalWlanPMFMode(in.PMFMode)
		if err != nil {
			return nil, err
		}
		security["pmfMode"] = pmf
	}
	if in.SetFastRoamingEnabled {
		if targetType == "OPEN" {
			return nil, apperr.New(apperr.ValidationFailed, "fast roaming is not a field of open WLAN security")
		}
		security["fastRoamingEnabled"] = in.FastRoamingEnabled
	}
	if in.SetWPA3FastRoamingEnabled {
		if !officialWlanSecurityIsTransition(targetType) {
			return nil, apperr.New(apperr.ValidationFailed, "WPA3 fast roaming applies only to WPA2/WPA3 transition security")
		}
		security["wpa3FastRoamingEnabled"] = in.WPA3FastRoamingEnabled
	}
	if in.SetSAEAnticloggingThresholdSeconds || in.SetSAESyncTimeSeconds {
		if !officialWlanSecurityNeedsSAE(targetType) {
			return nil, apperr.New(apperr.ValidationFailed, "SAE settings apply only to WPA3 personal security")
		}
		sae, _ := security["saeConfiguration"].(map[string]any)
		if sae == nil {
			return nil, apperr.New(apperr.Conflict, "WPA3 personal security has no SAE configuration")
		}
		sae = deepCloneMap(sae)
		if in.SetSAEAnticloggingThresholdSeconds {
			sae["anticloggingThresholdSeconds"] = in.SAEAnticloggingThresholdSeconds
		}
		if in.SetSAESyncTimeSeconds {
			sae["syncTimeSeconds"] = in.SAESyncTimeSeconds
		}
		security["saeConfiguration"] = sae
	}
	if in.SetRadiusProfileID || in.SetRadiusNASIDSource || in.SetRadiusNASID {
		if !officialWlanSecurityIsEnterprise(targetType) {
			return nil, apperr.New(apperr.ValidationFailed, "RADIUS settings apply only to enterprise WLAN security")
		}
		radius, _ := security["radiusConfiguration"].(map[string]any)
		if radius == nil {
			return nil, apperr.New(apperr.Conflict, "enterprise WLAN security has no RADIUS configuration")
		}
		radius = deepCloneMap(radius)
		if in.SetRadiusProfileID {
			profileID := strings.TrimSpace(in.RadiusProfileID)
			if !looksLikeUUID(profileID) {
				return nil, apperr.New(apperr.ValidationFailed, "RADIUS profile ID must be a valid UUID")
			}
			radius["profileId"] = profileID
		}
		if in.SetRadiusNASIDSource && in.SetRadiusNASID {
			return nil, apperr.New(apperr.ValidationFailed, "choose either a derived or user-defined RADIUS NAS ID")
		}
		if in.SetRadiusNASIDSource {
			source, err := canonicalWlanRadiusNASIDSource(in.RadiusNASIDSource)
			if err != nil {
				return nil, err
			}
			radius["nasId"] = map[string]any{"type": "DERIVED", "source": source}
		}
		if in.SetRadiusNASID {
			value := strings.TrimSpace(in.RadiusNASID)
			if value == "" {
				return nil, apperr.New(apperr.ValidationFailed, "RADIUS NAS ID cannot be empty")
			}
			radius["nasId"] = map[string]any{"type": "USER_DEFINED", "value": value}
		}
		security["radiusConfiguration"] = radius
	}
	if in.SetCOAEnabled {
		if !officialWlanSecurityIsEnterprise(targetType) {
			return nil, apperr.New(apperr.ValidationFailed, "COA applies only to enterprise WLAN security")
		}
		security["coaEnabled"] = in.COAEnabled
	}
	if in.SetWPA3SecurityMode {
		if targetType != "WPA3_ENTERPRISE" {
			return nil, apperr.New(apperr.ValidationFailed, "WPA3 security mode applies only to WPA3 enterprise security")
		}
		mode, err := canonicalWlanWPA3SecurityMode(in.WPA3SecurityMode)
		if err != nil {
			return nil, err
		}
		security["securityMode"] = mode
	}
	if err := validateOfficialWlanSecurityDocument(security); err != nil {
		return nil, err
	}
	return security, nil
}

func validateOfficialWlanSecurityDocument(security map[string]any) error {
	typ := strings.ToUpper(strField(security, "type"))
	if typ == "OPEN" {
		return nil
	}
	if officialWlanSecurityIsPersonal(typ) {
		if err := validateOfficialWlanPassphrase(strField(security, "passphrase")); err != nil {
			return apperr.New(apperr.Conflict, "controller WLAN has an invalid personal passphrase field")
		}
	}
	if officialWlanSecurityNeedsSAE(typ) {
		sae, _ := security["saeConfiguration"].(map[string]any)
		anti, sync := intField(sae, "anticloggingThresholdSeconds"), intField(sae, "syncTimeSeconds")
		if sae == nil || anti < 1 || anti > 60 || sync < 1 || sync > 60 {
			return apperr.New(apperr.Conflict, "controller WLAN has an invalid SAE configuration")
		}
	}
	if officialWlanSecurityIsTransition(typ) {
		if _, ok := security["wpa3FastRoamingEnabled"]; !ok || strField(security, "pmfMode") == "" {
			return apperr.New(apperr.Conflict, "controller WLAN transition security is missing required PMF or roaming fields")
		}
		if boolField(security, "wpa3FastRoamingEnabled") && !boolField(security, "fastRoamingEnabled") {
			return apperr.New(apperr.ValidationFailed, "WPA3 fast roaming requires default fast roaming to be enabled")
		}
	}
	if officialWlanSecurityIsEnterprise(typ) {
		if _, ok := security["coaEnabled"]; !ok {
			return apperr.New(apperr.Conflict, "controller enterprise WLAN is missing COA configuration")
		}
		radius, _ := security["radiusConfiguration"].(map[string]any)
		if radius == nil || !looksLikeUUID(strField(radius, "profileId")) || !validWlanRadiusNASID(radius["nasId"]) {
			return apperr.New(apperr.Conflict, "controller enterprise WLAN has an invalid RADIUS configuration")
		}
	}
	if typ == "WPA3_ENTERPRISE" {
		mode := strField(security, "securityMode")
		if mode != "DEFAULT" && mode != "HIGH_SECURITY_192_BIT" {
			return apperr.New(apperr.Conflict, "controller WPA3 enterprise WLAN has an invalid security mode")
		}
	}
	return nil
}

func validWlanRadiusNASID(value any) bool {
	nasID, _ := value.(map[string]any)
	switch strField(nasID, "type") {
	case "DERIVED":
		source := strField(nasID, "source")
		return source == "DEVICE_MAC_ADDRESS" || source == "DEVICE_NAME" || source == "SITE_NAME" || source == "BSSID"
	case "USER_DEFINED":
		return strings.TrimSpace(strField(nasID, "value")) != ""
	default:
		return false
	}
}

func (s *WlanService) applyOfficialUpdate(ctx context.Context, query string, in WlanInput, target *plan.Target) (Wlan, error) {
	doc, body, err := s.prepareOfficialUpdate(ctx, query, in)
	if err != nil {
		return Wlan{}, err
	}
	if target != nil {
		beforeSnapshot, afterSnapshot := officialWlanUpdateSnapshots(doc.wire, wlanResponseView(body, doc.wire), in)
		p := plan.Update("wlan", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update wlan %s", doc.normalized.Name), beforeSnapshot, afterSnapshot)
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Wlan{}, err
		}
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	path, err := transport.IntegrationSitePath(ctx, "wifi", "broadcasts", doc.normalized.ID)
	if err != nil {
		return Wlan{}, err
	}
	var response map[string]any
	if err := transport.DoOfficial(ctx, http.MethodPut, path, body, &response); err != nil {
		return Wlan{}, err
	}
	observed, err := fetchOfficialSiteDetail(s.api, ctx, doc.normalized.ID, "wifi", "broadcasts")
	if err != nil {
		return Wlan{}, verificationError("updated WLAN could not be verified", err)
	}
	if err := requireObservedResourceID(observed, doc.normalized.ID, "WLAN update"); err != nil {
		return Wlan{}, err
	}
	if !wlanWireDocumentsEqual(wlanWritableDocument(observed), body) {
		return Wlan{}, apperr.New(apperr.Conflict, "WLAN update verification failed: observed writable document differs from requested state")
	}
	return NormalizeWlan(observed), nil
}

func (s *WlanService) applyOfficialDelete(ctx context.Context, query string, target *plan.Target) (Wlan, error) {
	doc, err := s.resolveOfficialDocument(ctx, query)
	if err != nil {
		return Wlan{}, err
	}
	if target != nil {
		p := plan.Delete("wlan", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("delete wlan %s", doc.normalized.Name), wlanSnapshot(doc.normalized))
		if err := requirePreparedTarget(*target, p.Changes); err != nil {
			return Wlan{}, err
		}
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	path, err := transport.IntegrationSitePath(ctx, "wifi", "broadcasts", doc.normalized.ID)
	if err != nil {
		return Wlan{}, err
	}
	if err := transport.DoOfficial(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return Wlan{}, err
	}
	if _, err := fetchOfficialSiteDetail(s.api, ctx, doc.normalized.ID, "wifi", "broadcasts"); err == nil {
		return Wlan{}, apperr.New(apperr.Conflict, "WLAN delete verification failed: deleted broadcast is still present")
	} else if !apperr.Is(err, apperr.NotFound) {
		return Wlan{}, verificationError("deleted WLAN could not be verified", err)
	}
	return doc.normalized, nil
}

func officialWlanCreateBody(in WlanInput) (map[string]any, error) {
	security, err := officialWlanSecurity(in)
	if err != nil {
		return nil, err
	}
	band := in.Band
	if band == "" {
		band = "both"
	}
	frequencies, err := officialWlanFrequencies(band)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"type": "STANDARD", "name": in.Name, "enabled": true, "hideName": false,
		"clientIsolationEnabled": false, "multicastToUnicastConversionEnabled": false, "uapsdEnabled": true,
		"advertiseDeviceName": false, "arpProxyEnabled": false, "bssTransitionEnabled": true,
		"channel2gLockedTo6": false, "dtimPeriod2gLockedTo3": false,
		"broadcastingFrequenciesGHz": frequencies, "securityConfiguration": security,
		"network": map[string]any{"type": "NATIVE"},
	}
	if inputSetsWlanNetwork(in) {
		body["network"] = map[string]any{"type": "SPECIFIC", "networkId": in.Network}
	}
	if in.SetGuest && in.Guest {
		body["hotspotConfiguration"] = map[string]any{"type": "CAPTIVE_PORTAL"}
	}
	return body, nil
}

func officialWlanSecurity(in WlanInput) (map[string]any, error) {
	typ, err := canonicalOfficialWlanSecurity(in.Security)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"type": typ}
	if typ == "OPEN" {
		if inputSetsWlanPassword(in) || inputSetsWlanAdvancedSecurity(in) {
			return nil, apperr.New(apperr.ValidationFailed, "open WLAN security does not accept personal or enterprise security fields")
		}
		return body, nil
	}
	if officialWlanSecurityIsPersonal(typ) {
		if err := validateOfficialWlanPassphrase(in.Password); err != nil {
			return nil, err
		}
		body["passphrase"] = in.Password
	} else if inputSetsWlanPassword(in) {
		return nil, apperr.New(apperr.ValidationFailed, "enterprise WLAN security does not accept a personal passphrase")
	}
	if in.SetPMFMode {
		if !officialWlanSecuritySupportsPMF(typ) {
			return nil, apperr.New(apperr.ValidationFailed, "PMF mode is not configurable for this WLAN security type")
		}
		pmf, err := canonicalWlanPMFMode(in.PMFMode)
		if err != nil {
			return nil, err
		}
		body["pmfMode"] = pmf
	}
	if in.SetFastRoamingEnabled {
		body["fastRoamingEnabled"] = in.FastRoamingEnabled
	}
	if in.SetWPA3FastRoamingEnabled {
		if !officialWlanSecurityIsTransition(typ) {
			return nil, apperr.New(apperr.ValidationFailed, "WPA3 fast roaming applies only to WPA2/WPA3 transition security")
		}
		body["wpa3FastRoamingEnabled"] = in.WPA3FastRoamingEnabled
	}
	if in.WPA3FastRoamingEnabled && (!in.SetFastRoamingEnabled || !in.FastRoamingEnabled) {
		return nil, apperr.New(apperr.ValidationFailed, "WPA3 fast roaming requires default fast roaming to be enabled")
	}
	if officialWlanSecurityNeedsSAE(typ) {
		sae, err := officialWlanSAE(in)
		if err != nil {
			return nil, err
		}
		body["saeConfiguration"] = sae
	} else if in.SetSAEAnticloggingThresholdSeconds || in.SetSAESyncTimeSeconds {
		return nil, apperr.New(apperr.ValidationFailed, "SAE settings apply only to WPA3 personal security")
	}
	if officialWlanSecurityIsEnterprise(typ) {
		radius, err := officialWlanRadius(in)
		if err != nil {
			return nil, err
		}
		if !in.SetCOAEnabled {
			return nil, apperr.New(apperr.ValidationFailed, "enterprise WLAN security requires an explicit COA setting")
		}
		body["radiusConfiguration"] = radius
		body["coaEnabled"] = in.COAEnabled
	} else if in.SetRadiusProfileID || in.SetRadiusNASIDSource || in.SetRadiusNASID || in.SetCOAEnabled {
		return nil, apperr.New(apperr.ValidationFailed, "RADIUS and COA settings apply only to enterprise WLAN security")
	}
	if typ == "WPA2_WPA3_PERSONAL" || typ == "WPA2_WPA3_ENTERPRISE" {
		if !in.SetPMFMode || !in.SetWPA3FastRoamingEnabled {
			return nil, apperr.New(apperr.ValidationFailed, "WPA2/WPA3 transition security requires explicit PMF and WPA3 fast-roaming settings")
		}
	}
	if typ == "WPA3_ENTERPRISE" {
		if !in.SetWPA3SecurityMode {
			return nil, apperr.New(apperr.ValidationFailed, "WPA3 enterprise security requires an explicit security mode")
		}
		mode, err := canonicalWlanWPA3SecurityMode(in.WPA3SecurityMode)
		if err != nil {
			return nil, err
		}
		body["securityMode"] = mode
	} else if in.SetWPA3SecurityMode {
		return nil, apperr.New(apperr.ValidationFailed, "WPA3 security mode applies only to WPA3 enterprise security")
	}
	return body, nil
}

func canonicalOfficialWlanSecurity(value string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_")) {
	case "open":
		return "OPEN", nil
	case "wpapsk", "wpa2_personal":
		return "WPA2_PERSONAL", nil
	case "wpa3_personal":
		return "WPA3_PERSONAL", nil
	case "wpa2_wpa3_personal":
		return "WPA2_WPA3_PERSONAL", nil
	case "wpaeap", "wpa2_enterprise":
		return "WPA2_ENTERPRISE", nil
	case "wpa2_wpa3_enterprise":
		return "WPA2_WPA3_ENTERPRISE", nil
	case "wpa3_enterprise":
		return "WPA3_ENTERPRISE", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "WLAN security %q is not supported", value)
	}
}

func officialWlanSecurityIsPersonal(typ string) bool {
	return typ == "WPA2_PERSONAL" || typ == "WPA3_PERSONAL" || typ == "WPA2_WPA3_PERSONAL"
}

func officialWlanSecurityIsEnterprise(typ string) bool {
	return typ == "WPA2_ENTERPRISE" || typ == "WPA2_WPA3_ENTERPRISE" || typ == "WPA3_ENTERPRISE"
}

func officialWlanSecurityNeedsSAE(typ string) bool {
	return typ == "WPA3_PERSONAL" || typ == "WPA2_WPA3_PERSONAL"
}

func officialWlanSecurityIsTransition(typ string) bool {
	return typ == "WPA2_WPA3_PERSONAL" || typ == "WPA2_WPA3_ENTERPRISE"
}

func officialWlanSecuritySupportsPMF(typ string) bool {
	return typ == "WPA2_PERSONAL" || typ == "WPA2_WPA3_PERSONAL" ||
		typ == "WPA2_ENTERPRISE" || typ == "WPA2_WPA3_ENTERPRISE"
}

func canonicalWlanPMFMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "required":
		return "REQUIRED", nil
	case "optional":
		return "OPTIONAL", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "WLAN PMF mode %q is unsupported; use required or optional", value)
	}
}

func officialWlanSAE(in WlanInput) (map[string]any, error) {
	if !in.SetSAEAnticloggingThresholdSeconds || !in.SetSAESyncTimeSeconds {
		return nil, apperr.New(apperr.ValidationFailed, "WPA3 personal security requires explicit SAE anticlogging and sync times")
	}
	if in.SAEAnticloggingThresholdSeconds < 1 || in.SAEAnticloggingThresholdSeconds > 60 || in.SAESyncTimeSeconds < 1 || in.SAESyncTimeSeconds > 60 {
		return nil, apperr.New(apperr.ValidationFailed, "WLAN SAE times must be between 1 and 60 seconds")
	}
	return map[string]any{
		"anticloggingThresholdSeconds": in.SAEAnticloggingThresholdSeconds,
		"syncTimeSeconds":              in.SAESyncTimeSeconds,
	}, nil
}

func officialWlanRadius(in WlanInput) (map[string]any, error) {
	profileID := strings.TrimSpace(in.RadiusProfileID)
	if !in.SetRadiusProfileID || !looksLikeUUID(profileID) {
		return nil, apperr.New(apperr.ValidationFailed, "enterprise WLAN security requires a valid RADIUS profile ID")
	}
	if in.SetRadiusNASIDSource == in.SetRadiusNASID {
		return nil, apperr.New(apperr.ValidationFailed, "enterprise WLAN security requires exactly one derived or user-defined RADIUS NAS ID")
	}
	var nasID map[string]any
	if in.SetRadiusNASIDSource {
		source, err := canonicalWlanRadiusNASIDSource(in.RadiusNASIDSource)
		if err != nil {
			return nil, err
		}
		nasID = map[string]any{"type": "DERIVED", "source": source}
	} else {
		value := strings.TrimSpace(in.RadiusNASID)
		if value == "" {
			return nil, apperr.New(apperr.ValidationFailed, "RADIUS NAS ID cannot be empty")
		}
		nasID = map[string]any{"type": "USER_DEFINED", "value": value}
	}
	return map[string]any{"profileId": profileID, "nasId": nasID}, nil
}

func canonicalWlanRadiusNASIDSource(value string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")) {
	case "device-mac-address":
		return "DEVICE_MAC_ADDRESS", nil
	case "device-name":
		return "DEVICE_NAME", nil
	case "site-name":
		return "SITE_NAME", nil
	case "bssid":
		return "BSSID", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "RADIUS NAS ID source %q is unsupported", value)
	}
}

func canonicalWlanWPA3SecurityMode(value string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")) {
	case "default":
		return "DEFAULT", nil
	case "high-security-192-bit":
		return "HIGH_SECURITY_192_BIT", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "WPA3 security mode %q is unsupported", value)
	}
}

func validateOfficialWlanPassphrase(passphrase string) error {
	length := utf8.RuneCountInString(passphrase)
	if strings.TrimSpace(passphrase) == "" || length < 8 || length > 63 {
		return apperr.New(apperr.ValidationFailed, "personal WLAN passphrase must contain 8 to 63 characters")
	}
	return nil
}

func officialWlanFrequencies(band string) ([]any, error) {
	switch strings.ToLower(band) {
	case "2g", "2.4":
		return []any{2.4}, nil
	case "5g", "5":
		return []any{5.0}, nil
	case "6g", "6":
		return []any{6.0}, nil
	case "both":
		return []any{2.4, 5.0}, nil
	default:
		return nil, apperr.Newf(apperr.ValidationFailed, "WLAN band %q is unsupported", band)
	}
}

func numberSlice(v any) []float64 {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]float64, 0, len(raw))
	for _, item := range raw {
		if number, ok := item.(float64); ok {
			out = append(out, number)
		}
	}
	return out
}

func officialBand(frequencies []float64) string {
	labels := make([]string, 0, len(frequencies))
	for _, candidate := range []float64{2.4, 5, 6} {
		for _, frequency := range frequencies {
			if frequency == candidate {
				labels = append(labels, anyToString(candidate))
				break
			}
		}
	}
	return strings.Join(labels, "+")
}

func wlanInputBody(in WlanInput) map[string]any {
	body := map[string]any{}
	if inputSetsWlanName(in) {
		body["name"] = in.Name
	}
	if inputSetsWlanSecurity(in) {
		body["security"] = in.Security
	}
	if inputSetsWlanNetwork(in) {
		body["networkconf_id"] = in.Network
	}
	if inputSetsWlanPassword(in) {
		body["x_passphrase"] = in.Password
	}
	if inputSetsWlanBand(in) {
		body["wlan_band"] = in.Band
	}
	if in.SetGuest {
		body["is_guest"] = in.Guest
	}
	return body
}

// wlanPlanAfter builds the dry-run after map; password is always masked as ***.
func wlanPlanAfter(in WlanInput) map[string]any {
	after := map[string]any{}
	if inputSetsWlanName(in) {
		after["name"] = in.Name
	}
	if inputSetsWlanSecurity(in) {
		after["security"] = in.Security
	}
	if inputSetsWlanNetwork(in) {
		after["network"] = in.Network
	}
	if inputSetsWlanPassword(in) {
		after["password"] = "***"
	}
	if inputSetsWlanBand(in) {
		after["band"] = in.Band
	}
	if in.SetGuest {
		after["guest"] = in.Guest
	}
	appendWlanInputSecurityPlan(after, in)
	return after
}

func wlanSnapshot(w Wlan) map[string]any {
	return map[string]any{
		"id":         w.ID,
		"name":       w.Name,
		"enabled":    w.Enabled,
		"security":   w.Security,
		"network_id": w.NetworkID,
		"band":       w.Band,
		"guest":      w.Guest,
	}
}

func officialWlanSnapshot(raw map[string]any) map[string]any {
	snapshot := wlanSnapshot(NormalizeWlan(raw))
	security, _ := raw["securityConfiguration"].(map[string]any)
	if security == nil {
		return snapshot
	}
	appendWlanSecurityPlan(snapshot, security)
	return snapshot
}

func officialWlanUpdateSnapshots(beforeRaw, afterRaw map[string]any, in WlanInput) (map[string]any, map[string]any) {
	before := officialWlanSnapshot(beforeRaw)
	after := officialWlanSnapshot(afterRaw)
	if inputSetsWlanPassword(in) {
		after["password"] = "***"
	}
	return before, after
}

func appendWlanInputSecurityPlan(snapshot map[string]any, in WlanInput) {
	if in.SetPMFMode {
		snapshot["pmf_mode"] = strings.ToLower(in.PMFMode)
	}
	if in.SetSAEAnticloggingThresholdSeconds {
		snapshot["sae_anticlogging_threshold_seconds"] = in.SAEAnticloggingThresholdSeconds
	}
	if in.SetSAESyncTimeSeconds {
		snapshot["sae_sync_time_seconds"] = in.SAESyncTimeSeconds
	}
	if in.SetFastRoamingEnabled {
		snapshot["fast_roaming_enabled"] = in.FastRoamingEnabled
	}
	if in.SetWPA3FastRoamingEnabled {
		snapshot["wpa3_fast_roaming_enabled"] = in.WPA3FastRoamingEnabled
	}
	if in.SetRadiusProfileID {
		snapshot["radius_profile_id"] = in.RadiusProfileID
	}
	if in.SetRadiusNASIDSource {
		snapshot["radius_nas_id_source"] = strings.ToLower(strings.ReplaceAll(in.RadiusNASIDSource, "_", "-"))
	}
	if in.SetRadiusNASID {
		snapshot["radius_nas_id"] = in.RadiusNASID
	}
	if in.SetCOAEnabled {
		snapshot["coa_enabled"] = in.COAEnabled
	}
	if in.SetWPA3SecurityMode {
		snapshot["wpa3_security_mode"] = strings.ToLower(strings.ReplaceAll(in.WPA3SecurityMode, "_", "-"))
	}
}

func appendWlanSecurityPlan(snapshot map[string]any, security map[string]any) {
	if value := strField(security, "pmfMode"); value != "" {
		snapshot["pmf_mode"] = strings.ToLower(value)
	}
	if sae, ok := security["saeConfiguration"].(map[string]any); ok {
		snapshot["sae_anticlogging_threshold_seconds"] = intField(sae, "anticloggingThresholdSeconds")
		snapshot["sae_sync_time_seconds"] = intField(sae, "syncTimeSeconds")
	}
	if _, ok := security["fastRoamingEnabled"]; ok {
		snapshot["fast_roaming_enabled"] = boolField(security, "fastRoamingEnabled")
	}
	if _, ok := security["wpa3FastRoamingEnabled"]; ok {
		snapshot["wpa3_fast_roaming_enabled"] = boolField(security, "wpa3FastRoamingEnabled")
	}
	if radius, ok := security["radiusConfiguration"].(map[string]any); ok {
		snapshot["radius_profile_id"] = strField(radius, "profileId")
		if nasID, ok := radius["nasId"].(map[string]any); ok {
			if strField(nasID, "type") == "DERIVED" {
				snapshot["radius_nas_id_source"] = strings.ToLower(strings.ReplaceAll(strField(nasID, "source"), "_", "-"))
			} else if strField(nasID, "type") == "USER_DEFINED" {
				snapshot["radius_nas_id"] = strField(nasID, "value")
			}
		}
	}
	if _, ok := security["coaEnabled"]; ok {
		snapshot["coa_enabled"] = boolField(security, "coaEnabled")
	}
	if value := strField(security, "securityMode"); value != "" {
		snapshot["wpa3_security_mode"] = strings.ToLower(strings.ReplaceAll(value, "_", "-"))
	}
}

func mergeWlanAfter(w Wlan, in WlanInput) map[string]any {
	after := wlanSnapshot(w)
	if inputSetsWlanName(in) {
		after["name"] = in.Name
	}
	if inputSetsWlanSecurity(in) {
		after["security"] = in.Security
	}
	if inputSetsWlanNetwork(in) {
		after["network_id"] = in.Network
	}
	if inputSetsWlanPassword(in) {
		after["password"] = "***"
	}
	if inputSetsWlanBand(in) {
		after["band"] = in.Band
	}
	if in.SetGuest {
		after["guest"] = in.Guest
	}
	return after
}

func validateWlanCreate(in WlanInput) error {
	if err := validateRequired("WLAN name", in.Name); err != nil {
		return err
	}
	if err := validateRequired("WLAN security", in.Security); err != nil {
		return err
	}
	if err := validateWlanFields(in); err != nil {
		return err
	}
	if wlanSecurityRequiresPassword(in.Security) && strings.TrimSpace(in.Password) == "" {
		return apperr.New(apperr.ValidationFailed, "secured personal WLAN creation requires a password")
	}
	return nil
}

func validateWlanUpdate(in WlanInput) error {
	if !inputSetsWlanName(in) && !inputSetsWlanSecurity(in) && !inputSetsWlanNetwork(in) &&
		!inputSetsWlanPassword(in) && !inputSetsWlanBand(in) && !in.SetGuest && !in.SetEnabled && !inputSetsWlanAdvancedSecurity(in) {
		return apperr.New(apperr.ValidationFailed, "WLAN update requires at least one changed field")
	}
	if inputSetsWlanName(in) {
		if err := validateRequired("WLAN name", in.Name); err != nil {
			return err
		}
	}
	return validateWlanFields(in)
}

func validateWlanFields(in WlanInput) error {
	if inputSetsWlanSecurity(in) {
		if _, err := canonicalOfficialWlanSecurity(in.Security); err != nil {
			return err
		}
	}
	return validateEnum("WLAN band", in.Band, "2g", "5g", "6g", "both")
}

func inputSetsWlanName(in WlanInput) bool     { return in.SetName || in.Name != "" }
func inputSetsWlanSecurity(in WlanInput) bool { return in.SetSecurity || in.Security != "" }
func inputSetsWlanNetwork(in WlanInput) bool  { return in.SetNetwork || in.Network != "" }
func inputSetsWlanPassword(in WlanInput) bool { return in.SetPassword || in.Password != "" }
func inputSetsWlanBand(in WlanInput) bool     { return in.SetBand || in.Band != "" }

func inputSetsWlanAdvancedSecurity(in WlanInput) bool {
	return in.SetPMFMode || in.SetSAEAnticloggingThresholdSeconds || in.SetSAESyncTimeSeconds ||
		in.SetFastRoamingEnabled || in.SetWPA3FastRoamingEnabled || in.SetRadiusProfileID ||
		in.SetRadiusNASIDSource || in.SetRadiusNASID || in.SetCOAEnabled || in.SetWPA3SecurityMode
}

func wlanSecurityRequiresPassword(security string) bool {
	switch strings.ToLower(security) {
	case "wpapsk", "wpa2_personal", "wpa3_personal", "wpa2_wpa3_personal":
		return true
	default:
		return false
	}
}
