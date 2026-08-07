//go:build windows

package privatefile

import "golang.org/x/sys/windows"

func protectDir(path string) error {
	return setPrivateDACL(path, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

func protectFile(path string) error {
	return setPrivateDACL(path, windows.NO_INHERITANCE)
}

func setPrivateDACL(path string, inheritance uint32) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}

	entries := []windows.EXPLICIT_ACCESS{
		fullControlEntry(user.User.Sid, inheritance),
		fullControlEntry(system, inheritance),
		fullControlEntry(administrators, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}

func fullControlEntry(sid *windows.SID, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
