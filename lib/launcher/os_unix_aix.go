//go:build aix

package launcher

import (
	"syscall"
)

// killLeftoverProcesses terminates any child processes spawned by the launcher
// using a negative PID to target the entire process group.
func killLeftoverProcesses(pid int, _ string) {
	syscall.Kill(-pid, syscall.SIGKILL)
	// AIX does not seem to support WNOHANG
	// syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
}
