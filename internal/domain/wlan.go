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
	Name        string
	SetName     bool
	Security    string
	SetSecurity bool
	Network     string
	SetNetwork  bool
	Password    string
	SetPassword bool
	Band        string
	SetBand     bool
	Guest       bool
	SetGuest    bool
	Enabled     bool
	SetEnabled  bool
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
		after := NormalizeWlan(wlanResponseView(body, doc.wire))
		p := plan.Update("wlan", doc.normalized.ID, doc.normalized.Name,
			fmt.Sprintf("update wlan %s", doc.normalized.Name), wlanSnapshot(doc.normalized), wlanSnapshot(after))
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
	if err := validateWlanUpdate(in); err != nil {
		return Wlan{}, err
	}
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialUpdate(ctx, id, in)
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return Wlan{}, err
	}
	if err := validateWlanSecurityTransition(w, in); err != nil {
		return Wlan{}, err
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
	if strings.EqualFold(current.Security, "open") && inputSetsWlanSecurity(in) &&
		!strings.EqualFold(in.Security, "open") && in.Password == "" {
		return apperr.New(apperr.ValidationFailed, "securing an open WLAN requires a password")
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
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialDelete(ctx, id)
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return Wlan{}, err
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
	return s.applySetEnabled(ctx, id, true)
}

func (s *WlanService) Disable(ctx context.Context, id string) (plan.Plan, Wlan, error) {
	return s.setEnabledPlan(ctx, id, false)
}

func (s *WlanService) ApplyDisable(ctx context.Context, id string) (Wlan, error) {
	return s.applySetEnabled(ctx, id, false)
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

func (s *WlanService) applySetEnabled(ctx context.Context, id string, enabled bool) (Wlan, error) {
	if supportsOfficialDetails(s.api) {
		return s.ApplyUpdate(ctx, id, WlanInput{Enabled: enabled, SetEnabled: true})
	}
	w, err := s.getLegacy(ctx, id)
	if err != nil {
		return Wlan{}, err
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
	if !wireDocumentsEqual(wlanWritableDocument(observed), body) {
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
	if inputSetsWlanSecurity(in) {
		security, err := officialWlanSecurity(in.Security, in.Password)
		if err != nil {
			return wlanDocument{}, nil, err
		}
		if current, ok := body["securityConfiguration"].(map[string]any); ok &&
			strings.EqualFold(strField(current, "type"), strField(security, "type")) && !inputSetsWlanPassword(in) {
			// Preserve every supported security field when the effective type is unchanged.
		} else {
			body["securityConfiguration"] = security
		}
	}
	if inputSetsWlanPassword(in) && !inputSetsWlanSecurity(in) {
		security, ok := body["securityConfiguration"].(map[string]any)
		if !ok || strings.EqualFold(strField(security, "type"), "OPEN") {
			return wlanDocument{}, nil, apperr.New(apperr.ValidationFailed, "an open WLAN cannot accept a passphrase")
		}
		security = deepCloneMap(security)
		security["passphrase"] = in.Password
		body["securityConfiguration"] = security
	}
	if wireDocumentsEqual(body, wlanWritableDocument(doc.wire)) {
		return wlanDocument{}, nil, apperr.New(apperr.ValidationFailed, "WLAN update would not change controller state")
	}
	return doc, body, nil
}

func (s *WlanService) applyOfficialUpdate(ctx context.Context, query string, in WlanInput) (Wlan, error) {
	doc, body, err := s.prepareOfficialUpdate(ctx, query, in)
	if err != nil {
		return Wlan{}, err
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
	if !wireDocumentsEqual(wlanWritableDocument(observed), body) {
		return Wlan{}, apperr.New(apperr.Conflict, "WLAN update verification failed: observed writable document differs from requested state")
	}
	return NormalizeWlan(observed), nil
}

func (s *WlanService) applyOfficialDelete(ctx context.Context, query string) (Wlan, error) {
	doc, err := s.resolveOfficialDocument(ctx, query)
	if err != nil {
		return Wlan{}, err
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
	security, err := officialWlanSecurity(in.Security, in.Password)
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

func officialWlanSecurity(security, password string) (map[string]any, error) {
	var typ string
	switch strings.ToLower(security) {
	case "open":
		typ = "OPEN"
	case "wpapsk", "wpa2_personal":
		typ = "WPA2_PERSONAL"
	case "wpa3_personal":
		typ = "WPA3_PERSONAL"
	case "wpa2_wpa3_personal":
		typ = "WPA2_WPA3_PERSONAL"
	default:
		return nil, apperr.Newf(apperr.ValidationFailed, "WLAN security %q is not safely configurable through the official command surface", security)
	}
	body := map[string]any{"type": typ}
	if typ != "OPEN" {
		if strings.TrimSpace(password) == "" {
			return nil, apperr.New(apperr.ValidationFailed, "personal WLAN security requires a passphrase")
		}
		body["passphrase"] = password
	}
	return body, nil
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
		!inputSetsWlanPassword(in) && !inputSetsWlanBand(in) && !in.SetGuest && !in.SetEnabled {
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
	if err := validateEnum("WLAN security", in.Security,
		"open", "wpapsk", "wpaeap", "wpa2_personal", "wpa3_personal", "wpa2_wpa3_personal",
		"wpa2_enterprise", "wpa3_enterprise", "wpa2_wpa3_enterprise"); err != nil {
		return err
	}
	return validateEnum("WLAN band", in.Band, "2g", "5g", "6g", "both")
}

func inputSetsWlanName(in WlanInput) bool     { return in.SetName || in.Name != "" }
func inputSetsWlanSecurity(in WlanInput) bool { return in.SetSecurity || in.Security != "" }
func inputSetsWlanNetwork(in WlanInput) bool  { return in.SetNetwork || in.Network != "" }
func inputSetsWlanPassword(in WlanInput) bool { return in.SetPassword || in.Password != "" }
func inputSetsWlanBand(in WlanInput) bool     { return in.SetBand || in.Band != "" }

func wlanSecurityRequiresPassword(security string) bool {
	switch strings.ToLower(security) {
	case "wpapsk", "wpa2_personal", "wpa3_personal", "wpa2_wpa3_personal":
		return true
	default:
		return false
	}
}
