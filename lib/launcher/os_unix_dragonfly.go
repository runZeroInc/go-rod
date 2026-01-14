//go:build dragonfly
// +build dragonfly

package launcher

import (
	"syscall"
)

// setupLimits adjust resource limits for the launcher process on Unix-like systems.
// Specifically, it increases the file descriptor limit and disables core dumps.
func (l *Launcher) setupLimits() error {
	// Increase the file descriptor to prevent "too many open files" errors in
	// when using many concurrent browser tabs.
	noFile := &syscall.Rlimit{}
	_ = syscall.Getrlimit(syscall.RLIMIT_NOFILE, noFile)
	if noFile.Cur < int64(rlimitNoFiles) {
		_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: rlimitNoFiles, Max: rlimitNoFiles})
	}

	// Disable core dumps to prevent sandbox self-termination from filling up disk space.
	noCore := &syscall.Rlimit{}
	_ = syscall.Getrlimit(syscall.RLIMIT_CORE, noCore)
	if noCore.Cur != 0 {
		_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0})
	}
	return nil
}
