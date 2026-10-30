package contractapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfilesFailClosed(t *testing.T) {
	if _, err := ProfileFor("10.5.67"); err == nil {
		t.Fatal("unknown profile accepted")
	}
	for _, version := range []string{"10.4.57", "10.6.109"} {
		p, err := ProfileFor(version)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if _, err := NewValidator(p, dir); err == nil {
			t.Fatal("missing opted-in cache accepted")
		}
		if err := os.WriteFile(filepath.Join(dir, version+".json"), []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewValidator(p, dir); err == nil {
			t.Fatal("wrong digest accepted")
		}
	}
}
