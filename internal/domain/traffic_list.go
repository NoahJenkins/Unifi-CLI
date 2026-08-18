package domain

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type TrafficListItem struct {
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
	Start any    `json:"start,omitempty"`
	Stop  any    `json:"stop,omitempty"`
}

type TrafficList struct {
	ID    string            `json:"id"`
	Type  string            `json:"type"`
	Name  string            `json:"name"`
	Items []TrafficListItem `json:"items"`
}

func (r TrafficList) GetID() string   { return r.ID }
func (r TrafficList) GetMAC() string  { return "" }
func (r TrafficList) GetName() string { return r.Name }

type TrafficListInput struct {
	Type     string
	Name     string
	SetName  bool
	Items    []string
	SetItems bool
}

type TrafficListService struct{ api SwitchingAPI }

func NewTrafficListService(api SwitchingAPI) *TrafficListService {
	return &TrafficListService{api: api}
}

func (s *TrafficListService) List(ctx context.Context) ([]TrafficList, error) {
	path, err := s.api.IntegrationSitePath(ctx, "traffic-matching-lists")
	if err != nil {
		return nil, err
	}
	raw, err := s.api.FetchOfficialObjects(ctx, path)
	if err != nil {
		return nil, err
	}
	items := make([]TrafficList, 0, len(raw))
	for _, value := range raw {
		items = append(items, normalizeTrafficList(value))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *TrafficListService) Get(ctx context.Context, query string) (TrafficList, error) {
	items, err := s.List(ctx)
	if err != nil {
		return TrafficList{}, err
	}
	item, err := resolve.One(items, query)
	if err != nil {
		return TrafficList{}, err
	}
	raw, err := fetchOfficialSiteDetail(s.api, ctx, item.ID, "traffic-matching-lists")
	if err != nil {
		return TrafficList{}, err
	}
	return normalizeTrafficList(raw), nil
}

func (s *TrafficListService) Create(_ context.Context, in TrafficListInput) (plan.Plan, error) {
	value, err := trafficListCreateValue(in)
	if err != nil {
		return plan.Plan{}, err
	}
	return plan.Create("traffic_list", value.Name, "create "+trafficListSummary(value), trafficListSnapshot(value)), nil
}

func (s *TrafficListService) Update(ctx context.Context, query string, in TrafficListInput) (plan.Plan, TrafficList, error) {
	current, expected, err := s.prepareUpdate(ctx, query, in)
	if err != nil {
		return plan.Plan{}, TrafficList{}, err
	}
	return plan.Update("traffic_list", current.ID, current.Name, "update "+trafficListSummary(current), trafficListSnapshot(current), trafficListSnapshot(expected)), current, nil
}

func (s *TrafficListService) Delete(ctx context.Context, query string) (plan.Plan, TrafficList, error) {
	current, err := s.Get(ctx, query)
	if err != nil {
		return plan.Plan{}, TrafficList{}, err
	}
	return plan.Delete("traffic_list", current.ID, current.Name, "delete "+trafficListSummary(current), trafficListSnapshot(current)), current, nil
}

func (s *TrafficListService) ApplyCreate(ctx context.Context, in TrafficListInput) (TrafficList, error) {
	body, err := trafficListCreateBody(in)
	if err != nil {
		return TrafficList{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "traffic-matching-lists")
	if err != nil {
		return TrafficList{}, err
	}
	var response map[string]any
	if err := s.api.DoOfficial(ctx, http.MethodPost, path, body, &response); err != nil {
		return TrafficList{}, err
	}
	id := strField(response, "id")
	if id == "" {
		return TrafficList{}, apperr.New(apperr.Internal, "controller did not return a traffic list ID")
	}
	observed, err := s.getByID(ctx, id)
	if err != nil {
		return TrafficList{}, verificationError("created traffic list could not be verified", err)
	}
	if observedBody, _ := trafficListBody(observed); !wireDocumentsEqual(observedBody, body) {
		return TrafficList{}, apperr.New(apperr.Conflict, "created traffic list does not match requested state")
	}
	return observed, nil
}

func (s *TrafficListService) ApplyUpdate(ctx context.Context, query string, in TrafficListInput) (TrafficList, error) {
	current, expected, err := s.prepareUpdate(ctx, query, in)
	if err != nil {
		return TrafficList{}, err
	}
	afterBody, err := trafficListBody(expected)
	if err != nil {
		return TrafficList{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "traffic-matching-lists", current.ID)
	if err != nil {
		return TrafficList{}, err
	}
	if err := s.api.DoOfficial(ctx, http.MethodPut, path, afterBody, nil); err != nil {
		return TrafficList{}, err
	}
	observed, err := s.getByID(ctx, current.ID)
	if err != nil {
		return TrafficList{}, verificationError("updated traffic list could not be verified", err)
	}
	observedBody, err := trafficListBody(observed)
	if err != nil || !wireDocumentsEqual(observedBody, afterBody) {
		return TrafficList{}, apperr.New(apperr.Conflict, "updated traffic list does not match complete requested state")
	}
	return observed, nil
}

func (s *TrafficListService) prepareUpdate(ctx context.Context, query string, in TrafficListInput) (TrafficList, TrafficList, error) {
	current, err := s.Get(ctx, query)
	if err != nil {
		return TrafficList{}, TrafficList{}, err
	}
	if in.Type != "" {
		kind, err := canonicalTrafficListType(in.Type)
		if err != nil {
			return TrafficList{}, TrafficList{}, err
		}
		if kind != current.Type {
			return TrafficList{}, TrafficList{}, apperr.New(apperr.ValidationFailed, "traffic list type cannot be changed")
		}
	}
	expected := current
	if in.SetName || in.Name != "" {
		expected.Name = in.Name
	}
	if in.SetItems {
		items, err := parseTrafficListItems(current.Type, in.Items)
		if err != nil {
			return TrafficList{}, TrafficList{}, err
		}
		expected.Items = items
	}
	if expected.Name == "" || len(expected.Items) == 0 {
		return TrafficList{}, TrafficList{}, apperr.New(apperr.ValidationFailed, "traffic list name and items are required")
	}
	beforeBody, err := trafficListBody(current)
	if err != nil {
		return TrafficList{}, TrafficList{}, err
	}
	afterBody, err := trafficListBody(expected)
	if err != nil {
		return TrafficList{}, TrafficList{}, err
	}
	if reflect.DeepEqual(beforeBody, afterBody) {
		return TrafficList{}, TrafficList{}, apperr.New(apperr.ValidationFailed, "traffic list update does not change controller state")
	}
	return current, expected, nil
}

func (s *TrafficListService) ApplyDelete(ctx context.Context, query string) (TrafficList, error) {
	current, err := s.Get(ctx, query)
	if err != nil {
		return TrafficList{}, err
	}
	path, err := s.api.IntegrationSitePath(ctx, "traffic-matching-lists", current.ID)
	if err != nil {
		return TrafficList{}, err
	}
	if err := s.api.DoOfficial(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return TrafficList{}, err
	}
	if _, err := s.getByID(ctx, current.ID); err == nil {
		return TrafficList{}, apperr.New(apperr.Conflict, "deleted traffic list is still available by ID")
	} else if !apperr.Is(err, apperr.NotFound) {
		return TrafficList{}, verificationError("deleted traffic list could not be verified", err)
	}
	return current, nil
}

func (s *TrafficListService) getByID(ctx context.Context, id string) (TrafficList, error) {
	raw, err := fetchOfficialSiteDetail(s.api, ctx, id, "traffic-matching-lists")
	if err != nil {
		return TrafficList{}, err
	}
	return normalizeTrafficList(raw), nil
}

func trafficListCreateBody(in TrafficListInput) (map[string]any, error) {
	value, err := trafficListCreateValue(in)
	if err != nil {
		return nil, err
	}
	return trafficListBody(value)
}

func trafficListCreateValue(in TrafficListInput) (TrafficList, error) {
	kind, err := canonicalTrafficListType(in.Type)
	if err != nil {
		return TrafficList{}, err
	}
	if in.Name == "" {
		return TrafficList{}, apperr.New(apperr.ValidationFailed, "traffic list name is required")
	}
	items, err := parseTrafficListItems(kind, in.Items)
	if err != nil {
		return TrafficList{}, err
	}
	return TrafficList{Type: kind, Name: in.Name, Items: items}, nil
}

func trafficListBody(value TrafficList) (map[string]any, error) {
	if value.Name == "" || len(value.Items) == 0 || !isTrafficListType(value.Type) {
		return nil, apperr.New(apperr.ValidationFailed, "traffic list type, name, and items are required")
	}
	items := make([]map[string]any, 0, len(value.Items))
	for _, item := range value.Items {
		wire := map[string]any{"type": item.Type}
		if item.Value != nil {
			wire["value"] = item.Value
		}
		if item.Start != nil {
			wire["start"] = item.Start
		}
		if item.Stop != nil {
			wire["stop"] = item.Stop
		}
		items = append(items, wire)
	}
	return map[string]any{"type": value.Type, "name": value.Name, "items": items}, nil
}

func canonicalTrafficListType(value string) (string, error) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-")) {
	case "ports":
		return "PORTS", nil
	case "ipv4-addresses":
		return "IPV4_ADDRESSES", nil
	case "ipv6-addresses":
		return "IPV6_ADDRESSES", nil
	default:
		return "", apperr.Newf(apperr.ValidationFailed, "unsupported traffic list type %q", value)
	}
}

func isTrafficListType(value string) bool {
	return value == "PORTS" || value == "IPV4_ADDRESSES" || value == "IPV6_ADDRESSES"
}

func parseTrafficListItems(kind string, values []string) ([]TrafficListItem, error) {
	if len(values) == 0 {
		return nil, apperr.New(apperr.ValidationFailed, "traffic list requires at least one item")
	}
	items := make([]TrafficListItem, 0, len(values))
	for _, value := range values {
		item, err := parseTrafficListItem(kind, strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func parseTrafficListItem(kind, value string) (TrafficListItem, error) {
	switch kind {
	case "PORTS":
		parts := strings.Split(value, "-")
		if len(parts) == 1 {
			port, err := parseTrafficPort(parts[0])
			return TrafficListItem{Type: "PORT_NUMBER", Value: port}, err
		}
		if len(parts) == 2 {
			start, startErr := parseTrafficPort(parts[0])
			stop, stopErr := parseTrafficPort(parts[1])
			if startErr != nil || stopErr != nil || start > stop {
				return TrafficListItem{}, apperr.New(apperr.ValidationFailed, "traffic port range is invalid")
			}
			return TrafficListItem{Type: "PORT_NUMBER_RANGE", Start: start, Stop: stop}, nil
		}
	case "IPV4_ADDRESSES", "IPV6_ADDRESSES":
		want4 := kind == "IPV4_ADDRESSES"
		if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Addr().Is4() == want4 {
			return TrafficListItem{Type: "SUBNET", Value: prefix.String()}, nil
		}
		if address, err := netip.ParseAddr(value); err == nil && address.Is4() == want4 {
			return TrafficListItem{Type: "IP_ADDRESS", Value: address.String()}, nil
		}
		if want4 {
			parts := strings.Split(value, "-")
			if len(parts) == 2 {
				start, startErr := netip.ParseAddr(parts[0])
				stop, stopErr := netip.ParseAddr(parts[1])
				if startErr == nil && stopErr == nil && start.Is4() && stop.Is4() && start.Compare(stop) <= 0 {
					return TrafficListItem{Type: "IP_ADDRESS_RANGE", Start: start.String(), Stop: stop.String()}, nil
				}
			}
		}
	}
	return TrafficListItem{}, apperr.Newf(apperr.ValidationFailed, "invalid %s traffic list item %q", kind, value)
}

func parseTrafficPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, apperr.New(apperr.ValidationFailed, "traffic port must be between 1 and 65535")
	}
	return port, nil
}

func normalizeTrafficList(value map[string]any) TrafficList {
	rawItems, _ := value["items"].([]any)
	items := make([]TrafficListItem, 0, len(rawItems))
	for _, raw := range rawItems {
		item, _ := raw.(map[string]any)
		items = append(items, TrafficListItem{Type: strField(item, "type"), Value: item["value"], Start: item["start"], Stop: item["stop"]})
	}
	return TrafficList{ID: strField(value, "id"), Type: strField(value, "type"), Name: strField(value, "name"), Items: items}
}

func trafficListSnapshot(value TrafficList) map[string]any {
	return map[string]any{"id": value.ID, "type": value.Type, "name": value.Name, "items": value.Items}
}

func trafficListSummary(value TrafficList) string {
	return fmt.Sprintf("%s traffic list %s", value.Type, value.Name)
}
