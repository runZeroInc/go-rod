//go:build !(linux || windows || darwin || freebsd || dragonfly || netbsd || openbsd)

package launcher

// Stub functions for unsupported unix-like OSes.
func (l *Launcher) setupLimits() error {
	return nil
}
