//go:build !linux && !windows

package launcher

import "syscall"

// setPdeathsig is a no-op on non-Linux unixes (macOS, BSDs).
// Parent-death signal is a Linux-specific feature.
func setPdeathsig(_ *syscall.SysProcAttr) {}
