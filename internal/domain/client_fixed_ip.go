package domain

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/netip"
	"sort"

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

type clientFixedIPUser struct {
	ID             string
	MAC            string
	Name           string
	NetworkID      string
	FixedIPEnabled bool
	FixedIP        string
}

func (u clientFixedIPUser) GetID() string   { return u.ID }
func (u clientFixedIPUser) GetMAC() string  { return u.MAC }
func (u clientFixedIPUser) GetName() string { return u.Name }

func NewClientFixedIPService(api ClientAPI) *ClientFixedIPService {
	return &ClientFixedIPService{api: api}
}

func (s *ClientFixedIPService) List(ctx context.Context) ([]ClientFixedIPReservation, error) {
	users, err := s.listUsers(ctx)
	if err != nil {
		return nil, err
	}
	reservations := make([]ClientFixedIPReservation, 0, len(users))
	for _, user := range users {
		if !user.FixedIPEnabled {
			continue
		}
		if err := validateFixedIPUserIdentity(user); err != nil {
			return nil, err
		}
		reservations = append(reservations, reservationFromUser(user))
	}
	sort.Slice(reservations, func(i, j int) bool {
		left := reservations[i]
		right := reservations[j]
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.MAC != right.MAC {
			return left.MAC < right.MAC
		}
		return left.ClientID < right.ClientID
	})
	return reservations, nil
}

func (s *ClientFixedIPService) Get(ctx context.Context, query string) (ClientFixedIPReservation, error) {
	user, err := s.resolveUser(ctx, query)
	if err != nil {
		return ClientFixedIPReservation{}, err
	}
	return reservationFromUser(user), nil
}

func (s *ClientFixedIPService) Set(ctx context.Context, query, fixedIP string) (plan.Plan, ClientFixedIPSnapshot, error) {
	return s.prepare(ctx, query, fixedIP, "", true)
}

// SetOnNetwork plans a reservation on an explicitly selected legacy network.
// The selector is required when the user record has no stored network ID and
// must match the stored network when one exists.
func (s *ClientFixedIPService) SetOnNetwork(ctx context.Context, query, fixedIP, network string) (plan.Plan, ClientFixedIPSnapshot, error) {
	return s.prepare(ctx, query, fixedIP, network, true)
}

func (s *ClientFixedIPService) Clear(ctx context.Context, query string) (plan.Plan, ClientFixedIPSnapshot, error) {
	return s.prepare(ctx, query, "", "", false)
}

func (s *ClientFixedIPService) prepare(ctx context.Context, query, fixedIP, networkQuery string, enable bool) (plan.Plan, ClientFixedIPSnapshot, error) {
	user, err := s.resolveUser(ctx, query)
	if err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}
	if err := validateFixedIPUserIdentity(user); err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}
	networkID, err := s.reservationNetworkID(ctx, user, networkQuery)
	if err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}
	network, err := s.getNetwork(ctx, networkID)
	if err != nil {
		return plan.Plan{}, ClientFixedIPSnapshot{}, err
	}

	snapshot := ClientFixedIPSnapshot{
		ClientID: user.ID, MAC: user.MAC, Name: user.Name, NetworkID: networkID,
		ReservationNetworkID: user.NetworkID,
		FixedIPEnabled:       user.FixedIPEnabled, FixedIP: user.FixedIP,
		Subnet: network.Subnet, DHCPEnabled: network.DHCPEnabled,
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

// reservationNetworkID returns the legacy network that a set or clear binds
// to. A stored user network is authoritative. Without one, the caller must
// select the network, and a connected client must currently use it.
func (s *ClientFixedIPService) reservationNetworkID(ctx context.Context, user clientFixedIPUser, networkQuery string) (string, error) {
	if networkQuery == "" {
		if user.NetworkID == "" {
			return "", apperr.WithHint(
				apperr.New(apperr.Conflict, "client fixed-IP user record has no network ID"),
				"select the reservation network with --network <id|exact-name>",
			)
		}
		return user.NetworkID, nil
	}
	selected, err := NewNetworkService(s.api).getLegacy(ctx, networkQuery)
	if err != nil {
		return "", err
	}
	if selected.ID == "" {
		return "", apperr.New(apperr.Conflict, "selected client network has no immutable ID")
	}
	if user.NetworkID != "" {
		if user.NetworkID != selected.ID {
			return "", apperr.New(apperr.Conflict, "client fixed-IP user record belongs to a different network than --network")
		}
		return selected.ID, nil
	}
	if err := s.requireConnectedClientOnNetwork(ctx, user, selected); err != nil {
		return "", err
	}
	return selected.ID, nil
}

// requireConnectedClientOnNetwork cross-checks an explicit network selection
// against the client's current connection. An offline client has no current
// address to compare, so the explicit selection and later subnet checks apply.
func (s *ClientFixedIPService) requireConnectedClientOnNetwork(ctx context.Context, user clientFixedIPUser, selected Network) error {
	clients, err := NewClientService(s.api).listLegacy(ctx)
	if err != nil {
		return err
	}
	for _, connected := range clients {
		if resolve.NormalizeMAC(connected.MAC) != user.MAC {
			continue
		}
		if connected.IP != "" {
			addr, err := netip.ParseAddr(connected.IP)
			prefix, prefixErr := netip.ParsePrefix(selected.Subnet)
			if err != nil || !addr.Is4() || prefixErr != nil || !prefix.Addr().Is4() || !prefix.Contains(addr) {
				return apperr.New(apperr.Conflict, "client's current IPv4 address is not inside the selected network subnet")
			}
		}
		if connected.networkID != "" && connected.networkID != selected.ID {
			return apperr.New(apperr.Conflict, "client is currently connected to a different network than --network")
		}
	}
	return nil
}

func (s *ClientFixedIPService) listUsers(ctx context.Context) ([]clientFixedIPUser, error) {
	var raw []map[string]any
	if err := s.api.Do(ctx, http.MethodGet, s.api.SitePath(client.PathRestUser), nil, &raw); err != nil {
		return nil, err
	}
	users := make([]clientFixedIPUser, 0, len(raw))
	for _, item := range raw {
		users = append(users, normalizeFixedIPUser(item))
	}
	return users, nil
}

func (s *ClientFixedIPService) resolveUser(ctx context.Context, query string) (clientFixedIPUser, error) {
	users, err := s.listUsers(ctx)
	if err != nil {
		return clientFixedIPUser{}, err
	}
	user, err := resolve.One(users, query)
	if err == nil || !apperr.Is(err, apperr.NotFound) || !looksLikeUUID(query) {
		return user, err
	}

	raw, official, fetchErr := fetchOfficialSite(s.api, ctx, "clients")
	if fetchErr != nil {
		return clientFixedIPUser{}, fetchErr
	}
	if !official {
		return clientFixedIPUser{}, err
	}
	officialClients := make([]Client, 0, len(raw))
	for _, item := range raw {
		officialClients = append(officialClients, NormalizeClient(item))
	}
	officialClient, resolveErr := resolve.One(officialClients, query)
	if resolveErr != nil {
		return clientFixedIPUser{}, resolveErr
	}
	return resolve.One(users, officialClient.MAC)
}

func normalizeFixedIPUser(user map[string]any) clientFixedIPUser {
	return clientFixedIPUser{
		ID:             strField(user, "_id", "id"),
		MAC:            resolve.NormalizeMAC(strField(user, "mac", "macAddress")),
		Name:           strField(user, "name", "hostname"),
		NetworkID:      strField(user, "network_id", "networkconf_id"),
		FixedIPEnabled: boolField(user, "use_fixedip"),
		FixedIP:        strField(user, "fixed_ip"),
	}
}

func validateFixedIPUserIdentity(user clientFixedIPUser) error {
	if user.ID == "" {
		return apperr.New(apperr.Conflict, "client fixed-IP user record has no immutable ID")
	}
	if user.MAC == "" {
		return apperr.New(apperr.Conflict, "client fixed-IP user record has no valid MAC address")
	}
	return nil
}

func reservationFromUser(user clientFixedIPUser) ClientFixedIPReservation {
	fixedIP := ""
	if user.FixedIPEnabled {
		fixedIP = user.FixedIP
	}
	return ClientFixedIPReservation{
		ClientID: user.ID, MAC: user.MAC, Name: user.Name, NetworkID: user.NetworkID,
		FixedIPEnabled: user.FixedIPEnabled, FixedIP: fixedIP,
	}
}

func (s *ClientFixedIPService) ApplySetPrepared(ctx context.Context, target plan.Target, id, fixedIP string) (ClientFixedIPReservation, error) {
	return s.ApplySetOnNetworkPrepared(ctx, target, id, fixedIP, "")
}

func (s *ClientFixedIPService) ApplySetOnNetworkPrepared(ctx context.Context, target plan.Target, id, fixedIP, network string) (ClientFixedIPReservation, error) {
	_, snapshot, err := s.SetOnNetwork(ctx, id, fixedIP, network)
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
	if enabled {
		network, err := s.getNetwork(ctx, snapshot.NetworkID)
		if err != nil {
			return ClientFixedIPReservation{}, verificationError("client fixed-IP network could not be verified", err)
		}
		if network.Subnet != snapshot.Subnet || network.DHCPEnabled != snapshot.DHCPEnabled {
			return ClientFixedIPReservation{}, apperr.New(apperr.Conflict, "client fixed-IP verification failed: observed network differs from the prepared network")
		}
	}
	return ClientFixedIPReservation{
		ClientID: snapshot.ClientID, MAC: snapshot.MAC, Name: snapshot.Name, NetworkID: snapshot.NetworkID,
		FixedIPEnabled: enabled, FixedIP: fixedIP,
	}, nil
}

func reservationFromSnapshot(snapshot ClientFixedIPSnapshot) ClientFixedIPReservation {
	// ReservationNetworkID is the stored user value; it is empty when the
	// plan selects the network explicitly.
	networkID := snapshot.ReservationNetworkID
	fixedIP := ""
	if snapshot.FixedIPEnabled {
		fixedIP = snapshot.FixedIP
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
