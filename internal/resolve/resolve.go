package resolve

import (
	"strings"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

type Identifiable interface {
	GetID() string
	GetMAC() string
	GetName() string
}

func NormalizeMAC(s string) string {
	s = strings.ToLower(s)
	repl := strings.NewReplacer("-", "", ":", "", ".", "")
	return repl.Replace(s)
}

func One[T Identifiable](items []T, query string) (T, error) {
	var zero T
	q := strings.TrimSpace(query)
	if q == "" {
		return zero, apperr.New(apperr.ValidationFailed, "identifier is required")
	}
	var matches []T
	macQ := NormalizeMAC(q)
	for _, it := range items {
		if it.GetID() == q {
			return it, nil
		}
	}
	for _, it := range items {
		if it.GetMAC() != "" && NormalizeMAC(it.GetMAC()) == macQ {
			matches = append(matches, it)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return zero, apperr.Newf(apperr.AmbiguousID, "multiple matches for %q", q)
	}
	matches = nil
	for _, it := range items {
		if it.GetName() == q {
			matches = append(matches, it)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return zero, apperr.Newf(apperr.AmbiguousID, "multiple matches for %q", q)
	}
	return zero, apperr.WithHint(
		apperr.Newf(apperr.NotFound, "not found: %s", q),
		"list resources and use id, mac, or exact name",
	)
}
