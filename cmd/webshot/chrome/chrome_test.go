package chrome

// Enable full browsers tests by setting WEBSHOT_BROWSER_TESTS=true

import (
	"os"
	"os/user"
	"strconv"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestLimitedDiscardingBuffer(t *testing.T) {
	buf := NewLimitedBuffer(10)
	expectedStored := "HelloWorld"
	input := []byte(expectedStored + "!!!")
	n, err := buf.Write(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Fatalf("expected to write %d bytes, wrote %d", len(input), n)
	}
	if buf.String() != expectedStored {
		t.Fatalf("expected buffer to contain %q, got %q", expectedStored, buf.String())
	}
	moreInput := []byte("MoreDataXYZABC")
	n, err = buf.Write(moreInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(moreInput) {
		t.Fatalf("expected to write %d bytes, wrote %d", len(moreInput), n)
	}
	if string(buf.buf) != expectedStored {
		t.Fatalf("expected buffer to still contain %q, got %q", expectedStored, string(buf.buf))
	}
}

func getBrowserTestsEnabled(t *testing.T) bool {
	enabled, _ := strconv.ParseBool(os.Getenv("WEBSHOT_BROWSER_TESTS"))
	if !enabled {
		t.Skip("Skipping browser tests, set WEBSHOT_BROWSER_TESTS=true to enable")
	}
	return true
}

func getTestLogger() *logrus.Logger {
	lgr := logrus.New()
	lgr.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		DisableColors:    true,
	})
	lgr.SetLevel(logrus.TraceLevel)
	return lgr
}

func getScreenshotAndVerify(t *testing.T, w *Webshot, url string) {
	uname, _ := user.Current()
	t.Logf("Using browser executable %s (%s) (parent is %s uid=%d/%d, child will be uid=%d/gid=%d)",
		w.LaunchedChromiumPath, w.LaunchedVersion, uname.Username, os.Geteuid(), os.Getuid(), w.GetUID(), w.GetGID())

	r, err := w.Screenshot(url)
	if err != nil {
		t.Fatalf("screenshot of %s failed: %s", url, err)
	}
	if len(r.Image) == 0 {
		t.Fatalf("screenshot of %s returned empty image: %v", url, r.Error)
	}
	if r.DOM["globals"] == "" {
		t.Fatalf("screenshot of %s returned empty globals in DOM: %v", url, r.DOM)
	}
	t.Logf("screenshot of %s succeeded, image size %d bytes, globals: %v", url, len(r.Image), r.DOM["globals"])
}

// TestChromeBrowserScreenshotWithSystem tests taking a screenshot using the system-installed Chrome/Chromium browser.
// This will fail if no suitable browser is installed.
func TestChromeBrowserScreenshotWithSystem(t *testing.T) {
	if !getBrowserTestsEnabled(t) {
		return
	}
	lgr := getTestLogger()
	opts := GetDefaultOptions(lgr.WithField("source", "webshot-test"))
	w := NewWebshot(opts...)
	w.UseSystemChromium = true
	w.UseAutomaticInstall = false

	defer w.Cleanup()

	if err := w.Init(); err != nil {
		t.Fatalf("failed to init webshot with system chrome: %s", err)
	}
	getScreenshotAndVerify(t, w, "https://go.dev/")
}

// TestChromeBrowserScreenshotWithAutomaticInstall tests taking a screenshot using an automatically installed Chrome/Chromium browser.
// This downloads binaries from Chrome-for-Testing or Puppeteer if no cached version is available.
func TestChromeBrowserScreenshotWithAutomaticInstall(t *testing.T) {
	if !getBrowserTestsEnabled(t) {
		return
	}
	lgr := getTestLogger()

	opts := GetDefaultOptions(lgr.WithField("source", "webshot-test"))
	w := NewWebshot(opts...)
	w.UseSystemChromium = false

	// Use any existing cache or download fresh binaries as needed.
	w.UseAutomaticInstall = true

	defer w.Cleanup()

	if err := w.Init(); err != nil {
		t.Fatalf("failed to init webshot with system chrome: %s", err)
	}

	getScreenshotAndVerify(t, w, "https://go.dev/")
}
