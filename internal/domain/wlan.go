package domain

import (
	"context"
	"fmt"
	"net/http"

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
	Name     string
	Security string
	Network  string
	Password string
	Band     string
	Guest    bool
	SetGuest bool
}

type WlanService struct {
	api WlanAPI
}

func NewWlanService(api WlanAPI) *WlanService {
	return &WlanService{api: api}
}

func (s *WlanService) List(ctx context.Context) ([]Wlan, error) {
	var raw []map[string]any
	path := s.api.SitePath(client.PathRestWlan)
	if err := s.api.Do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
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
	return resolve.One(items, id)
}

func (s *WlanService) Create(ctx context.Context, in WlanInput) (plan.Plan, error) {
	_ = ctx
	after := wlanPlanAfter(in)
	p := plan.Create("wlan", in.Name,
		fmt.Sprintf("create wlan %s", in.Name),
		after,
	)
	return p, nil
}

func (s *WlanService) ApplyCreate(ctx context.Context, in WlanInput) (Wlan, error) {
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
	w, err := s.Get(ctx, id)
	if err != nil {
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
	w, err := s.Get(ctx, id)
	if err != nil {
		return Wlan{}, err
	}
	path := s.api.SitePath(client.PathRestWlan, w.ID)
	body := wlanInputBody(in)
	if err := s.api.Do(ctx, http.MethodPut, path, body, nil); err != nil {
		return Wlan{}, err
	}
	if in.Name != "" {
		w.Name = in.Name
	}
	if in.Security != "" {
		w.Security = in.Security
	}
	if in.Network != "" {
		w.NetworkID = in.Network
	}
	if in.Band != "" {
		w.Band = in.Band
	}
	if in.SetGuest {
		w.Guest = in.Guest
	}
	return w, nil
}

func (s *WlanService) Delete(ctx context.Context, id string) (plan.Plan, Wlan, error) {
	w, err := s.Get(ctx, id)
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
	w, err := s.Get(ctx, id)
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
	w, err := s.Get(ctx, id)
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
	w, err := s.Get(ctx, id)
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
	return Wlan{
		ID:        strField(m, "_id", "id"),
		Name:      strField(m, "name"),
		Enabled:   boolField(m, "enabled"),
		Security:  strField(m, "security"),
		NetworkID: strField(m, "networkconf_id", "network_id"),
		Band:      strField(m, "wlan_band", "band"),
		Guest:     boolField(m, "is_guest"),
	}
}

func wlanInputBody(in WlanInput) map[string]any {
	body := map[string]any{}
	if in.Name != "" {
		body["name"] = in.Name
	}
	if in.Security != "" {
		body["security"] = in.Security
	}
	if in.Network != "" {
		body["networkconf_id"] = in.Network
	}
	if in.Password != "" {
		body["x_passphrase"] = in.Password
	}
	if in.Band != "" {
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
	if in.Name != "" {
		after["name"] = in.Name
	}
	if in.Security != "" {
		after["security"] = in.Security
	}
	if in.Network != "" {
		after["network"] = in.Network
	}
	if in.Password != "" {
		after["password"] = "***"
	}
	if in.Band != "" {
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
	if in.Name != "" {
		after["name"] = in.Name
	}
	if in.Security != "" {
		after["security"] = in.Security
	}
	if in.Network != "" {
		after["network_id"] = in.Network
	}
	if in.Password != "" {
		after["password"] = "***"
	}
	if in.Band != "" {
		after["band"] = in.Band
	}
	if in.SetGuest {
		after["guest"] = in.Guest
	}
	return after
}
