//go:build windows

// Package privatefiletest verifies platform-native private permissions in tests.
package privatefiletest

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func AssertDir(t *testing.T, path string) {
	t.Helper()
	assertPrivateDACL(t, path, true)
}

func AssertFile(t *testing.T, path string) {
	t.Helper()
	assertPrivateDACL(t, path, false)
}

type accessCoverage struct {
	effective   bool
	inheritable bool
}

const fileAllAccess windows.ACCESS_MASK = 0x1f01ff

func assertPrivateDACL(t *testing.T, path string, directory bool) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL control = %#x, want SE_DACL_PROTECTED", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("DACL missing")
	}
	wantSIDs := approvedSIDs(t)
	coverage := make(map[string]accessCoverage, len(wantSIDs))
	for sid := range wantSIDs {
		coverage[sid] = accessCoverage{}
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !isFullControl(ace.Mask) {
			t.Fatalf("ACE %d = type:%d flags:%#x mask:%#x", index, ace.Header.AceType, ace.Header.AceFlags, ace.Mask)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, ok := wantSIDs[sid]; !ok {
			t.Fatalf("ACE %d grants unapproved SID %s", index, sid)
		}

		entry := coverage[sid]
		if !directory {
			if ace.Header.AceFlags != windows.NO_INHERITANCE {
				t.Fatalf("file ACE %d has inheritance flags %#x", index, ace.Header.AceFlags)
			}
			entry.effective = true
		} else {
			const inheritanceMask = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
			const permittedFlags = inheritanceMask | windows.INHERIT_ONLY_ACE
			if ace.Header.AceFlags&^permittedFlags != 0 {
				t.Fatalf("directory ACE %d has unexpected flags %#x", index, ace.Header.AceFlags)
			}
			if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE == 0 {
				entry.effective = true
			}
			if ace.Header.AceFlags&inheritanceMask == inheritanceMask {
				entry.inheritable = true
			}
		}
		coverage[sid] = entry
	}
	for sid, entry := range coverage {
		if !entry.effective || directory && !entry.inheritable {
			t.Errorf("SID %s coverage = %+v, want effective access and directory inheritance = %t", sid, entry, directory)
		}
	}
}

func isFullControl(mask windows.ACCESS_MASK) bool {
	return mask == windows.GENERIC_ALL || mask == fileAllAccess
}

func approvedSIDs(t *testing.T) map[string]struct{} {
	t.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]struct{}{
		user.User.Sid.String():  {},
		system.String():         {},
		administrators.String(): {},
	}
}
