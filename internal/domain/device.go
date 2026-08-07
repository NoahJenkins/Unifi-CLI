package domain

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type DeviceAPI interface {
	Do(ctx context.Context, method, path string, in, out any) error
	SitePath(parts ...string) string
}

type Device struct {
	ID      string `json:"id"`
	MAC     string `json:"mac"`
	Name    string `json:"name"`
	Model   string `json:"model"`
	Type    string `json:"type"`
	State   string `json:"state"`
	IP      string `json:"ip"`
	Version string `json:"version"`
	Uplink  string `json:"uplink"`
	Adopted bool   `json:"adopted"`
}

type ActionAcceptance struct {
	Accepted bool `json:"accepted"`
}

type pendingDevice struct {
	device                Device
	adoptionTargetSiteIDs map[string]struct{}
}

func (d pendingDevice) GetID() string   { return "" }
func (d pendingDevice) GetMAC() string  { return d.device.MAC }
func (d pendingDevice) GetName() string { return d.device.Name }

func (d Device) GetID() string   { return d.ID }
func (d Device) GetMAC() string  { return d.MAC }
func (d Device) GetName() string { return d.Name }

type DeviceService struct {
	api DeviceAPI
}

func NewDeviceService(api DeviceAPI) *DeviceService {
	return &DeviceService{api: api}
}

func (s *DeviceService) List(ctx context.Context) ([]Device, error) {
	raw, official, err := fetchOfficialSite(s.api, ctx, "devices")
	if err != nil {
		return nil, err
	}
	if !official {
		path := s.api.SitePath(client.PathStatDevice)
		if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
			return nil, err
		}
	}
	out := make([]Device, 0, len(raw))
	for _, m := range raw {
		out = append(out, NormalizeDevice(m))
	}
	return out, nil
}

func (s *DeviceService) Get(ctx context.Context, id string) (Device, error) {
	items, err := s.List(ctx)
	if err != nil {
		return Device{}, err
	}
	overview, err := resolve.One(items, id)
	if err != nil {
		return Device{}, err
	}
	if !supportsOfficialDetails(s.api) || !looksLikeUUID(overview.ID) {
		return overview, nil
	}
	detail, err := fetchOfficialSiteDetail(s.api, ctx, overview.ID, "devices")
	if err != nil {
		return Device{}, err
	}
	return mergeOfficialDeviceDetail(overview, NormalizeDevice(detail)), nil
}

func (s *DeviceService) listLegacy(ctx context.Context) ([]Device, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathStatDevice), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(raw))
	for _, item := range raw {
		out = append(out, NormalizeDevice(item))
	}
	return out, nil
}

func (s *DeviceService) getLegacy(ctx context.Context, id string) (Device, error) {
	items, err := s.listLegacy(ctx)
	if err != nil {
		return Device{}, err
	}
	if item, ok := findExactID(items, id); ok {
		return item, nil
	}
	if !looksLikeUUID(id) {
		return resolve.One(items, id)
	}
	raw, official, err := fetchOfficialSite(s.api, ctx, "devices")
	if err != nil {
		return Device{}, err
	}
	if !official {
		return resolve.One(items, id)
	}
	officialItems := make([]Device, 0, len(raw))
	for _, item := range raw {
		officialItems = append(officialItems, NormalizeDevice(item))
	}
	return resolveLegacyMutationTarget(items, officialItems, id, "device", func(a, b Device) bool { return sameMAC(a, b) })
}

func (s *DeviceService) Rename(ctx context.Context, id, newName string) (plan.Plan, Device, error) {
	if err := validateRequired("device name", newName); err != nil {
		return plan.Plan{}, Device{}, err
	}
	d, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Device{}, err
	}
	p := plan.Update("device", d.ID, d.Name,
		fmt.Sprintf("rename device %s to %s", d.Name, newName),
		map[string]any{"name": d.Name},
		map[string]any{"name": newName},
	)
	return p, d, nil
}

func (s *DeviceService) ApplyRename(ctx context.Context, id, newName string) (Device, error) {
	if err := validateRequired("device name", newName); err != nil {
		return Device{}, err
	}
	d, err := s.getLegacy(ctx, id)
	if err != nil {
		return Device{}, err
	}
	path := s.api.SitePath(client.PathRestDevice, d.ID)
	body := map[string]any{"name": newName}
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Device{}, err
	}
	observed, err := s.getLegacy(ctx, d.ID)
	if err != nil {
		return Device{}, verificationError("renamed device could not be verified", err)
	}
	if observed.Name != newName {
		return Device{}, apperr.New(apperr.Conflict, "device rename verification failed: observed name differs from requested state")
	}
	return observed, nil
}

func mergeOfficialDeviceDetail(overview, detail Device) Device {
	if detail.ID == "" {
		detail.ID = overview.ID
	}
	if detail.MAC == "" {
		detail.MAC = overview.MAC
	}
	if detail.Name == "" {
		detail.Name = overview.Name
	}
	if detail.Model == "" {
		detail.Model = overview.Model
	}
	if overview.Type != "" {
		detail.Type = overview.Type
	}
	if detail.State == "unknown" {
		detail.State = overview.State
	}
	if detail.IP == "" {
		detail.IP = overview.IP
	}
	if detail.Version == "" {
		detail.Version = overview.Version
	}
	detail.Adopted = overview.Adopted || detail.Adopted
	return detail
}

func (s *DeviceService) Restart(ctx context.Context, id string) (plan.Plan, Device, error) {
	if supportsOfficialDetails(s.api) {
		return s.officialActionPlan(ctx, id, "restart")
	}
	return s.cmdPlan(ctx, id, "restart", "update")
}

func (s *DeviceService) ApplyRestart(ctx context.Context, id string) (ActionAcceptance, error) {
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialRestart(ctx, id)
	}
	return s.applyDevMgr(ctx, id, "restart")
}

func (s *DeviceService) Locate(ctx context.Context, id string) (plan.Plan, Device, error) {
	return s.cmdPlan(ctx, id, "set-locate", "update")
}

func (s *DeviceService) ApplyLocate(ctx context.Context, id string) (ActionAcceptance, error) {
	return s.applyDevMgr(ctx, id, "set-locate")
}

func (s *DeviceService) Upgrade(ctx context.Context, id string) (plan.Plan, Device, error) {
	return s.cmdPlan(ctx, id, "upgrade", "update")
}

func (s *DeviceService) ApplyUpgrade(ctx context.Context, id string) (ActionAcceptance, error) {
	return s.applyDevMgr(ctx, id, "upgrade")
}

func (s *DeviceService) Adopt(ctx context.Context, id string) (plan.Plan, Device, error) {
	if supportsOfficialDetails(s.api) {
		d, _, err := s.resolveOfficialAdoption(ctx, id)
		if err != nil {
			return plan.Plan{}, Device{}, err
		}
		p := plan.Update("device", d.ID, d.Name, fmt.Sprintf("adopt device %s", d.Name),
			map[string]any{"action": "none", "mac": d.MAC}, map[string]any{"action": "adopt", "mac": d.MAC})
		return p, d, nil
	}
	return s.cmdPlan(ctx, id, "adopt", "update")
}

func (s *DeviceService) ApplyAdopt(ctx context.Context, id string) (ActionAcceptance, error) {
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialAdopt(ctx, id)
	}
	return s.applyDevMgr(ctx, id, "adopt")
}

func (s *DeviceService) Forget(ctx context.Context, id string) (plan.Plan, Device, error) {
	if supportsOfficialDetails(s.api) {
		d, err := s.Get(ctx, id)
		if err != nil {
			return plan.Plan{}, Device{}, err
		}
		if !looksLikeUUID(d.ID) {
			return plan.Plan{}, Device{}, apperr.New(apperr.Conflict, "official device target has an invalid ID")
		}
		p := plan.Delete("device", d.ID, d.Name, fmt.Sprintf("forget device %s", d.Name),
			map[string]any{"id": d.ID, "mac": d.MAC, "name": d.Name})
		return p, d, nil
	}
	d, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Device{}, err
	}
	p := plan.Delete("device", d.ID, d.Name,
		fmt.Sprintf("forget device %s", d.Name),
		map[string]any{"id": d.ID, "mac": d.MAC, "name": d.Name},
	)
	return p, d, nil
}

func (s *DeviceService) ApplyForget(ctx context.Context, id string) (ActionAcceptance, error) {
	if supportsOfficialDetails(s.api) {
		return s.applyOfficialForget(ctx, id)
	}
	return s.applyDevMgr(ctx, id, "delete-device")
}

func (s *DeviceService) cmdPlan(ctx context.Context, id, cmd, op string) (plan.Plan, Device, error) {
	d, err := s.getLegacy(ctx, id)
	if err != nil {
		return plan.Plan{}, Device{}, err
	}
	summary := fmt.Sprintf("%s device %s", cmd, d.Name)
	if op == "delete" {
		return plan.Delete("device", d.ID, d.Name, summary, map[string]any{"cmd": cmd, "mac": d.MAC}), d, nil
	}
	p := plan.Update("device", d.ID, d.Name, summary,
		map[string]any{"action": "none"},
		map[string]any{"action": cmd, "mac": d.MAC},
	)
	return p, d, nil
}

func (s *DeviceService) applyDevMgr(ctx context.Context, id, cmd string) (ActionAcceptance, error) {
	d, err := s.getLegacy(ctx, id)
	if err != nil {
		return ActionAcceptance{}, err
	}
	path := s.api.SitePath(client.PathCmdDevMgr)
	body := map[string]any{"cmd": cmd, "mac": d.MAC}
	if err := s.api.Do(ctx, http.MethodPost, path, body, nil); err != nil {
		return ActionAcceptance{}, err
	}
	return ActionAcceptance{Accepted: true}, nil
}

func (s *DeviceService) officialActionPlan(ctx context.Context, query, action string) (plan.Plan, Device, error) {
	d, err := s.Get(ctx, query)
	if err != nil {
		return plan.Plan{}, Device{}, err
	}
	if !looksLikeUUID(d.ID) {
		return plan.Plan{}, Device{}, apperr.New(apperr.Conflict, "official device target has an invalid ID")
	}
	p := plan.Update("device", d.ID, d.Name, fmt.Sprintf("%s device %s", action, d.Name),
		map[string]any{"action": "none", "id": d.ID, "state": d.State},
		map[string]any{"action": action, "id": d.ID, "state": d.State})
	return p, d, nil
}

func (s *DeviceService) applyOfficialRestart(ctx context.Context, query string) (ActionAcceptance, error) {
	d, err := s.Get(ctx, query)
	if err != nil {
		return ActionAcceptance{}, err
	}
	if !looksLikeUUID(d.ID) {
		return ActionAcceptance{}, apperr.New(apperr.Conflict, "official device target has an invalid ID")
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	path, err := transport.IntegrationSitePath(ctx, "devices", d.ID, "actions")
	if err != nil {
		return ActionAcceptance{}, err
	}
	if err := transport.DoOfficial(ctx, http.MethodPost, path, map[string]any{"action": "RESTART"}, nil); err != nil {
		return ActionAcceptance{}, err
	}
	return ActionAcceptance{Accepted: true}, nil
}

func (s *DeviceService) getPendingOfficial(ctx context.Context, query string) (pendingDevice, error) {
	raw, official, err := fetchOfficialGlobal(s.api, ctx, "pending-devices")
	if err != nil {
		return pendingDevice{}, err
	}
	if !official {
		return pendingDevice{}, apperr.New(apperr.Internal, "official pending-device transport is unavailable")
	}
	items := make([]pendingDevice, 0, len(raw))
	for _, item := range raw {
		d := NormalizeDevice(item)
		if d.Name == "" {
			d.Name = d.Model
		}
		targets, err := parseAdoptionTargetSiteIDs(item["adoptionTargetSiteIds"])
		if err != nil {
			return pendingDevice{}, err
		}
		items = append(items, pendingDevice{device: d, adoptionTargetSiteIDs: targets})
	}
	selected, err := resolve.One(items, query)
	if err != nil {
		return pendingDevice{}, err
	}
	if strings.TrimSpace(selected.device.MAC) == "" {
		return pendingDevice{}, apperr.New(apperr.Conflict, "pending device target has no MAC address")
	}
	return selected, nil
}

func parseAdoptionTargetSiteIDs(value any) (map[string]struct{}, error) {
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 {
		return nil, apperr.New(apperr.Conflict, "pending device has an empty or malformed adoption target site set")
	}
	targets := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		id, ok := item.(string)
		if !ok || !looksLikeUUID(id) {
			return nil, apperr.New(apperr.Conflict, "pending device has an empty or malformed adoption target site set")
		}
		if _, duplicate := targets[id]; duplicate {
			return nil, apperr.New(apperr.Conflict, "pending device adoption target site set contains duplicates")
		}
		targets[id] = struct{}{}
	}
	return targets, nil
}

func officialSiteIDFromPath(path string) (string, error) {
	prefix := client.OfficialPath("sites") + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", apperr.New(apperr.Internal, "official site path is malformed")
	}
	escaped := strings.SplitN(strings.TrimPrefix(path, prefix), "/", 2)[0]
	id, err := url.PathUnescape(escaped)
	if err != nil || !looksLikeUUID(id) {
		return "", apperr.New(apperr.Internal, "official site path has an invalid site ID")
	}
	return id, nil
}

func (s *DeviceService) resolveOfficialAdoption(ctx context.Context, query string) (Device, string, error) {
	transport, err := requireOfficialMutationAPI(s.api)
	if err != nil {
		return Device{}, "", err
	}
	path, err := transport.IntegrationSitePath(ctx, "devices")
	if err != nil {
		return Device{}, "", err
	}
	siteID, err := officialSiteIDFromPath(path)
	if err != nil {
		return Device{}, "", err
	}
	pending, err := s.getPendingOfficial(ctx, query)
	if err != nil {
		return Device{}, "", err
	}
	if _, eligible := pending.adoptionTargetSiteIDs[siteID]; !eligible {
		return Device{}, "", apperr.Newf(apperr.Conflict, "pending device is not eligible for adoption into site %s", siteID)
	}
	d := pending.device
	d.ID = d.MAC
	return d, path, nil
}

func (s *DeviceService) applyOfficialAdopt(ctx context.Context, query string) (ActionAcceptance, error) {
	d, path, err := s.resolveOfficialAdoption(ctx, query)
	if err != nil {
		return ActionAcceptance{}, err
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	body := map[string]any{"ignoreDeviceLimit": false, "macAddress": d.MAC}
	var response map[string]any
	if err := transport.DoOfficial(ctx, http.MethodPost, path, body, &response); err != nil {
		return ActionAcceptance{}, err
	}
	if !looksLikeUUID(strField(response, "id")) || !reflect.DeepEqual(resolve.NormalizeMAC(strField(response, "macAddress")), resolve.NormalizeMAC(d.MAC)) {
		return ActionAcceptance{}, apperr.New(apperr.Conflict, "device adoption result is unverified: controller response is missing the matching device ID or MAC")
	}
	return ActionAcceptance{Accepted: true}, nil
}

func (s *DeviceService) applyOfficialForget(ctx context.Context, query string) (ActionAcceptance, error) {
	d, err := s.Get(ctx, query)
	if err != nil {
		return ActionAcceptance{}, err
	}
	if !looksLikeUUID(d.ID) {
		return ActionAcceptance{}, apperr.New(apperr.Conflict, "official device target has an invalid ID")
	}
	transport, _ := requireOfficialMutationAPI(s.api)
	path, err := transport.IntegrationSitePath(ctx, "devices", d.ID)
	if err != nil {
		return ActionAcceptance{}, err
	}
	if err := transport.DoOfficial(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return ActionAcceptance{}, err
	}
	if _, err := fetchOfficialSiteDetail(s.api, ctx, d.ID, "devices"); err == nil {
		return ActionAcceptance{}, apperr.New(apperr.Conflict, "device forget verification failed: deleted device is still present")
	} else if !apperr.Is(err, apperr.NotFound) {
		return ActionAcceptance{}, verificationError("forgotten device could not be verified", err)
	}
	return ActionAcceptance{Accepted: true}, nil
}

func NormalizeDevice(m map[string]any) Device {
	d := Device{
		ID:      strField(m, "_id", "id"),
		MAC:     strField(m, "mac", "macAddress"),
		Name:    strField(m, "name", "display_name"),
		Model:   strField(m, "model"),
		Type:    strField(m, "type"),
		IP:      strField(m, "ip", "last_ip", "ipAddress"),
		Version: strField(m, "version", "sw_version", "firmwareVersion"),
		Adopted: boolField(m, "adopted"),
		State:   mapDeviceState(m["state"]),
		Uplink:  uplinkMAC(m),
	}
	if d.Type == "" {
		d.Type = officialDeviceType(m["features"])
	}
	if _, official := m["macAddress"]; official {
		d.Adopted = true
	}
	return d
}

func mapDeviceState(v any) string {
	n, ok := asInt(v)
	if !ok {
		if s, ok := v.(string); ok && s != "" {
			switch strings.ToUpper(s) {
			case "ONLINE":
				return "connected"
			case "OFFLINE":
				return "disconnected"
			case "PENDING_ADOPTION":
				return "pending"
			case "UPDATING":
				return "upgrading"
			case "GETTING_READY":
				return "provisioning"
			case "CONNECTION_INTERRUPTED":
				return "heartbeat_missed"
			case "ADOPTING":
				return "adopting"
			case "DELETING":
				return "deleting"
			case "ISOLATED":
				return "isolated"
			default:
				return strings.ToLower(s)
			}
		}
		return "unknown"
	}
	switch n {
	case 0:
		return "disconnected"
	case 1:
		return "connected"
	case 2:
		return "pending"
	case 3:
		return "firmware_mismatch"
	case 4:
		return "upgrading"
	case 5:
		return "provisioning"
	case 6:
		return "heartbeat_missed"
	case 7:
		return "adopting"
	case 8:
		return "deleting"
	case 9:
		return "inform_error"
	case 10:
		return "adopting_failed"
	case 11:
		return "isolated"
	default:
		return "unknown"
	}
}

func uplinkMAC(m map[string]any) string {
	u, ok := m["uplink"].(map[string]any)
	if !ok {
		return ""
	}
	return strField(u, "uplink_mac", "mac", "deviceId")
}

func officialDeviceType(v any) string {
	features := map[string]bool{}
	for _, feature := range anyStringSlice(v) {
		features[feature] = true
	}
	if featureMap, ok := v.(map[string]any); ok {
		for feature := range featureMap {
			features[feature] = true
		}
	}
	switch {
	case features["gateway"]:
		return "gateway"
	case features["switching"]:
		return "switch"
	case features["accessPoint"]:
		return "access_point"
	default:
		return ""
	}
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		if s := anyToString(v); s != "" {
			return s
		}
	}
	return ""
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		if float64(t) == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case jsonNumber:
		return t.String()
	default:
		s := fmt.Sprint(t)
		if s == "" || s == "<nil>" {
			return ""
		}
		return s
	}
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return false
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case jsonNumber:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// jsonNumber matches encoding/json.Number without importing for the type assert path.
type jsonNumber interface {
	Int64() (int64, error)
	String() string
}
