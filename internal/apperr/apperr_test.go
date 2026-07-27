package apperr_test

import (
	"errors"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/apperr"
)

func TestErrorString(t *testing.T) {
	err := apperr.New(apperr.NotFound, "device missing")
	if got := err.Error(); got != "not_found: device missing" {
		t.Fatalf("Error() = %q", got)
	}
	withHint := apperr.WithHint(apperr.New(apperr.AuthFailed, "bad creds"), "check API key")
	if got := withHint.Error(); got != "auth_failed: bad creds (check API key)" {
		t.Fatalf("Error() with hint = %q", got)
	}
}

func TestNewf(t *testing.T) {
	err := apperr.Newf(apperr.AmbiguousID, "matched %d devices", 3)
	if err.Message != "matched 3 devices" {
		t.Fatalf("message = %q", err.Message)
	}
}

func TestIsAndAs(t *testing.T) {
	err := apperr.New(apperr.ValidationFailed, "bad input")
	if !apperr.Is(err, apperr.ValidationFailed) {
		t.Fatal("Is should match code")
	}
	if apperr.Is(err, apperr.NotFound) {
		t.Fatal("Is should not match other code")
	}
	if apperr.Is(errors.New("plain"), apperr.Internal) {
		t.Fatal("Is should reject plain errors")
	}
	if got := apperr.As(err); got == nil || got.Code != apperr.ValidationFailed {
		t.Fatalf("As = %+v", got)
	}
	if apperr.As(errors.New("plain")) != nil {
		t.Fatal("As should return nil for plain errors")
	}
}
