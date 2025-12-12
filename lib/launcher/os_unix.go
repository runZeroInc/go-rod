//go:build !windows

package launcher

import (
	"os/exec"
	"syscall"
)

func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func (l *Launcher) osSetupCmd(cmd *exec.Cmd) {
	if l.browser.GetXVFB() {
		*cmd = *exec.Command("xvfb-run", cmd.Args...) //nolint:gosec
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
