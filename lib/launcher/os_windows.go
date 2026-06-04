//go:build windows

package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mitchellh/go-ps"
	"github.com/runZeroInc/go-rod/pkg/winacl"
	"github.com/runZeroInc/go-rod/pkg/winacl/api"
	"golang.org/x/sys/windows"
)

type osAttributes struct {
	Username string
	Token    windows.Token
	Error    error
	SID      *windows.SID
	Sudo     bool
}

// osPreferredUsernames is the list of preferred non-SYSTEM accounts to attempt to sudo to
// when running as SYSTEM under a service.
var osPreferredUsernames = []string{
	"NT AUTHORITY\\NetworkService",
	"NT AUTHORITY\\LocalService",
}

// Grant Read/Execute to S-1-15-2-2 (All Restricted Application Packages) to support LPAC
// - https://source.chromium.org/chromium/chromium/src/+/main:testing/scripts/common.py;l=62
// - https://chromium.googlesource.com/chromium/src/+/refs/heads/main/docs/design/sandbox.md#lpac-file-system-permissions
// - https://learn.microsoft.com/en-us/windows/win32/secauthz/well-known-sids
var (
	RestrictedAppPackagesSID, _   = windows.StringToSid("S-1-15-2-2")
	RestrictedAppPackagesUsername = "APPLICATION PACKAGE AUTHORITY\\ALL RESTRICTED APPLICATION PACKAGES"
	AllAppPackagesSID, _          = windows.StringToSid("S-1-15-2-1")
	AllAppPackagesUsername        = "APPLICATION PACKAGE AUTHORITY\\ALL APPLICATION PACKAGES"
	EveryoneSID, _                = windows.StringToSid("S-1-1-0")
	CreatorOwnerSID, _            = windows.StringToSid("S-1-3-0")
	CreatorGroupSID, _            = windows.StringToSid("S-1-3-1")
)

func (l *Launcher) osResolveAttributes() {
	l.osAttributes = &osAttributes{}
	user, dom, sid, err := getCurrentProcessTokenUser()
	if err != nil {
		l.logger.Debugf("failed to resolve current user: %v", err)
	} else {
		l.osAttributes.Username = dom + "\\" + user
		l.osAttributes.SID = sid
	}

	// Return early if the current user is not SYSTEM
	if strings.EqualFold(l.osAttributes.Username, "NT AUTHORITY\\SYSTEM") == false {
		l.logger.Debugf("running as %s and skipping sudo", l.osAttributes.Username)
		return
	}

	for _, v := range osPreferredUsernames {
		dom, user, found := strings.Cut(v, "\\")
		if !found {
			user = v
			dom = "."
		}
		// Note that Microsoft Edge will still timeout if this code is run as system and gets a valid token
		// when it is NOT running as a service. It will still obtain the token, but Edge will not start
		// properly when launched this way.
		token, err := api.LogonUser(user, dom, "", api.LOGON32_LOGON_SERVICE, api.LOGON32_PROVIDER_DEFAULT)
		if err == nil {
			l.osAttributes.Username = v
			l.osAttributes.Token = token
			l.osAttributes.Error = nil
			l.osAttributes.SID = sid
			l.osAttributes.Sudo = true
			l.logger.Debugf("created sudo token for user '%s' (%s)", v, sid.String())
			break
		}
		l.osAttributes.Error = fmt.Errorf("user %s: %w", v, err)
		l.logger.Debugf("failed to create token for user %s: %v", v, err)
	}
	if l.osAttributes.Token == 0 {
		l.logger.Debugf("sudo not available: %s", l.osAttributes.Error)
	}
}

func (l *Launcher) ensureUserPermissions(userDir, binPath string) error {
	if l.osAttributes.Username == "" {
		return nil
	}
	var res error
	if err := l.osEnsureUserPermissionsUserDir(userDir); err != nil {
		res = errors.Join(res, fmt.Errorf("user dir permissions: %w", err))
	}
	if err := l.osEnsureUserPermissionsBinary(binPath); err != nil {
		res = errors.Join(res, fmt.Errorf("binary permissions: %w", err))
	}
	return res
}

func (l *Launcher) osEnsureUserPermissionsBinary(binPath string) error {
	if binPath == "" {
		return fmt.Errorf("no binary path")
	}
	err := l.grantReadExecToPackages(binPath)
	if err != nil {
		l.logger.Errorf("grant read/exec to packages on binary %s: %v", binPath, err)
	}
	return nil
}

func (l *Launcher) osEnsureUserPermissionsUserDir(userDir string) error {
	if userDir == "" {
		return fmt.Errorf("no user-data-dir")
	}

	// Grant read/execute to LPAC
	if err := l.grantReadExecToPackages(l.Browser.TempDir); err != nil {
		l.logger.Errorf("grant read/exec to packages on temp dir %s failed: %v", l.Browser.TempDir, err)
	}
	if err := l.grantReadExecToPackages(l.Browser.CacheDir); err != nil {
		l.logger.Errorf("grant read/exec to packages on cache dir %s failed: %v", l.Browser.CacheDir, err)
	}
	if err := l.grantReadExecToPackages(userDir); err != nil {
		l.logger.Errorf("grant read/exec to packages on user dir %s failed: %v", userDir, err)
	}

	// If we're sudoing to another user, change the ownership of the user dir
	if l.osAttributes.Username == "" {
		return nil
	}
	st, err := os.Stat(userDir)
	if err != nil {
		return fmt.Errorf("userdir %s: %w", userDir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("userdir %s is not a directory", userDir)
	}

	// Change the ownership to the target user account
	var errSet error
	if err := l.chownByUsername(l.Browser.TempDir, l.osAttributes.Username); err != nil {
		l.logger.Errorf("chown temp dir %q for user %s failed: %v", l.Browser.TempDir, l.osAttributes.Username, err)
		errSet = err
	}
	if err := l.chownByUsername(userDir, l.osAttributes.Username); err != nil {
		l.logger.Errorf("chown user dir %q for user %s failed: %v", userDir, l.osAttributes.Username, err)
		errSet = errors.Join(errSet, err)
	}

	// Preemptively create the crashpad directory structure required by Edge
	// Edge ignores the --userdata-dir but follows %LOCALAPPDATA%
	crashPadParts := []string{"Microsoft", "Edge", "User Data", "Crashpad"}
	bpath := userDir
	for _, part := range crashPadParts {
		bpath = filepath.Join(bpath, part)
		if err := os.MkdirAll(bpath, 0o755); err != nil { //nolint:gosec
			l.logger.Errorf("mkdir %q for user %s failed: %v", bpath, l.osAttributes.Username, err)
		}
		if err := l.chownByUsername(bpath, l.osAttributes.Username); err != nil {
			l.logger.Errorf("chown %q for user %s failed: %v", bpath, l.osAttributes.Username, err)
			errSet = errors.Join(errSet, err)
		}
		if err := l.grantReadExecToPackages(bpath); err != nil {
			l.logger.Errorf("grant read/exec to packages on path %s failed: %v", bpath, err)
		}
	}

	return errSet
}

// osEnsureApplicationPermissions enables read/execute permissions for everyone, with an explicit grant
// for All Application Packages and All Restricted Application Packages to support LPAC.
func (b *Browser) osEnsureApplicationPermissions(dir string) error {
	// Ignore errors setting permissions on individual files/directories
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		_ = b.grantReadExecToPackages(path)
		return nil
	})
	return nil
}

// Do not inherit the parent error handling mode for the subprocess
const CREATE_DEFAULT_ERROR_MODE = 0x04000000

func killGroup(pid int) {
	terminateProcess(pid)
}

func (l *Launcher) osSetupCmd(_ context.Context, cmd *exec.Cmd) error {
	var err error
	cmd.SysProcAttr = l.getSysProcAttr()
	if l.Browser.HideWindow {
		cmd.SysProcAttr.HideWindow = true
	}
	if l.osAttributes.Token != 0 {
		cmd.SysProcAttr.Token = syscall.Token(l.osAttributes.Token)
	}

	// Override the USERNAME env variable
	if l.Browser.WithEnv != nil && l.osAttributes.Username != "" {
		l.Browser.WithEnv["USERNAME"] = l.osAttributes.Username
	}

	return err
}

// ChownByUsername grants the named user full control of path using the raw
// Win32 ACL APIs. Prefer (*Launcher).chownByUsername which honors the
// UseWinACLAPI option and defaults to the safer icacls implementation.
func ChownByUsername(path string, username string) error {
	_ = winacl.Chmod(path, 0o755)
	return winacl.Apply(path, false, true,
		winacl.GrantName(windows.GENERIC_ALL, username),
		winacl.GrantName(windows.MAXIMUM_ALLOWED, username),
	)
}

// GrantReadExecToPackages grants read/execute permissions to AllApplicationPackages,
// AllRestrictedApplicationPackages, and Everyone using the raw Win32 ACL APIs.
// Prefer (*Launcher).grantReadExecToPackages or (*Browser).grantReadExecToPackages
// which honor the UseWinACLAPI option and default to the safer icacls implementation.
func GrantReadExecToPackages(path string) error {
	if _, err := os.Stat(path); err != nil {
		// Skip non-existent paths
		return nil
	}
	// Give everyone read/execute (LPAC explicitly) and make sure the owner has maximum allowed
	return winacl.Apply(path, false, false,
		winacl.GrantSid(windows.GENERIC_READ|windows.GENERIC_EXECUTE|windows.READ_CONTROL, AllAppPackagesSID),
		winacl.GrantSid(windows.GENERIC_READ|windows.GENERIC_EXECUTE|windows.READ_CONTROL, RestrictedAppPackagesSID),
		winacl.GrantSid(windows.GENERIC_READ|windows.GENERIC_EXECUTE|windows.READ_CONTROL, EveryoneSID),
		winacl.GrantSid(windows.MAXIMUM_ALLOWED, CreatorOwnerSID),
	)
}

// chownByUsername dispatches to either the icacls-based implementation
// (default) or the raw Win32 API implementation when UseWinACLAPI is set.
func (l *Launcher) chownByUsername(path, username string) error {
	if l.Browser != nil && l.Browser.UseWinACLAPI {
		return ChownByUsername(path, username)
	}
	return chownByUsernameICACLS(path, username)
}

// grantReadExecToPackages dispatches to either the icacls-based implementation
// (default) or the raw Win32 API implementation when UseWinACLAPI is set.
func (l *Launcher) grantReadExecToPackages(path string) error {
	if l.Browser != nil && l.Browser.UseWinACLAPI {
		return GrantReadExecToPackages(path)
	}
	return grantReadExecToPackagesICACLS(path)
}

// grantReadExecToPackages dispatches to either the icacls-based implementation
// (default) or the raw Win32 API implementation when UseWinACLAPI is set.
func (b *Browser) grantReadExecToPackages(path string) error {
	if b.UseWinACLAPI {
		return GrantReadExecToPackages(path)
	}
	return grantReadExecToPackagesICACLS(path)
}

// icaclsTimeout caps how long an icacls invocation is allowed to run before
// it is killed. icacls normally completes in well under a second, but on
// slow filesystems or contested directories it can hang; bound it so a
// stuck invocation cannot block browser startup indefinitely.
const icaclsTimeout = 2 * time.Minute

// runICACLS executes the icacls command for path with the supplied arguments.
// It always appends /C (continue on errors) and /Q (quiet) to keep behavior
// closer to the in-process API which silently skips per-entry failures.
func runICACLS(path string, args ...string) error {
	cmdArgs := append([]string{path}, args...)
	cmdArgs = append(cmdArgs, "/C", "/Q")
	ctx, cancel := context.WithTimeout(context.Background(), icaclsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "icacls", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("icacls %v on %s timed out after %s: %s", args, path, icaclsTimeout, strings.TrimSpace(string(out)))
	}
	if err != nil {
		return fmt.Errorf("icacls %v on %s failed: %w: %s", args, path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// chownByUsernameICACLS mirrors the semantics of ChownByUsername using the
// icacls command line tool instead of the raw Win32 ACL APIs.
//
// It first resets the DACL to a 0o755-equivalent (CreatorOwner full,
// CreatorGroup read/execute, Everyone read/execute) with parent inheritance
// removed, then grants full control to the target user with object/container
// inheritance, and finally re-enables inheritance from the parent.
func chownByUsernameICACLS(path, username string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	// Replace the DACL: remove inherited entries and grant a 0o755-equivalent
	// set of permissions. /grant:r replaces existing grants for these
	// trustees rather than appending.
	if err := runICACLS(path,
		"/inheritance:r",
		"/grant:r",
		"*S-1-3-0:(OI)(CI)F",  // CreatorOwner: full
		"*S-1-3-1:(OI)(CI)RX", // CreatorGroup: read+execute
		"*S-1-1-0:(OI)(CI)RX", // Everyone:     read+execute
	); err != nil {
		return err
	}
	// Grant the named user full control with inheritance.
	if err := runICACLS(path,
		"/grant", fmt.Sprintf("%s:(OI)(CI)F", username),
	); err != nil {
		return err
	}
	// Re-enable inheritance from the parent (matches Apply(..., inherit=true)).
	return runICACLS(path, "/inheritance:e")
}

// grantReadExecToPackagesICACLS mirrors the semantics of
// GrantReadExecToPackages using the icacls command line tool.
func grantReadExecToPackagesICACLS(path string) error {
	if _, err := os.Stat(path); err != nil {
		// Skip non-existent paths
		return nil
	}
	return runICACLS(path,
		"/grant",
		"*S-1-15-2-1:(OI)(CI)RX", // AllApplicationPackages
		"*S-1-15-2-2:(OI)(CI)RX", // AllRestrictedApplicationPackages
		"*S-1-1-0:(OI)(CI)RX",    // Everyone
		"*S-1-3-0:(OI)(CI)F",     // CreatorOwner
	)
}

// getCurrentProcessTokenUser retrieves the username, domain, and SID of the current process token
func getCurrentProcessTokenUser() (string, string, *windows.SID, error) {
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

// getSysProcAttr returns the SysProcAttr for Windows platforms
// Specifically, it sets the CreationFlags to create a new process group
// with default error mode.
func (l *Launcher) getSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | CREATE_DEFAULT_ERROR_MODE,
	}
}

// setupLimits is a no-op on Windows.
func (l *Launcher) setupLimits() error {
	return nil
}

// terminateProcess terminates a process by its PID on Windows.
func terminateProcess(pid int) {
	handle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE, true, uint32(pid))
	if err != nil {
		return
	}
	_ = syscall.TerminateProcess(handle, 0)
	_ = syscall.CloseHandle(handle)
}

// getWindowsCmdPath tries to find the cmd.exe path on Windows
func getWindowsCmdPath() string {
	cmdPath := os.Getenv("COMSPEC")
	if len(cmdPath) != 0 {
		return cmdPath
	}

	winDir := os.Getenv("WINDIR")
	if len(winDir) != 0 {
		return winDir + "\\System32\\cmd.exe"
	}

	rootDir := os.Getenv("SystemRoot")
	if len(rootDir) != 0 {
		return rootDir + "\\System32\\cmd.exe"
	}

	return "C:\\Windows\\System32\\cmd.exe"
}

func killLeftoverProcesses(pid int, bin string) {
	// Terminate the process group under the original pid
	procs, _ := ps.Processes()
	for _, proc := range procs {
		if (proc.Pid() == pid || proc.PPid() == pid) && strings.EqualFold(filepath.Base(proc.Executable()), bin) {
			// Carefully limit the taskkill to this executable name and this child pid
			cpid := strconv.FormatInt(int64(proc.Pid()), 10)
			cmd := exec.Command(getWindowsCmdPath(), "/c", "taskkill /F /T /IM "+bin+" /PID "+cpid) //nolint:gosec
			cmd.Stdin = nil
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			_ = cmd.Run()
		}
	}
	terminateProcess(pid)
}
