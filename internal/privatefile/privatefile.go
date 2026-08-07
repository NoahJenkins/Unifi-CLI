// Package privatefile creates and protects directories and files that contain
// local security-sensitive state.
package privatefile

import (
	"fmt"
	"os"
)

// EnsureDir creates path when needed and restricts it to the current user and
// operating-system recovery principals.
func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := protectDir(path); err != nil {
		return fmt.Errorf("protect private directory: %w", err)
	}
	return nil
}

// ProtectFile restricts an existing file to the current user and
// operating-system recovery principals.
func ProtectFile(path string) error {
	if err := protectFile(path); err != nil {
		return fmt.Errorf("protect private file: %w", err)
	}
	return nil
}
