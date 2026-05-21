//go:build windows

package api

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var logonUserW = windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")

// Do not inherit the parent error handling mode for the subprocess
const CREATE_DEFAULT_ERROR_MODE = 0x04000000

type LogonType int32

const (
	// LOGON32_LOGON_BATCH is intended for batch servers, where
	// processes may be executing on behalf of a user without their
	// direct intervention. This type is also for higher performance
	// servers that process many plaintext authentication attempts at a
	// time, such as mail or web servers.
	LOGON32_LOGON_BATCH LogonType = 4

	// LOGON32_LOGON_INTERACTIVE is intended for users who will be
	// interactively using the computer, such as a user being logged on
	// by a terminal server, remote shell, or similar process. This
	// logon type has the additional expense of caching logon
	// information for disconnected operations; therefore, it is
	// inappropriate for some client/server applications, such as a
	// mail server.
	LOGON32_LOGON_INTERACTIVE LogonType = 2

	// LOGON32_LOGON_NETWORK is intended for high performance servers to
	// authenticate plaintext passwords. The LogonUser function does not
	// cache credentials for this logon type.
	LOGON32_LOGON_NETWORK LogonType = 3

	// LOGON32_LOGON_NETWORK_CLEARTEXT preserves the name and password
	// in the authentication package, which allows the server to make
	// connections to other network servers while impersonating the
	// client. A server can accept plaintext credentials from a client,
	// call LogonUser, verify that the user can access the system across
	// the network, and still communicate with other servers.
	LOGON32_LOGON_NETWORK_CLEARTEXT LogonType = 8

	// LOGON32_LOGON_NEW_CREDENTIALS allows the caller to clone its
	// current token and specify new credentials for outbound
	// connections. The new logon session has the same local identifier
	// but uses different credentials for other network connections.
	//
	// This logon type is supported only by the LOGON32_PROVIDER_WINNT50
	// logon provider.
	LOGON32_LOGON_NEW_CREDENTIALS LogonType = 9

	// LOGON32_LOGON_SERVICE indicates a service-type logon. The account
	// provided must have the service privilege enabled.
	LOGON32_LOGON_SERVICE LogonType = 5

	// LOGON32_LOGON_UNLOCK GINAs are no longer supported.
	//
	// Windows Server 2003 and Windows XP:  This logon type is for GINA
	// DLLs that log on users who will be interactively using the
	// computer. This logon type can generate a unique audit record that
	// shows when the workstation was unlocked.
	//LOGON32_LOGON_UNLOCK LogonType = 7
)

// Copied from MS documentation:

type LogonProvider int32

const (
	// LOGON32_PROVIDER_DEFAULT uses the standard logon provider for the
	// system. The default security provider is negotiate, unless you
	// pass NULL for the domain name and the user name is not in UPN
	// format. In this case, the default provider is NTLM.
	LOGON32_PROVIDER_DEFAULT LogonProvider = 0

	// LOGON32_PROVIDER_WINNT40 uses the NTLM logon provider.
	// LOGON32_PROVIDER_WINNT40 LogonProvider = 1

	// LOGON32_PROVIDER_WINNT40 uses the NTLM logon provider.
	// LOGON32_PROVIDER_WINNT40 LogonProvider = 2

	// LOGON32_PROVIDER_WINNT50 uses the negotiate logon provider.
	LOGON32_PROVIDER_WINNT50 LogonProvider = 3
)

func LogonUser(username, domain, password string, logonType LogonType, logonProvider LogonProvider) (windows.Token, error) {
	pUsername, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return 0, fmt.Errorf("user: %w", err)
	}

	// If domain is empty, pass a null pointer instead. Hold the actual
	// *uint16 returned by UTF16PtrFromString in a typed local so the
	// compiler's pointer-pinning rule for syscall.SyscallN arguments
	// can keep it alive.
	var pDomain *uint16
	if domain != "" {
		pDomain, err = windows.UTF16PtrFromString(domain)
		if err != nil {
			return 0, fmt.Errorf("domain: %w", err)
		}
	}

	// If password is empty, pass a null pointer instead
	var pPassword *uint16
	if password != "" {
		pPassword, err = windows.UTF16PtrFromString(password)
		if err != nil {
			return 0, fmt.Errorf("password: %w", err)
		}
	}

	hToken := uintptr(0)
	// syscall.SyscallN (asm) triggers the unsafe.Pointer->uintptr pinning
	// rule; (*Proc).Call (Go) does not, so the GC may relocate the UTF-16
	// buffers while LogonUserW is reading them.
	res, _, err := syscall.SyscallN(
		logonUserW.Addr(),
		uintptr(unsafe.Pointer(pUsername)),
		uintptr(unsafe.Pointer(pDomain)),
		uintptr(unsafe.Pointer(pPassword)),
		uintptr(logonType),
		uintptr(logonProvider),
		uintptr(unsafe.Pointer(&hToken)),
	)
	runtime.KeepAlive(pUsername)
	runtime.KeepAlive(pDomain)
	runtime.KeepAlive(pPassword)
	if res != 0 {
		return windows.Token(hToken), nil
	}
	return 0, err
}
