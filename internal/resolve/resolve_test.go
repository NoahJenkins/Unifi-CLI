package resolve_test

import (
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
	"github.com/noahjenkins/unifi-cli/internal/resolve"
)

type item struct{ id, mac, name string }

func (i item) GetID() string   { return i.id }
func (i item) GetMAC() string  { return i.mac }
func (i item) GetName() string { return i.name }

func TestResolveByMACNormalized(t *testing.T) {
	items := []item{{"1", "aa:bb:cc:dd:ee:ff", "ap1"}}
	got, err := resolve.One(items, "AA-BB-CC-DD-EE-FF")
	if err != nil || got.id != "1" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestResolveAmbiguousName(t *testing.T) {
	items := []item{{"1", "aa:bb:cc:dd:ee:01", "ap"}, {"2", "aa:bb:cc:dd:ee:02", "ap"}}
	_, err := resolve.One(items, "ap")
	if !apperr.Is(err, apperr.AmbiguousID) {
		t.Fatalf("expected ambiguous_id, got %v", err)
	}
}

func TestResolveNotFound(t *testing.T) {
	_, err := resolve.One([]item{}, "nope")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("expected not_found, got %v", err)
	}
}
