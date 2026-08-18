package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/config"
)

func FuzzStrictYAMLConfiguration(f *testing.F) {
	for _, seed := range []string{
		"host: controller.example\n",
		"host: controller.example\nsite: default\nsafe_mode: true\n",
		"host: controller.example\nsaef_mode: false\n",
		"---\nhost: controller.example\n---\nhost: other.example\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = config.Load(path)
	})
}
