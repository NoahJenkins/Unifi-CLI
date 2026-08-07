//go:build !windows

package privatefile

import "os"

func protectDir(path string) error {
	return os.Chmod(path, 0o700)
}

func protectFile(path string) error {
	return os.Chmod(path, 0o600)
}
