//go:build windows

package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mitchellh/go-ps"
	"github.com/runZeroInc/go-rod/pkg/winacl"
	"github.com/runZeroInc/go-rod/pkg/winacl/api"
	"golang.org/x/sys/windows"
)

var advapi32 = windows.MustLoadDLL("advapi32.dll")

type osAttributes struct {
	Username string
	Token    windows.Token
	Error    error
	SID      *windows.SID
	Sudo     bool
}

var osPreferredUsernames = []string{
	"NT AUTHORITY\\NetworkService",
	"NT AUTHORITY\\LocalService",
}

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
		l.logger.Debugf("not running as system, skipping sudo (%s\\%s)", dom, user)
		return
	}
	// TODO: Skip sudo if we are not running as a service, since the token will not work properly.

	for _, v := range osPreferredUsernames {
		dom, user, found := strings.Cut(v, "\\")
		if !found {
			continue
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

func (l *Launcher) ensureUserPermissions(uid, gid int, userDir, binPath string) error {
	if l.osAttributes.Username == "" {
		return nil
	}
	var res error
	if err := l.osEnsureUserPermissionsUserDir(uid, gid, userDir); err != nil {
		res = errors.Join(res, fmt.Errorf("user dir permissions: %w", err))
	}
	if err := l.osEnsureUserPermissionsBinary(uid, gid, binPath); err != nil {
		res = errors.Join(res, fmt.Errorf("binary permissions: %w", err))
	}
	return res
}

func (l *Launcher) osEnsureUserPermissionsBinary(uid, gid int, binPath string) error {
	if binPath == "" {
		return fmt.Errorf("no binary path")
	}
	// Set read/exec permissions on the binary path
	_ = winacl.Chmod(binPath, 0o755)
	return nil
}

func (l *Launcher) osEnsureUserPermissionsUserDir(uid, gid int, userDir string) error {
	if userDir == "" {
		return fmt.Errorf("no user-data-dir")
	}

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

	var errSet error
	if err := ChownByUsername(l.Browser.TempDir, l.osAttributes.Username); err != nil {
		l.logger.Errorf("chown temp dir %s for user %s failed: %v", l.Browser.TempDir, l.osAttributes.Username, err)
		errSet = err
	}

	if err := ChownByUsername(userDir, l.osAttributes.Username); err != nil {
		l.logger.Errorf("chown user dir %s for user %s failed: %v", userDir, l.osAttributes.Username, err)
		errSet = errors.Join(errSet, err)
	}

	// Preemptively create the crashpad directory structure required by Edge
	// Edge ignores the --userdata-dir but follows %LOCALAPPDATA%
	crashPadParts := []string{"Microsoft", "Edge", "User Data", "Crashpad"}
	bpath := userDir
	for _, part := range crashPadParts {
		bpath = filepath.Join(bpath, part)
		if err := os.MkdirAll(bpath, 0o755); err != nil { //nolint:gosec
			l.logger.Errorf("mkdir %s for user %s failed: %v", bpath, l.osAttributes.Username, err)
		}
		if err := ChownByUsername(bpath, l.osAttributes.Username); err != nil {
			l.logger.Errorf("chown %s for user %s failed: %v", bpath, l.osAttributes.Username, err)
			errSet = errors.Join(errSet, err)
		}
	}
	return errSet
}

// Do not inherit the parent error handling mode for the subprocess
const CREATE_DEFAULT_ERROR_MODE = 0x04000000

func killGroup(pid int) {
	terminateProcess(pid)
}

func (l *Launcher) osSetupCmd(ctx context.Context, cmd *exec.Cmd, uid, gid int) error {
	var err error
	cmd.SysProcAttr = l.getSysProcAttr(uid, gid)
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

func ChownByUsername(path string, username string) error {
	_ = winacl.Chmod(path, 0o755)
	return winacl.Apply(path, false, true,
		winacl.GrantName(windows.GENERIC_ALL, username),
		winacl.GrantName(windows.MAXIMUM_ALLOWED, username),
	)
}

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

func (l *Launcher) getSysProcAttr(uid, gid int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | CREATE_DEFAULT_ERROR_MODE,
	}
}

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
