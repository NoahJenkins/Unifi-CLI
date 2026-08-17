package domain

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/netip"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/client"
	"github.com/noahjenkins/unifi-cli/internal/plan"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

// ClientFixedIPReservation is the effective fixed-IP configuration returned
// by client fixed-ip mutations. FixedIP is empty when the reservation is off.
type ClientFixedIPReservation struct {
	ClientID       string `json:"client_id"`
	MAC            string `json:"mac"`
	Name           string `json:"name"`
	NetworkID      string `json:"network_id"`
	FixedIPEnabled bool   `json:"fixed_ip_enabled"`
	FixedIP        string `json:"fixed_ip"`
}

// ClientFixedIPSnapshot is the canonical state bound to a prepared mutation.
type ClientFixedIPSnapshot struct {
	ClientID             string `json:"client_id"`
	MAC                  string `json:"mac"`
	Name                 string `json:"name"`
	NetworkID            string `json:"network_id"`
	ReservationNetworkID string `json:"reservation_network_id"`
	FixedIPEnabled       bool   `json:"fixed_ip_enabled"`
	FixedIP              string `json:"fixed_ip"`
	Subnet               string `json:"subnet"`
	DHCPEnabled          bool   `json:"dhcp_enabled"`
}

type ClientFixedIPService struct {
	api ClientAPI
}

func NewClientFixedIPService(api ClientAPI) *ClientFixedIPService {
	return &ClientFixedIPService{api: api}
}

func (s *ClientFixedIPService) Set(ctx context.Context, query, fixedIP string) (plan.Plan, ClientFixedIPSnapshot, error) {
	return s.prepare(ctx, query, fixedIP, true)
}

func (s *ClientFixedIPService) Clear(ctx context.Context, query string) (plan.Plan, ClientFixedIPSnapshot, error) {
	return s.prepare(ctx, query, "", false)
}

func (s *ClientFixedIPService) prepare(ctx context.Context, query, fixedIP string, enable bool) (plan.Plan, ClientFixedIPSnapshot, error) {
	connected, err := NewClientService(s.api).getLegacy(ctx, query)
	if err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}
	mac := resolve.NormalizeMAC(connected.MAC)
	if mac == "" {
		return plan.Plan{}, ClientFixedIPSnapshot{}, apperr.New(apperr.Conflict, "connected client has no valid MAC address")
	}
	if connected.networkID == "" {
		return plan.Plan{}, ClientFixedIPSnapshot{}, apperr.New(apperr.Conflict, "connected client has no network ID")
	}

	user, err := s.getUser(ctx, connected.ID)
	if err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}
	if strField(user, "_id", "id") != connected.ID || resolve.NormalizeMAC(strField(user, "mac", "macAddress")) != mac {
		return plan.Plan{}, ClientFixedIPSnapshot{}, apperr.New(apperr.Conflict, "client fixed-IP target identity does not match the connected client")
	}
	network, err := s.getNetwork(ctx, connected.networkID)
	if err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}

	snapshot := ClientFixedIPSnapshot{
		ClientID: connected.ID, MAC: mac, Name: connected.GetName(), NetworkID: connected.networkID,
		ReservationNetworkID: strField(user, "network_id", "networkconf_id"),
		FixedIPEnabled:       boolField(user, "use_fixedip"), FixedIP: strField(user, "fixed_ip"),
		Subnet: network.Subnet, DHCPEnabled: network.DHCPEnabled,
	}
	if snapshot.Name == "" {
		snapshot.Name = strField(user, "name", "hostname")
	}

	before := reservationFromSnapshot(snapshot)
	var after ClientFixedIPReservation
	if enable {
		if err := s.validateSet(ctx, snapshot, fixedIP); err != nil {
			return plan.Plan{}, ClientFixedIPSnapshot{}, err
		}
		if snapshot.FixedIPEnabled && snapshot.FixedIP == fixedIP && snapshot.ReservationNetworkID == snapshot.NetworkID {
			return plan.Plan{}, ClientFixedIPSnapshot{}, apperr.New(apperr.ValidationFailed, "client already has the requested fixed-IP reservation")
		}
		after = ClientFixedIPReservation{
			ClientID: snapshot.ClientID, MAC: snapshot.MAC, Name: snapshot.Name, NetworkID: snapshot.NetworkID,
			FixedIPEnabled: true, FixedIP: fixedIP,
		}
	} else {
		if !snapshot.FixedIPEnabled {
			return plan.Plan{}, ClientFixedIPSnapshot{}, apperr.New(apperr.ValidationFailed, "client fixed-IP reservation is already disabled")
		}
		after = ClientFixedIPReservation{
			ClientID: snapshot.ClientID, MAC: snapshot.MAC, Name: snapshot.Name, NetworkID: snapshot.NetworkID,
			FixedIPEnabled: false, FixedIP: "",
		}
	}

	action := "set"
	if !enable {
		action = "clear"
	}
	p := plan.Update("client", snapshot.ClientID, snapshot.Name,
		fmt.Sprintf("%s fixed IP for client %s", action, snapshot.Name), before, after)
	return p, snapshot, nil
}

func (s *ClientFixedIPService) ApplySetPrepared(ctx context.Context, target plan.Target, id, fixedIP string) (ClientFixedIPReservation, error) {
	_, snapshot, err := s.Set(ctx, id, fixedIP)
	if err != nil {
		return ClientFixedIPReservation{}, err
	}
	if err := requirePreparedTarget(target, snapshot); err != nil {
		return ClientFixedIPReservation{}, err
	}
	body := map[string]any{"_id": snapshot.ClientID, "use_fixedip": true, "network_id": snapshot.NetworkID, "fixed_ip": fixedIP}
	if err := s.api.Do(ctx, http.MethodPut, s.api.SitePath(client.PathRestUser, snapshot.ClientID), body, nil); err != nil {
		return ClientFixedIPReservation{}, err
	}
	return s.verify(ctx, snapshot, true, fixedIP)
}

func (s *ClientFixedIPService) ApplyClearPrepared(ctx context.Context, target plan.Target, id string) (ClientFixedIPReservation, error) {
	_, snapshot, err := s.Clear(ctx, id)
	if err != nil {
		return ClientFixedIPReservation{}, err
	}
	if err := requirePreparedTarget(target, snapshot); err != nil {
		return ClientFixedIPReservation{}, err
	}
	body := map[string]any{"_id": snapshot.ClientID, "use_fixedip": false}
	if err := s.api.Do(ctx, http.MethodPut, s.api.SitePath(client.PathRestUser, snapshot.ClientID), body, nil); err != nil {
		return ClientFixedIPReservation{}, err
	}
	return s.verify(ctx, snapshot, false, "")
}

func (s *ClientFixedIPService) getUser(ctx context.Context, id string) (map[string]any, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestUser, id), nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) != 1 {
		return nil, apperr.New(apperr.Conflict, "client fixed-IP user record is missing or ambiguous")
	}
	return raw[0], nil
}

func (s *ClientFixedIPService) getNetwork(ctx context.Context, id string) (Network, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestNetwork, id), nil, &raw); err != nil {
		return Network{}, err
	}
	if len(raw) != 1 || strField(raw[0], "_id", "id") != id {
		return Network{}, apperr.New(apperr.Conflict, "client network record is missing or does not match")
	}
	return NormalizeNetwork(raw[0]), nil
}

func (s *ClientFixedIPService) validateSet(ctx context.Context, snapshot ClientFixedIPSnapshot, fixedIP string) error {
	addr, err := netip.ParseAddr(fixedIP)
	if err != nil || !addr.Is4() {
		return apperr.New(apperr.ValidationFailed, "fixed IP must be a valid IPv4 address")
	}
	if !snapshot.DHCPEnabled {
		return apperr.New(apperr.ValidationFailed, "client network must have DHCP enabled")
	}
	prefix, err := netip.ParsePrefix(snapshot.Subnet)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 30 {
		return apperr.New(apperr.ValidationFailed, "client network must have a valid usable IPv4 subnet")
	}
	if !prefix.Contains(addr) {
		return apperr.New(apperr.ValidationFailed, "fixed IP must be inside the client network subnet")
	}
	if addr == prefix.Masked().Addr() || addr == prefix.Addr() || addr == ipv4Broadcast(prefix) {
		return apperr.New(apperr.ValidationFailed, "fixed IP must not be the network, broadcast, or gateway address")
	}

	var users []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestUser), nil, &users); err != nil {
		return err
	}
	for _, user := range users {
		if strField(user, "_id", "id") != snapshot.ClientID && boolField(user, "use_fixedip") && strField(user, "fixed_ip") == fixedIP {
			return apperr.New(apperr.Conflict, "fixed IP is already reserved by another client")
		}
	}

	clients, err := NewClientService(s.api).listLegacy(ctx)
	if err != nil {
		return err
	}
	for _, connected := range clients {
		if resolve.NormalizeMAC(connected.MAC) != snapshot.MAC && connected.IP == fixedIP {
			return apperr.New(apperr.Conflict, "fixed IP is currently used by another connected client")
		}
	}
	return nil
}

func (s *ClientFixedIPService) verify(ctx context.Context, snapshot ClientFixedIPSnapshot, enabled bool, fixedIP string) (ClientFixedIPReservation, error) {
	user, err := s.getUser(ctx, snapshot.ClientID)
	if err != nil {
		return ClientFixedIPReservation{}, verificationError("client fixed-IP mutation could not be verified", err)
	}
	if strField(user, "_id", "id") != snapshot.ClientID || resolve.NormalizeMAC(strField(user, "mac", "macAddress")) != snapshot.MAC {
		return ClientFixedIPReservation{}, apperr.New(apperr.Conflict, "client fixed-IP verification failed: observed identity differs from the target")
	}
	if boolField(user, "use_fixedip") != enabled {
		return ClientFixedIPReservation{}, apperr.New(apperr.Conflict, "client fixed-IP verification failed: observed enabled state differs from the request")
	}
	if enabled && (strField(user, "network_id", "networkconf_id") != snapshot.NetworkID || strField(user, "fixed_ip") != fixedIP) {
		return ClientFixedIPReservation{}, apperr.New(apperr.Conflict, "client fixed-IP verification failed: observed reservation differs from the request")
	}
	return ClientFixedIPReservation{
		ClientID: snapshot.ClientID, MAC: snapshot.MAC, Name: snapshot.Name, NetworkID: snapshot.NetworkID,
		FixedIPEnabled: enabled, FixedIP: fixedIP,
	}, nil
}

func reservationFromSnapshot(snapshot ClientFixedIPSnapshot) ClientFixedIPReservation {
	networkID := snapshot.NetworkID
	fixedIP := ""
	if snapshot.FixedIPEnabled {
		fixedIP = snapshot.FixedIP
		if snapshot.ReservationNetworkID != "" {
			networkID = snapshot.ReservationNetworkID
		}
	}
	return ClientFixedIPReservation{
		ClientID: snapshot.ClientID, MAC: snapshot.MAC, Name: snapshot.Name, NetworkID: networkID,
		FixedIPEnabled: snapshot.FixedIPEnabled, FixedIP: fixedIP,
	}
}

func ipv4Broadcast(prefix netip.Prefix) netip.Addr {
	network := prefix.Masked().Addr().As4()
	value := binary.BigEndian.Uint32(network[:])
	hostMask := ^uint32(0) >> prefix.Bits()
	var out [4]byte
	binary.BigEndian.PutUint32(out[:], value|hostMask)
	return netip.AddrFrom4(out)
}
