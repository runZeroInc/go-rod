//go:build !windows

package launcher

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func (l *Launcher) osSetupCmd(ctx context.Context, cmd *exec.Cmd, uid, gid int) {
	if l.browser.GetXVFB() {
		*cmd = *exec.CommandContext(ctx, "xvfb-run", cmd.Args...) //nolint:gosec
	}
	cmd.SysProcAttr = getSysProcAttr(uid, gid)
}

// getSysProcAttr returns the SysProcAttr for non-Windows platforms.
func getSysProcAttr(uid, gid int) *syscall.SysProcAttr {
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
	return v
}

func killLeftoverProcesses(pid int, bpath string) {
	syscall.Kill(-pid, syscall.SIGKILL)
	syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
}
