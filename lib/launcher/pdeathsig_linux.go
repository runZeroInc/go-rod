//go:build linux

package launcher

import "syscall"

// setPdeathsig arranges for the kernel to deliver SIGKILL to the browser
// process if the launching process exits unexpectedly. This prevents
// orphaned Chrome process trees when the parent is killed (SIGKILL, OOM,
// panic, terminal close, etc.) without running deferred cleanup handlers.
func setPdeathsig(v *syscall.SysProcAttr) {
	if v == nil {
		return
	}
	v.Pdeathsig = syscall.SIGKILL
}
