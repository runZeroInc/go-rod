//go:build !windows

package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/runZeroInc/go-rod/lib/launcher/flags"
)

type osAttributes struct {
	UID int
	GID int
}

func (l *Launcher) osResolveAttributes() {
	l.osAttributes = &osAttributes{}
	l.osAttributes.UID = l.Browser.UID
	l.osAttributes.GID = l.Browser.GID
}

func (l *Launcher) osSetupCmd(ctx context.Context, cmd *exec.Cmd) error {
	var err error

	// Automatically add --no-sandbox if unprivileged user namespaces are disabled on Linux
	if _, nsb := l.GetFlags(flags.NoSandbox); runtime.GOOS == "linux" && !nsb {
		unsDisabled, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
		if err == nil && bytes.HasPrefix(unsDisabled, []byte("1")) {
			l.logger.Debugf("automatically adding the --no-sandbox flag since unprivileged user namespaces are disabled")
			cmd.Args = append(cmd.Args, "--no-sandbox")
		}
	}

	if l.Browser.GetXVFB() {
		*cmd = *exec.CommandContext(ctx, "xvfb-run", cmd.Args...) //nolint:gosec
	}
	cmd.SysProcAttr, err = l.getSysProcAttr(l.Browser.UID, l.Browser.GID)
	return err
}

// getSysProcAttr returns the SysProcAttr for non-Windows platforms.
func (l *Launcher) getSysProcAttr(uid, gid int) (*syscall.SysProcAttr, error) {
	v := &syscall.SysProcAttr{
		Setpgid: true,
	}
	if os.Geteuid() == 0 && uid != 0 {
		cred := &syscall.Credential{
			Uid:         uint32(uid),
			Gid:         uint32(gid),
			NoSetGroups: true,
		}
		v.Credential = cred
	}
	return v, nil
}

const rlimitNoFiles = 1048576

func (l *Launcher) ensureUserPermissions(userDir, binPath string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if l.Browser.UID == 0 {
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

// osEnsureUserPermissionsBinary ensures that the binary and its leading paths
// have execute permissions for user, group, and other.
func (l *Launcher) osEnsureUserPermissionsBinary(binPath string) error {
	if binPath == "" {
		return fmt.Errorf("no binary path")
	}
	st, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("bin path %s: %w", binPath, err)
	}
	if st.IsDir() {
		return fmt.Errorf("bin path %s is a directory", binPath)
	}

	err = filepath.Walk(binPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		_ = os.Chmod(path, 0o755) //nolint:gosec
		return nil
	})
	if err != nil {
		return fmt.Errorf("bin path %s: %w", binPath, err)
	}

	// Try to ensure all leading paths are read/execute
	if err := l.osEnsureLeadingPathReadExec(binPath); err != nil {
		return fmt.Errorf("leading path read/exec %s: %w", binPath, err)
	}

	return nil
}

// osEnsureLeadingPathReadExec ensures that all components of the
// path allow read and execute permissions for user, group, and other.
func (l *Launcher) osEnsureLeadingPathReadExec(src string) error {
	// Ensure that all leading paths have execute enabled for user, group, and other
	base, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolve base path %s: %w", src, err)
	}
	pname := "/"
	for part := range strings.SplitSeq(base, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		pname = filepath.Join(pname, part)
		st, err := os.Stat(pname)
		if err != nil {
			return fmt.Errorf("stat path %s: %w", pname, err)
		}
		// We've reached the file at the end of the path
		if !st.IsDir() {
			return nil
		}
		cperm := st.Mode().Perm()
		if err := os.Chmod(pname, cperm|0o111); err != nil { //nolint:gosec
			l.logger.Errorf("failed to chmod path %s as %o: %s", pname, cperm|0o111, err)
		}
	}
	return nil
}

func (l *Launcher) osEnsureUserPermissionsUserDir(userDir string) error {
	// Validate user dir
	if userDir == "" {
		return fmt.Errorf("no user-data-dir")
	}
	st, err := os.Stat(userDir)
	if err != nil {
		return fmt.Errorf("userdir %s: %w", userDir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("userdir %s is not a directory", userDir)
	}

	err = filepath.Walk(userDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == userDir {
			// Ensure that the user directory is owned by the user
			if err := os.Chown(path, l.Browser.UID, l.Browser.GID); err != nil {
				return fmt.Errorf("chown path %s: %w", path, err)
			}
			return nil
		}
		// Try to ensure all leading paths are 755
		_ = os.Chmod(path, 0o755) //nolint:gosec
		return nil
	})
	if err != nil {
		return fmt.Errorf("userdir path %s: %w", userDir, err)
	}

	// Try to ensure all leading paths are read/execute
	if err := l.osEnsureLeadingPathReadExec(userDir); err != nil {
		return fmt.Errorf("leading path read/exec %s: %w", userDir, err)
	}

	return nil
}

// osEnsureApplicationPermissions enables read/execute permissions for everyone.
func osEnsureApplicationPermissions(dir string) error {
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		_ = os.Chmod(path, 0o755) //nolint:gosec
		return nil
	})
	return nil
}

func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
