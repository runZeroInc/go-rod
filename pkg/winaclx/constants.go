//go:build windows

package winacl

import (
	"golang.org/x/sys/windows"
)

/*
	https://github.com/hectane/go-acl

	The MIT License (MIT)

	Copyright (c) 2015 Nathan Osman

	Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

	The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/

var (
	logonUserW = windows.NewLazySystemDLL("advapi32.dll").NewProc("LogonUserW")
	advapi32   = windows.MustLoadDLL("advapi32.dll")
)

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

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379593.aspx
const (
	SE_UNKNOWN_OBJECT_TYPE = iota
	SE_FILE_OBJECT
	SE_SERVICE
	SE_PRINTER
	SE_REGISTRY_KEY
	SE_LMSHARE
	SE_KERNEL_OBJECT
	SE_WINDOW_OBJECT
	SE_DS_OBJECT
	SE_DS_OBJECT_ALL
	SE_PROVIDER_DEFINED_OBJECT
	SE_WMIGUID_OBJECT
	SE_REGISTRY_WOW64_32KEY
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379573.aspx
const (
	OWNER_SECURITY_INFORMATION               = 0x00001
	GROUP_SECURITY_INFORMATION               = 0x00002
	DACL_SECURITY_INFORMATION                = 0x00004
	SACL_SECURITY_INFORMATION                = 0x00008
	LABEL_SECURITY_INFORMATION               = 0x00010
	ATTRIBUTE_SECURITY_INFORMATION           = 0x00020
	SCOPE_SECURITY_INFORMATION               = 0x00040
	PROCESS_TRUST_LABEL_SECURITY_INFORMATION = 0x00080
	BACKUP_SECURITY_INFORMATION              = 0x10000

	PROTECTED_DACL_SECURITY_INFORMATION   = 0x80000000
	PROTECTED_SACL_SECURITY_INFORMATION   = 0x40000000
	UNPROTECTED_DACL_SECURITY_INFORMATION = 0x20000000
	UNPROTECTED_SACL_SECURITY_INFORMATION = 0x10000000
)

var (
	procGetNamedSecurityInfoW = advapi32.MustFindProc("GetNamedSecurityInfoW")
	procSetNamedSecurityInfoW = advapi32.MustFindProc("SetNamedSecurityInfoW")
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/ee207397.aspx
const (
	SECURITY_MAX_SID_SIZE = 68
)

// https://msdn.microsoft.com/en-us/library/windows/desktop/aa379650.aspx
const (
	WinNullSid                                  = 0
	WinWorldSid                                 = 1
	WinLocalSid                                 = 2
	WinCreatorOwnerSid                          = 3
	WinCreatorGroupSid                          = 4
	WinCreatorOwnerServerSid                    = 5
	WinCreatorGroupServerSid                    = 6
	WinNtAuthoritySid                           = 7
	WinDialupSid                                = 8
	WinNetworkSid                               = 9
	WinBatchSid                                 = 10
	WinInteractiveSid                           = 11
	WinServiceSid                               = 12
	WinAnonymousSid                             = 13
	WinProxySid                                 = 14
	WinEnterpriseControllersSid                 = 15
	WinSelfSid                                  = 16
	WinAuthenticatedUserSid                     = 17
	WinRestrictedCodeSid                        = 18
	WinTerminalServerSid                        = 19
	WinRemoteLogonIdSid                         = 20
	WinLogonIdsSid                              = 21
	WinLocalSystemSid                           = 22
	WinLocalServiceSid                          = 23
	WinNetworkServiceSid                        = 24
	WinBuiltinDomainSid                         = 25
	WinBuiltinAdministratorsSid                 = 26
	WinBuiltinUsersSid                          = 27
	WinBuiltinGuestsSid                         = 28
	WinBuiltinPowerUsersSid                     = 29
	WinBuiltinAccountOperatorsSid               = 30
	WinBuiltinSystemOperatorsSid                = 31
	WinBuiltinPrintOperatorsSid                 = 32
	WinBuiltinBackupOperatorsSid                = 33
	WinBuiltinReplicatorSid                     = 34
	WinBuiltinPreWindows2000CompatibleAccessSid = 35
	WinBuiltinRemoteDesktopUsersSid             = 36
	WinBuiltinNetworkConfigurationOperatorsSid  = 37
	WinAccountAdministratorSid                  = 38
	WinAccountGuestSid                          = 39
	WinAccountKrbtgtSid                         = 40
	WinAccountDomainAdminsSid                   = 41
	WinAccountDomainUsersSid                    = 42
	WinAccountDomainGuestsSid                   = 43
	WinAccountComputersSid                      = 44
	WinAccountControllersSid                    = 45
	WinAccountCertAdminsSid                     = 46
	WinAccountSchemaAdminsSid                   = 47
	WinAccountEnterpriseAdminsSid               = 48
	WinAccountPolicyAdminsSid                   = 49
	WinAccountRasAndIasServersSid               = 50
	WinNTLMAuthenticationSid                    = 51
	WinDigestAuthenticationSid                  = 52
	WinSChannelAuthenticationSid                = 53
	WinThisOrganizationSid                      = 54
	WinOtherOrganizationSid                     = 55
	WinBuiltinIncomingForestTrustBuildersSid    = 56
	WinBuiltinPerfMonitoringUsersSid            = 57
	WinBuiltinPerfLoggingUsersSid               = 58
	WinBuiltinAuthorizationAccessSid            = 59
	WinBuiltinTerminalServerLicenseServersSid   = 60
	WinBuiltinDCOMUsersSid                      = 61
	WinBuiltinIUsersSid                         = 62
	WinIUserSid                                 = 63
	WinBuiltinCryptoOperatorsSid                = 64
	WinUntrustedLabelSid                        = 65
	WinLowLabelSid                              = 66
	WinMediumLabelSid                           = 67
	WinHighLabelSid                             = 68
	WinSystemLabelSid                           = 69
	WinWriteRestrictedCodeSid                   = 70
	WinCreatorOwnerRightsSid                    = 71
	WinCacheablePrincipalsGroupSid              = 72
	WinNonCacheablePrincipalsGroupSid           = 73
	WinEnterpriseReadonlyControllersSid         = 74
	WinAccountReadonlyControllersSid            = 75
	WinBuiltinEventLogReadersGroup              = 76
	WinNewEnterpriseReadonlyControllersSid      = 77
	WinBuiltinCertSvcDComAccessGroup            = 78
	WinMediumPlusLabelSid                       = 79
	WinLocalLogonSid                            = 80
	WinConsoleLogonSid                          = 81
	WinThisOrganizationCertificateSid           = 82
	WinApplicationPackageAuthoritySid           = 83
	WinBuiltinAnyPackageSid                     = 84
	WinCapabilityInternetClientSid              = 85
	WinCapabilityInternetClientServerSid        = 86
	WinCapabilityPrivateNetworkClientServerSid  = 87
	WinCapabilityPicturesLibrarySid             = 88
	WinCapabilityVideosLibrarySid               = 89
	WinCapabilityMusicLibrarySid                = 90
	WinCapabilityDocumentsLibrarySid            = 91
	WinCapabilitySharedUserCertificatesSid      = 92
	WinCapabilityEnterpriseAuthenticationSid    = 93
	WinCapabilityRemovableStorageSid            = 94
)

var procCreateWellKnownSid = advapi32.MustFindProc("CreateWellKnownSid")

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

var procSetEntriesInAclW = advapi32.MustFindProc("SetEntriesInAclW")
