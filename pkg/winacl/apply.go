//go:build windows

package winacl

import (
	"runtime"

	"github.com/runZeroInc/go-rod/pkg/winacl/api"
	"golang.org/x/sys/windows"
)

// Apply the provided access control entries to a file. If the replace
// parameter is true, existing entries will be overwritten. If the inherit
// parameter is true, the file will inherit ACEs from its parent.
func Apply(name string, replace, inherit bool, entries ...api.ExplicitAccess) error {
	var oldAcl windows.Handle
	if !replace {
		var secDesc windows.Handle
		api.GetNamedSecurityInfo(
			name,
			api.SE_FILE_OBJECT,
			api.DACL_SECURITY_INFORMATION,
			nil,
			nil,
			&oldAcl,
			nil,
			&secDesc,
		)
		defer windows.LocalFree(secDesc)
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
	if len(entries) > 0 {
		pinner.Pin(&entries[0])
	}
	for i := range entries {
		if entries[i].Trustee.Name != nil {
			pinner.Pin(entries[i].Trustee.Name)
		}
	}

	var acl windows.Handle
	if err := api.SetEntriesInAcl(
		entries,
		oldAcl,
		&acl,
	); err != nil {
		return err
	}
	defer windows.LocalFree(acl)

	var secInfo uint32
	if !inherit {
		secInfo = api.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		secInfo = api.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	return api.SetNamedSecurityInfo(
		name,
		api.SE_FILE_OBJECT,
		api.DACL_SECURITY_INFORMATION|secInfo,
		nil,
		nil,
		acl,
		0,
	)
}
