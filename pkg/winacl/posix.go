//go:build !windows

package winacl

import "os"

// Chmod is os.Chmod.
var Chmod = os.Chmod
