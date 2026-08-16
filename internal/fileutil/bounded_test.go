package fileutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRegularFile(path, 5)
	if err != nil || string(got) != "hello" {
		t.Fatalf("ReadRegularFile() = %q, %v", got, err)
	}
}

func TestReadRegularFileRejectsUnsafeInputs(t *testing.T) {
	dir := t.TempDir()
	tooLarge := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(tooLarge, []byte("123456"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		path    string
		maximum int64
		want    string
	}{
		"missing":   {filepath.Join(dir, "missing"), 10, "no such file"},
		"directory": {dir, 10, "not a regular file"},
		"too large": {tooLarge, 5, "exceeds 5 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadRegularFile(tt.path, tt.maximum)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ReadRegularFile() error = %v, want %q", err, tt.want)
			}
		})
	}
}
