//go:build windows

package winacl

import (
	"os"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// TestApplyStressGC exercises Apply repeatedly under aggressive GC pressure
// against the kind of well-known LPAC/Everyone/CreatorOwner SIDs that the
// chrome launcher uses via GrantReadExecToPackages. It is designed to catch
// regressions in the SID pinning fix in SetEntriesInAcl: if the SID pointers
// reached via Trustee.Name = (*uint16)(unsafe.Pointer(sid)) are not pinned
// for the duration of the kernel call, the syscall will eventually trip
// the cgo pointer checker (-gcflags="all=-d=checkptr=2") or, with
// GODEBUG=cgocheck=2, panic with "fatal error: checkptr: pointer
// arithmetic result points to invalid allocation".
//
// Run with extra checks to maximise the chance of catching a regression:
//
//	go test -race -tags windows -gcflags=all=-d=checkptr=2 \
//	  -run TestApplyStressGC ./pkg/winacl/...
//
// Also try GOGC=1 and GODEBUG=cgocheck=2,gctrace=1 in the environment.
func TestApplyStressGC(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test; skipping in -short mode")
	}

	f, err := os.CreateTemp("", "winacl-stress-*")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer os.Remove(f.Name())

	// Set GC to be as aggressive as we can without making the test
	// unbearably slow.
	oldGC := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(oldGC)

	const goroutines = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Allocate fresh SIDs on each iteration so we exercise
				// short-lived heap allocations that are prime candidates
				// for being moved/reclaimed if pinning is broken.
				everyone, err := windows.StringToSid("S-1-1-0")
				if err != nil {
					errCh <- err
					return
				}
				creatorOwner, err := windows.StringToSid("S-1-3-0")
				if err != nil {
					errCh <- err
					return
				}
				allApp, err := windows.StringToSid("S-1-15-2-1")
				if err != nil {
					errCh <- err
					return
				}
				restrictedApp, err := windows.StringToSid("S-1-15-2-2")
				if err != nil {
					errCh <- err
					return
				}

				if err := Apply(f.Name(), false, false,
					GrantSid(windows.GENERIC_READ|windows.GENERIC_EXECUTE|windows.READ_CONTROL, allApp),
					GrantSid(windows.GENERIC_READ|windows.GENERIC_EXECUTE|windows.READ_CONTROL, restrictedApp),
					GrantSid(windows.GENERIC_READ|windows.GENERIC_EXECUTE|windows.READ_CONTROL, everyone),
					GrantSid(windows.MAXIMUM_ALLOWED, creatorOwner),
				); err != nil {
					errCh <- err
					return
				}

				// Force the GC to run between iterations so any unpinned
				// allocation is a candidate for reclamation/relocation.
				runtime.GC()
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("Apply under GC pressure: %v", err)
		}
	}
}
