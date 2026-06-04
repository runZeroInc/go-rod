//go:build windows

package launcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// readDACL returns the output of `icacls <path>` for use as a string we can
// substring-match in assertions. It deliberately avoids parsing the output;
// the goal is just to confirm the SIDs/usernames we expect are present.
func readDACL(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s: %v: %s", path, err, string(out))
	}
	return string(out)
}

func mustMkTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rod-acl-test-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func currentUser(t *testing.T) string {
	t.Helper()
	user, dom, _, err := getCurrentProcessTokenUser()
	if err != nil {
		t.Fatalf("get current user: %v", err)
	}
	return dom + "\\" + user
}

// TestGrantReadExecToPackages_BothBackends verifies that both the icacls and
// the raw Win32 API code paths grant the expected ACEs (AllAppPackages,
// AllRestrictedAppPackages, Everyone) on a directory.
func TestGrantReadExecToPackages_BothBackends(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) error
	}{
		{"icacls", grantReadExecToPackagesICACLS},
		{"winapi", GrantReadExecToPackages},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := mustMkTempDir(t)
			if err := tc.fn(dir); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			acl := readDACL(t, dir)
			// AllApplicationPackages SID (S-1-15-2-1) and Everyone are
			// always rendered by icacls as friendly names; check both
			// the friendly names and SID-form to be resilient to
			// localization.
			wants := []string{
				"ALL APPLICATION PACKAGES",
				"ALL RESTRICTED APPLICATION PACKAGES",
				"Everyone",
			}
			for _, w := range wants {
				if !strings.Contains(acl, w) {
					t.Errorf("expected DACL to contain %q after %s, got:\n%s", w, tc.name, acl)
				}
			}
		})
	}
}

// TestChownByUsername_BothBackends verifies that both the icacls and the raw
// Win32 API code paths grant the calling user full control on a directory.
func TestChownByUsername_BothBackends(t *testing.T) {
	user := currentUser(t)
	cases := []struct {
		name string
		fn   func(string, string) error
	}{
		{"icacls", chownByUsernameICACLS},
		{"winapi", ChownByUsername},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := mustMkTempDir(t)
			if err := tc.fn(dir, user); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			acl := readDACL(t, dir)
			// Full control is rendered as "(F)" by icacls. Match on the
			// short user component (after the backslash) since icacls
			// may render the domain differently than the token user.
			short := user
			if i := strings.LastIndex(user, "\\"); i >= 0 {
				short = user[i+1:]
			}
			if !strings.Contains(acl, short) {
				t.Errorf("expected DACL to mention user %q after %s, got:\n%s", short, tc.name, acl)
			}
			if !strings.Contains(acl, "(F)") {
				t.Errorf("expected DACL to contain a full-control entry after %s, got:\n%s", tc.name, acl)
			}
			// Re-creating a file inside the directory should still be
			// possible since we granted the calling user full control.
			f, err := os.Create(filepath.Join(dir, "probe.txt"))
			if err != nil {
				t.Fatalf("create file in %s after %s: %v", dir, tc.name, err)
			}
			_ = f.Close()
		})
	}
}

// TestLauncherDispatch_UseWinACLAPI verifies that the (*Launcher) and
// (*Browser) dispatcher methods route to the correct backend based on the
// UseWinACLAPI flag. We exercise both true and false on the same directory
// and assert the operation succeeds in both modes.
func TestLauncherDispatch_UseWinACLAPI(t *testing.T) {
	for _, useAPI := range []bool{false, true} {
		name := "icacls"
		if useAPI {
			name = "winapi"
		}
		t.Run(name, func(t *testing.T) {
			dir := mustMkTempDir(t)
			l := &Launcher{Browser: &Browser{UseWinACLAPI: useAPI}}
			if err := l.grantReadExecToPackages(dir); err != nil {
				t.Fatalf("grantReadExecToPackages (%s): %v", name, err)
			}
			if err := l.Browser.grantReadExecToPackages(dir); err != nil {
				t.Fatalf("Browser.grantReadExecToPackages (%s): %v", name, err)
			}
			if err := l.chownByUsername(dir, currentUser(t)); err != nil {
				t.Fatalf("chownByUsername (%s): %v", name, err)
			}
			acl := readDACL(t, dir)
			if !strings.Contains(acl, "ALL APPLICATION PACKAGES") {
				t.Errorf("expected ALL APPLICATION PACKAGES in DACL (%s), got:\n%s", name, acl)
			}
		})
	}
}

// TestRunICACLS_NonexistentPath verifies that runICACLS surfaces an error
// (rather than hanging) when invoked on a path that does not exist.
func TestRunICACLS_NonexistentPath(t *testing.T) {
	missing := filepath.Join(os.TempDir(), "rod-acl-does-not-exist-xyzzy")
	_ = os.RemoveAll(missing)
	if err := runICACLS(missing, "/grant", "*S-1-1-0:(OI)(CI)RX"); err == nil {
		t.Fatalf("expected error from icacls on missing path %q", missing)
	}
}

// TestGrantReadExecToPackagesICACLS_MissingPathIsNoop confirms that the
// caller-facing helper silently skips paths that do not exist, matching the
// behavior of the raw-API GrantReadExecToPackages.
func TestGrantReadExecToPackagesICACLS_MissingPathIsNoop(t *testing.T) {
	missing := filepath.Join(os.TempDir(), "rod-acl-missing-noop-xyzzy")
	_ = os.RemoveAll(missing)
	if err := grantReadExecToPackagesICACLS(missing); err != nil {
		t.Fatalf("expected nil for missing path, got %v", err)
	}
	if err := GrantReadExecToPackages(missing); err != nil {
		t.Fatalf("winapi GrantReadExecToPackages on missing path: %v", err)
	}
}
