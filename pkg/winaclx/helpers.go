//go:build windows

package winacl

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ChownByUsername(path string, username string) error {
	_ = Chmod(path, 0o755)
	return osWindowsApplyACL(path, false, true,
		GrantName(windows.GENERIC_ALL, username),
		GrantName(windows.MAXIMUM_ALLOWED, username),
	)
}

func GetTokenUser(token windows.Token) (string, error) {
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("GetTokenUser failed: %v", err)
	}
	sid := tokenUser.User.Sid.String()

	return sid, nil
}

func GetCurrentProcessTokenUser() (string, string, *windows.SID, error) {
	var token windows.Token
	// Get the current process token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return "", "", nil, fmt.Errorf("OpenProcessToken failed: %v", err)
	}
	defer token.Close()

	// Retrieve the token user
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return "", "", nil, fmt.Errorf("GetTokenUser failed: %v", err)
	}

	// Convert SID to a readable string
	account, domain, _, err := tokenUser.User.Sid.LookupAccount("")
	if err != nil {
		return "", "", nil, fmt.Errorf("LookupAccount %s failed: %v", tokenUser.User.Sid.String(), err)
	}
	return account, domain, tokenUser.User.Sid, err
}

func LogonUser(username, domain, password string, logonType LogonType, logonProvider LogonProvider) (windows.Token, error) {
	pUsername, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return 0, fmt.Errorf("user: %w", err)
	}

	// Hold the *uint16 returned by UTF16PtrFromString in a typed local so
	// the compiler's pointer-pinning rule for syscall.SyscallN arguments
	// can keep it alive.
	var pDomain *uint16
	if domain != "" {
		pDomain, err = windows.UTF16PtrFromString(domain)
		if err != nil {
			return 0, fmt.Errorf("domain: %w", err)
		}
	}

	var pPassword *uint16
	if password != "" {
		pPassword, err = windows.UTF16PtrFromString(password)
		if err != nil {
			return 0, fmt.Errorf("password: %w", err)
		}
	}

	hToken := uintptr(0)
	// syscall.SyscallN (asm) triggers the unsafe.Pointer->uintptr pinning
	// rule; (*Proc).Call (Go) does not.
	res, _, err := syscall.SyscallN(
		logonUserW.Addr(),
		uintptr(unsafe.Pointer(pUsername)),
		uintptr(unsafe.Pointer(pDomain)),
		uintptr(unsafe.Pointer(pPassword)),
		uintptr(logonType),
		uintptr(logonProvider),
		uintptr(unsafe.Pointer(&hToken)),
	)
	if res != 0 {
		return windows.Token(hToken), nil
	}
	return 0, err
}

// Change the permissions of the specified file. Only the nine
// least-significant bytes are used, allowing access by the file's owner, the
// file's group, and everyone else to be explicitly controlled.
func Chmod(name string, fileMode os.FileMode) error {
	// https://support.microsoft.com/en-us/help/243330/well-known-security-identifiers-in-windows-operating-systems
	creatorOwnerSID, err := windows.StringToSid("S-1-3-0")
	if err != nil {
		return err
	}
	creatorGroupSID, err := windows.StringToSid("S-1-3-1")
	if err != nil {
		return err
	}
	everyoneSID, err := windows.StringToSid("S-1-1-0")
	if err != nil {
		return err
	}

	mode := uint32(fileMode)
	return osWindowsApplyACL(
		name,
		true,
		false,
		GrantSid(((mode&0700)<<23)|((mode&0200)<<9), creatorOwnerSID),
		GrantSid(((mode&0070)<<26)|((mode&0020)<<12), creatorGroupSID),
		GrantSid(((mode&0007)<<29)|((mode&0002)<<15), everyoneSID),
	)
}

// Create an ExplicitAccess instance granting permissions to the provided SID.
func GrantSid(accessPermissions uint32, sid *windows.SID) ExplicitAccess {
	return ExplicitAccess{
		AccessPermissions: accessPermissions,
		AccessMode:        GRANT_ACCESS,
		Inheritance:       SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: Trustee{
			TrusteeForm: TRUSTEE_IS_SID,
			Name:        (*uint16)(unsafe.Pointer(sid)),
		},
	}
}

// Create an ExplicitAccess instance granting permissions to the provided name.
func GrantName(accessPermissions uint32, name string) ExplicitAccess {
	return ExplicitAccess{
		AccessPermissions: accessPermissions,
		AccessMode:        GRANT_ACCESS,
		Inheritance:       SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: Trustee{
			TrusteeForm: TRUSTEE_IS_NAME,
			Name:        windows.StringToUTF16Ptr(name),
		},
	}
}

// Create an ExplicitAccess instance denying permissions to the provided SID.
func DenySid(accessPermissions uint32, sid *windows.SID) ExplicitAccess {
	return ExplicitAccess{
		AccessPermissions: accessPermissions,
		AccessMode:        DENY_ACCESS,
		Inheritance:       SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: Trustee{
			TrusteeForm: TRUSTEE_IS_SID,
			Name:        (*uint16)(unsafe.Pointer(sid)),
		},
	}
}

// Create an ExplicitAccess instance denying permissions to the provided name.
func DenyName(accessPermissions uint32, name string) ExplicitAccess {
	return ExplicitAccess{
		AccessPermissions: accessPermissions,
		AccessMode:        DENY_ACCESS,
		Inheritance:       SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: Trustee{
			TrusteeForm: TRUSTEE_IS_NAME,
			Name:        windows.StringToUTF16Ptr(name),
		},
	}
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa446645.aspx
func GetNamedSecurityInfo(objectName string, objectType int32, secInfo uint32, owner, group **windows.SID, dacl, sacl, secDesc *windows.Handle) error {
	namePtr := windows.StringToUTF16Ptr(objectName)
	ret, _, _ := syscall.SyscallN(
		procGetNamedSecurityInfoW.Addr(),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(objectType),
		uintptr(secInfo),
		uintptr(unsafe.Pointer(owner)),
		uintptr(unsafe.Pointer(group)),
		uintptr(unsafe.Pointer(dacl)),
		uintptr(unsafe.Pointer(sacl)),
		uintptr(unsafe.Pointer(secDesc)),
	)
	if ret != 0 {
		return windows.Errno(ret)
	}
	return nil
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379579.aspx
func SetNamedSecurityInfo(objectName string, objectType int32, secInfo uint32, owner, group *windows.SID, dacl, sacl windows.Handle) error {
	namePtr := windows.StringToUTF16Ptr(objectName)
	ret, _, _ := syscall.SyscallN(
		procSetNamedSecurityInfoW.Addr(),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(objectType),
		uintptr(secInfo),
		uintptr(unsafe.Pointer(owner)),
		uintptr(unsafe.Pointer(group)),
		uintptr(dacl),
		uintptr(sacl),
	)
	if ret != 0 {
		return windows.Errno(ret)
	}
	return nil
}

// osWindowsApplyACL the provided access control entries to a file. If the replace
// parameter is true, existing entries will be overwritten. If the inherit
// parameter is true, the file will inherit ACEs from its parent.
func osWindowsApplyACL(name string, replace, inherit bool, entries ...ExplicitAccess) error {
	var oldAcl windows.Handle
	if !replace {
		var secDesc windows.Handle
		GetNamedSecurityInfo(
			name,
			SE_FILE_OBJECT,
			DACL_SECURITY_INFORMATION,
			nil,
			nil,
			&oldAcl,
			nil,
			&secDesc,
		)
		defer windows.LocalFree(secDesc)
	}

	// Pin every pointer the kernel will dereference *transitively* through
	// entries[i].Trustee.Name. The unsafe.Pointer->uintptr pinning rule in
	// syscall.SyscallN only pins direct arguments (&entries[0]); pointers
	// nested inside that struct -- here, the SID or UTF-16 buffer cast to
	// *uint16 -- are NOT covered, and the cast through a smaller type
	// obscures the true allocation from escape analysis.
	var pinner runtime.Pinner
	defer pinner.Unpin()
	if len(entries) > 0 {
		pinner.Pin(&entries[0])
	}
	for i := range entries {
		if entries[i].Trustee.Name != nil {
			pinner.Pin(entries[i].Trustee.Name)
		}
	}

	var acl windows.Handle
	if err := SetEntriesInAcl(
		entries,
		oldAcl,
		&acl,
	); err != nil {
		return err
	}
	defer windows.LocalFree(acl)

	var secInfo uint32
	if !inherit {
		secInfo = PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		secInfo = UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	return SetNamedSecurityInfo(
		name,
		SE_FILE_OBJECT,
		DACL_SECURITY_INFORMATION|secInfo,
		nil,
		nil,
		acl,
		0,
	)
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa446585.aspx
func CreateWellKnownSid(sidType int32, sidDomain, sid *windows.SID, sidLen *uint32) error {
	ret, _, err := syscall.SyscallN(
		procCreateWellKnownSid.Addr(),
		uintptr(sidType),
		uintptr(unsafe.Pointer(sidDomain)),
		uintptr(unsafe.Pointer(sid)),
		uintptr(unsafe.Pointer(sidLen)),
	)
	if ret == 0 {
		return err
	}
	return nil
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379636.aspx
type Trustee struct {
	MultipleTrustee          *Trustee
	MultipleTrusteeOperation int32
	TrusteeForm              int32
	TrusteeType              int32
	Name                     *uint16
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa446627.aspx
type ExplicitAccess struct {
	AccessPermissions uint32
	AccessMode        int32
	Inheritance       uint32
	Trustee           Trustee
}

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379576.aspx
func SetEntriesInAcl(entries []ExplicitAccess, oldAcl windows.Handle, newAcl *windows.Handle) error {
	if len(entries) == 0 {
		return nil
	}

	ret, _, _ := syscall.SyscallN(
		procSetEntriesInAclW.Addr(),
		uintptr(len(entries)),
		uintptr(unsafe.Pointer(&entries[0])),
		uintptr(oldAcl),
		uintptr(unsafe.Pointer(newAcl)),
	)
	if ret != 0 {
		return windows.Errno(ret)
	}
	return nil
}
