//go:build windows
// +build windows

package api

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379284.aspx
const (
	NO_MULTIPLE_TRUSTEE = iota
	TRUSTEE_IS_IMPERSONATE
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379638.aspx
const (
	TRUSTEE_IS_SID = iota
	TRUSTEE_IS_NAME
	TRUSTEE_BAD_FORM
	TRUSTEE_IS_OBJECTS_AND_SID
	TRUSTEE_IS_OBJECTS_AND_NAME
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379639.aspx
const (
	TRUSTEE_IS_UNKNOWN = iota
	TRUSTEE_IS_USER
	TRUSTEE_IS_GROUP
	TRUSTEE_IS_DOMAIN
	TRUSTEE_IS_ALIAS
	TRUSTEE_IS_WELL_KNOWN_GROUP
	TRUSTEE_IS_DELETED
	TRUSTEE_IS_INVALID
	TRUSTEE_IS_COMPUTER
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa374899.aspx
const (
	NOT_USED_ACCESS = iota
	GRANT_ACCESS
	SET_ACCESS
	DENY_ACCESS
	REVOKE_ACCESS
	SET_AUDIT_SUCCESS
	SET_AUDIT_FAILURE
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa446627.aspx
const (
	NO_INHERITANCE                     = 0x0
	SUB_OBJECTS_ONLY_INHERIT           = 0x1
	SUB_CONTAINERS_ONLY_INHERIT        = 0x2
	SUB_CONTAINERS_AND_OBJECTS_INHERIT = 0x3
	INHERIT_NO_PROPAGATE               = 0x4
	INHERIT_ONLY                       = 0x8

	OBJECT_INHERIT_ACE       = 0x1
	CONTAINER_INHERIT_ACE    = 0x2
	NO_PROPAGATE_INHERIT_ACE = 0x4
	INHERIT_ONLY_ACE         = 0x8
)

var (
	procSetEntriesInAclW = advapi32.MustFindProc("SetEntriesInAclW")
)

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

	// Pin every pointer the kernel will dereference *transitively* through
	// the entries slice. The unsafe.Pointer->uintptr pinning rule inside
	// syscall.SyscallN only pins what is passed directly as a uintptr arg
	// (here, &entries[0]). The kernel also reads through
	// entries[i].Trustee.Name -- a *uint16 that, depending on TrusteeForm,
	// is either a UTF-16 string or a SID pointer (cast via
	// (*uint16)(unsafe.Pointer(sid))). Those targets are NOT covered by
	// the inline pinning rule, and the typed-as-*uint16 cast obscures the
	// true allocation from escape analysis, which has been the source of
	// delayed memory-corruption crashes on long-running Windows scanners.
	var pinner runtime.Pinner
	defer pinner.Unpin()
	for i := range entries {
		if entries[i].Trustee.Name != nil {
			pinner.Pin(entries[i].Trustee.Name)
		}
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
