//go:build !windows

package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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

func (l *Launcher) osEnsureUserPermissionsBinary(binPath string) error {
	// Validate bin path
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
