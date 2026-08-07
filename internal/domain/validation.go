package domain

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

func validateRequired(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return apperr.Newf(apperr.ValidationFailed, "%s is required", field)
	}
	return nil
}

func validateVLAN(vlan *int) error {
	if vlan != nil && (*vlan < 1 || *vlan > 4094) {
		return apperr.New(apperr.ValidationFailed, "VLAN must be between 1 and 4094")
	}
	return nil
}

func validateCIDR(field, value string) error {
	if value == "" {
		return nil
	}
	if _, err := netip.ParsePrefix(value); err != nil {
		return apperr.Newf(apperr.ValidationFailed, "%s must be a valid CIDR", field)
	}
	return nil
}

func validateIPOrCIDR(field, value string) error {
	if value == "" || strings.EqualFold(value, "any") {
		return nil
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return nil
	}
	if _, err := netip.ParsePrefix(value); err == nil {
		return nil
	}
	return apperr.Newf(apperr.ValidationFailed, "%s must be an IP address, CIDR, or any", field)
}

func validateEnum(field, value string, allowed ...string) error {
	if value == "" {
		return nil
	}
	normalized := strings.ToLower(value)
	if !slices.Contains(allowed, normalized) {
		return apperr.Newf(apperr.ValidationFailed, "%s %q is unsupported", field, value)
	}
	return nil
}

func validatePortIndex(portIdx int) error {
	if portIdx < 1 {
		return apperr.New(apperr.ValidationFailed, "port index must be at least 1")
	}
	return nil
}
