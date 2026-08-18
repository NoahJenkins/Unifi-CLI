package authstore_test

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/noahjenkins/unifi-cli/internal/authstore"
)

func TestNativeKeyringRoundTrip(t *testing.T) {
	if os.Getenv("UNIFI_NATIVE_KEYRING_IT") != "1" {
		t.Skip("set UNIFI_NATIVE_KEYRING_IT=1 to test the native credential store")
	}
	controller := fmt.Sprintf("https://native-keyring-test-%d.invalid:443", time.Now().UnixNano())
	store := authstore.NewStore(authstore.Options{StateHome: t.TempDir(), GOOS: runtime.GOOS})
	t.Cleanup(func() { _ = store.Delete(controller) })

	if err := store.Save(controller, "synthetic-key-one", false); err != nil {
		t.Fatalf("save native key: %v", err)
	}
	if got, found, err := store.Load(controller); err != nil || !found || got != "synthetic-key-one" {
		t.Fatalf("load native key = %q, %v, %v", got, found, err)
	}
	if err := store.Save(controller, "synthetic-key-two", false); err != nil {
		t.Fatalf("overwrite native key: %v", err)
	}
	if got, found, err := store.Load(controller); err != nil || !found || got != "synthetic-key-two" {
		t.Fatalf("load overwritten native key = %q, %v, %v", got, found, err)
	}
	if err := store.Delete(controller); err != nil {
		t.Fatalf("delete native key: %v", err)
	}
	if got, found, err := store.Load(controller); err != nil || found || got != "" {
		t.Fatalf("load deleted native key = %q, %v, %v", got, found, err)
	}
}
