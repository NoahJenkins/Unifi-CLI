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
	assertPrivateDACL(t, path, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

func AssertFile(t *testing.T, path string) {
	t.Helper()
	assertPrivateDACL(t, path, windows.NO_INHERITANCE)
}

func assertPrivateDACL(t *testing.T, path string, wantInheritance uint8) {
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
	dacl, present, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if !present || dacl == nil {
		t.Fatal("DACL missing")
	}
	wantSIDs := approvedSIDs(t)
	if got, want := int(dacl.AceCount), len(wantSIDs); got != want {
		t.Fatalf("ACE count = %d, want %d", got, want)
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != wantInheritance || ace.Mask != windows.GENERIC_ALL {
			t.Fatalf("ACE %d = type:%d flags:%#x mask:%#x", index, ace.Header.AceType, ace.Header.AceFlags, ace.Mask)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, ok := wantSIDs[sid]; !ok {
			t.Fatalf("ACE %d grants unapproved SID %s", index, sid)
		}
		delete(wantSIDs, sid)
	}
	if len(wantSIDs) != 0 {
		t.Fatalf("DACL missing approved SIDs: %v", wantSIDs)
	}
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
