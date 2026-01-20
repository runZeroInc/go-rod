package chrome

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runZeroInc/go-rod"
	"github.com/runZeroInc/go-rod/lib/cdp"
	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/launcher/flags"
	"github.com/runZeroInc/go-rod/lib/proto"
	"github.com/runZeroInc/go-rod/pkg/gson"
	"github.com/sirupsen/logrus"
)

var ErrScreenshotTimeout = errors.New("timeout")

// MaxStdoutCapture is the maximum amount of Chromium stdout/stderr output to return in the event of an error
const MaxStdoutCapture = 1024 * 1024 * 256

// TimeoutNavigationSeconds is the timeout for page navigation
const TimeoutNavigationSeconds = 30

// TimeoutExecSeconds sets the limit for initial process execution
const TimeoutExecSeconds = 30

// MinCapturesDefault sets the default minimum number of screenshot capture attempts
const MinCapturesDefault = 8

// TimeBetweenCaptures sets the delay between screenshot attempts of the same tab
const TimeBetweenCaptures = 250 * time.Millisecond

type Screenshotter interface {
	Screenshot(url string) (*WebshotResult, error)
	GetBrowserPath() string
	GetBrowserVersion() string
	GetUID() int
	GetGID() int
	Cleanup()
}

type WebshotStats struct {
	Execs       atomic.Uint64
	Captures    atomic.Uint64
	Failures    atomic.Uint64
	ErrorsFile  atomic.Uint64
	ErrorsExec  atomic.Uint64
	ErrorsEmpty atomic.Uint64
	ErrorsTrap  atomic.Uint64
	WaitExec    atomic.Uint64
}

// Webshot struct wraps a chromium instance used for screenshots
type Webshot struct {
	ChromiumPath         string
	TempDir              string
	Timeout              time.Duration
	Width                int
	Height               int
	DeviceScaleFactor    float64
	Fullpage             bool
	MinCaptures          int
	MaxCaptures          int
	Proxy                string
	LaunchedChromiumPath string
	LaunchedVersion      string
	LaunchedError        error
	ChromiumArgs         []string
	Setuid               int
	Setgid               int
	Username             string
	Stats                *WebshotStats
	Browser              *rod.Browser
	Logger               *logrus.Entry
	UserAgent            string
	CacheDir             string
	UseSystemChromium    bool
	UseAutomaticInstall  bool
	UID                  int
	GID                  int
	LaunchTimeout        time.Duration

	killMutex     sync.Mutex
	killAttempted bool
	sync.Mutex
}

type WebshotResult struct {
	Image       []byte
	Error       error
	Console     []string
	DOM         map[string]string
	Products    []string
	CaptureMeta []WebshotCaptureMeta
}

type WebshotCaptureMeta struct {
	Bytes        int64
	Milliseconds int64
	Error        error
}

const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36 webshot/3"

// NewWebshot returns a new Webshot instance
func NewWebshot(options ...WebshotOption) *Webshot {
	w := &Webshot{
		Width:               1920,
		Height:              1080,
		UserAgent:           DefaultUserAgent,
		Timeout:             time.Duration(TimeoutExecSeconds) * time.Second,
		Stats:               &WebshotStats{},
		UseSystemChromium:   true,
		UseAutomaticInstall: false,
		Fullpage:            false,
		MinCaptures:         MinCapturesDefault,
		LaunchTimeout:       time.Minute,
		MaxCaptures:         8,
		DeviceScaleFactor:   1,
	}
	for _, opt := range options {
		opt(w)
	}

	if w.Logger == nil {
		w.Logger = logrus.New().WithField("source", "screenshots")
	}

	return w
}

type WebshotOption func(*Webshot)

func WithTimeout(d time.Duration) WebshotOption {
	return func(w *Webshot) {
		w.Timeout = d
	}
}

func WithLaunchTimeout(d time.Duration) WebshotOption {
	return func(w *Webshot) {
		w.LaunchTimeout = d
	}
}

func WithDimensions(width, height int) WebshotOption {
	return func(w *Webshot) {
		w.Width = width
		w.Height = height
	}
}

func WithDeviceScaleFactor(factor float64) WebshotOption {
	return func(w *Webshot) {
		w.DeviceScaleFactor = factor
	}
}

func WithUserAgent(ua string) WebshotOption {
	return func(w *Webshot) {
		w.UserAgent = ua
	}
}

func WithUseChromiumPath(chromiumPath string) WebshotOption {
	return func(w *Webshot) {
		w.ChromiumPath = chromiumPath
	}
}

func WithCacheDir(cacheDir string) WebshotOption {
	return func(w *Webshot) {
		w.CacheDir = cacheDir
	}
}

func WithUseSystemChromium(useSystem bool) WebshotOption {
	return func(w *Webshot) {
		w.UseSystemChromium = useSystem
	}
}

func WithUseAutomaticInstall(useAuto bool) WebshotOption {
	return func(w *Webshot) {
		w.UseAutomaticInstall = useAuto
	}
}

func WithLogger(lgr *logrus.Entry) WebshotOption {
	return func(w *Webshot) {
		w.Logger = lgr
	}
}

func WithFullpage(fullpage bool) WebshotOption {
	return func(w *Webshot) {
		w.Fullpage = fullpage
	}
}

func WithMinCaptures(minCaptures int) WebshotOption {
	return func(w *Webshot) {
		w.MinCaptures = minCaptures
	}
}

func WithMaxCaptures(maxCaptures int) WebshotOption {
	return func(w *Webshot) {
		w.MaxCaptures = maxCaptures
	}
}

func WithUID(uid int) WebshotOption {
	return func(w *Webshot) {
		w.UID = uid
	}
}

func WithGID(gid int) WebshotOption {
	return func(w *Webshot) {
		w.GID = gid
	}
}

func (w *Webshot) Init() error {
	w.Lock()
	defer w.Unlock()

	if w.Browser != nil {
		return nil
	}
	var err error

	// Create a profile for this instance of Chrome, this requires a call to cleanup() if it doesn't error
	w.TempDir, err = os.MkdirTemp(os.TempDir(), "webshot-chrome-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory: %w", err)
	}

	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		// Running as root, try to find a less-privileged user
		uid, gid, username := resolvePreferredUIDGID()
		w.Setuid = uid
		w.Setgid = gid
		w.Username = username

		// Ensure that the base temporary directory is owned by the browser user
		if err := os.Chown(w.TempDir, w.Setuid, w.Setgid); err != nil {
			return fmt.Errorf("failed to set ownership for temporary directory: %w", err)
		}

		// Restrict access to only the browser user
		if err := os.Chmod(w.TempDir, 0o700); err != nil { // nolint:gosec
			return fmt.Errorf("failed to set permissions for temporary directory: %w", err)
		}
	} else {
		// Otherwise stick with the current user
		w.Setuid = os.Getuid()
		w.Setgid = os.Getgid()
	}

	opts := []launcher.BrowserOption{
		launcher.WithTimeout(w.Timeout),
		launcher.WithLaunchTimeout(w.LaunchTimeout),
		launcher.WithUseAutomaticInstall(w.UseAutomaticInstall),
		launcher.WithUseSystemChromium(w.UseSystemChromium),
		launcher.WithCacheDir(w.CacheDir),
		launcher.WithTempDir(w.TempDir),
		launcher.WithWindowSize(w.Width, w.Height),
		launcher.WithLogger(w.Logger),
		launcher.WithUserAgent(w.UserAgent),
		launcher.WithUID(w.Setuid),
		launcher.WithGID(w.Setgid),
		launcher.WithWorkingDir(w.TempDir),
		launcher.WithEnv(w.GetCleanEnv(w.TempDir)),
		launcher.WithHideWindow(true),
		launcher.WithContext(context.Background()),
	}

	if chromiumPath := os.Getenv("WEBSHOT_CHROMIUM_PATH"); chromiumPath != "" {
		opts = append(opts, launcher.WithUseChromiumPath(chromiumPath))
	}

	launchAttempts := 0
	sandboxDisabled := false

TryAgain:

	// Initialize go-rod and configure the logger
	b := rod.New(opts...)
	b.Logger(w.Logger)

	// Identify, validate, launch, and connect to Chromium
	err = b.Connect()

	// Record launched path and version before handling errors
	if l := b.GetLauncher(); l != nil {
		w.LaunchedChromiumPath = l.GetBin()
		chromiumArgs, ferr := l.FormatArgs()
		if ferr == nil {
			w.ChromiumArgs = chromiumArgs
		}
		// Track if the sandbox was already disabled
		if l.Has(flags.NoSandbox) {
			sandboxDisabled = true
		}
	}

	//
	// Chrome tries really hard to self-abort if it can't enable its sandbox safely. This triggers core dumps which in turn
	// fill the disk with garbage and more importantly don't take screenshots. We use Setrlimit() to disable core dumps in
	// go-rod, and try to catch common sandbox failure modes here to retry with --no-sandbox.
	//
	// - When run as root, Chrome will refuse to launch without --no-sandbox. We try to avoid this by using a non-root user
	//   and making sure the temp profile directory is owned by that user.
	//
	// - When run on a system with user namespaces disabled, Chrome will also refuse to launch without --no-sandbox. The
	//   workarounds are to use the system package with its AppArmor profile, use the system package with a setuid helper,
	//   enable user namespaces system-wide, or run with --no-sandbox. The only option available for running CfT binaries
	//   is to try again with --no-sandbox specified.
	//
	// References:
	//  - https://chromium.googlesource.com/chromium/src/+/main/docs/security/apparmor-userns-restrictions.md
	//  - https://chromium.googlesource.com/chromium/src/+/main/docs/linux/suid_sandbox_development.md
	//
	if errors.Is(err, launcher.ErrNoSandbox) && launchAttempts == 0 && !sandboxDisabled {
		w.Logger.Warnf("chromium sandbox error detected, retrying launch with --no-sandbox")
		opts = append(opts, launcher.WithNoSandbox(true))
		launchAttempts++
		goto TryAgain
	}

	if err != nil {
		w.cleanup()
		w.LaunchedError = err
		return fmt.Errorf("failed to start chromium browser: %w", err)
	}

	if ver, err := b.Version(); err == nil {
		w.LaunchedVersion = ver.Product
	}

	w.Browser = b
	return nil
}

func (w *Webshot) GetStats() map[string]uint64 {
	return map[string]uint64{
		"captures": w.Stats.Captures.Load(),
		"failures": w.Stats.Failures.Load(),
	}
}

var tmpOverrides = []string{"TMP", "TEMP", "TMPDIR", "HOME", "HOMEPATH", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"}

var envImportWindows = []string{
	"COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432",
	"COMPUTERNAME",
	"LOCALAPPDATA", "LOGONSERVER",
	"ALLUSERSAPPDATA", "ALLUSERSPROFILE",
	"APPDATA", "HOMEDRIVE",
	"COMSPEC", "PATH", "PATHEXT",
	"NUMBER_OF_PROCESSORS", "OS",
	"PROCESSOR_ARCHITECTURE", "PROCESSOR_IDENTIFIER", "PROCESSOR_LEVEL", "PROCESSOR_REVISION",
	"PROGRAMW6432", "PSMODULEPATH", "PUBLIC", "SESSIONNAME",
	"SystemDrive", "SystemRoot",
	"USERPROFILE",
	"DEFAULTUSERPROFILE", // Refers to the value in HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList [DefaultUserProfile].
	"PROFILESFOLDER",     // Refers to the value in HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList [ProfilesDirectory].
	"PROGRAMFILES",
	"PROGRAMFILES(X86)", // Refers to the C:\Program Files (x86) folder on 64-bit systems.
	"PROGRAMDATA",       // Refers to the C:\Program Data folder.
	"SYSTEM",            // Refers to %WINDIR%\system32.
	"SYSTEM16",          // Refers to %WINDIR%\system.
	"SYSTEM32",          // Refers to %WINDIR%\system32.
	"SYSTEMDRIVE",       // The drive that holds the Windows folder. This value is a drive name and not a folder name (C: not C:\).
	"SYSTEMPROFILE",     // Refers to the value in HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\S-1-5-18 [ProfileImagePath].
	"SYSTEMROOT",        // Same as WINDIR.
	"WINDIR",            // Refers to the Windows folder located on the system drive.
	"USERNAME",          // The name of the user currently logged on to the Windows operating system.
	"USERSID",           // Represents the current user-account security identifier (SID).
	"USERDOMAIN",        // The name of the Windows domain that contains the user-account.
}

var envImportUnix = []string{
	"PATH", "SHELL", "TERM",
}

func (w *Webshot) GetCleanEnv(baseDir string) map[string]string {
	username := w.Username
	if username == "" {
		uptr, err := user.Current()
		if err != nil {
			username = "root"
		} else {
			username = uptr.Username
		}
	}

	// Set up a clean environment for the browser process
	var envSys []string
	if runtime.GOOS == "windows" {
		envSys = envImportWindows
	} else {
		envSys = envImportUnix
	}
	// Import only a minimal set of environment variables
	r := make(map[string]string, len(envSys)+len(tmpOverrides)+4)
	for _, e := range envSys {
		if v := os.Getenv(e); v != "" {
			r[e] = v
		}
	}
	// Override the various writeable directories with the base directory
	for _, v := range tmpOverrides {
		r[v] = baseDir
	}
	// Override some specific values for Chromes
	r["GOOGLE_API_KEY"] = "no"
	r["GOOGLE_DEFAULT_CLIENT_ID"] = "no"
	r["LOGNAME"] = username
	r["USER"] = username
	// Disable Microsoft Edge telemetry
	r["MSEDGEDRIVER_TELEMETRY_OPTOUT"] = "1"
	return r
}

func (w *Webshot) GetStatsString() string {
	stats := w.GetStats()
	statKeys := make([]string, 0, len(stats))
	for k := range stats {
		statKeys = append(statKeys, k+":"+strconv.FormatUint(stats[k], 10))
	}
	sort.Strings(statKeys)
	return strings.Join(statKeys, ", ")
}

// Screenshot takes screenshots using chromium and returns the best image data as a base64 string
func (w *Webshot) Screenshot(url string) (*WebshotResult, error) {
	var err error
	res := &WebshotResult{DOM: map[string]string{}}

	stime := time.Now()
	if err = w.Init(); err != nil {
		w.Stats.Failures.Add(1)
		return nil, fmt.Errorf("init: %w", err)
	}
	if elapsed := time.Since(stime); elapsed > time.Second {
		w.Logger.Debugf("screenshot %s init took longer than expected: %s", url, elapsed)
	}

	// Set a timeout for all stages (page, nav, screenshot)
	pageCtx, cancel := context.WithTimeout(context.Background(), w.Timeout)
	defer cancel()

	// Create a blank page first to avoid navigation timeouts
	page, err := w.Browser.PageWithContext(pageCtx, proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		w.Stats.Failures.Add(1)
		return nil, fmt.Errorf("page: %w", err)
	}
	defer page.Close()

	page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:             w.Width,
		Height:            w.Height,
		DeviceScaleFactor: w.DeviceScaleFactor,
	})

	// Navigate to the target URL
	waitForNav := page.WaitNavigation(proto.PageLifecycleEventNameNetworkAlmostIdle)
	if err = page.Navigate(url); err != nil {
		w.Stats.Failures.Add(1)
		return nil, fmt.Errorf("navigate: %w", err)
	}

	// Wait for the page to load
	waitForNav()

	// Move the mouse a bit to try and trigger any lazy rendering
	_ = page.Mouse.MoveTo(proto.Point{X: float64(rand.IntN(w.Width)), Y: float64(rand.IntN(w.Height))}) //nolint:gosec

	var bestCap []byte
	var buf []byte

	// Try multiple times to get the best screenshot, using the largest image as the best candidate
	captures := 0
	for i := 0; i < w.MaxCaptures; i++ {
		if i > 0 {
			time.Sleep(TimeBetweenCaptures)
		}

		stime = time.Now()
		buf, err = page.Screenshot(w.Fullpage, &proto.PageCaptureScreenshot{
			Format:  proto.PageCaptureScreenshotFormatPng,
			Quality: gson.Int(100),
		})

		if err != nil && strings.Contains(err.Error(), cdp.ErrNotAttachedToActivePage.Message) {
			// Wait for tag to attach
			continue
		}

		res.CaptureMeta = append(res.CaptureMeta, WebshotCaptureMeta{
			Bytes:        int64(len(buf)),
			Milliseconds: int64(time.Since(stime) / time.Millisecond),
			Error:        err,
		})

		if err != nil {
			break
		}

		// Look for the least compressible image the lazy way as the best candidate
		if len(buf) > len(bestCap) {
			bestCap = buf
		}

		captures++
		if captures >= w.MinCaptures {
			break
		}
	}

	if len(bestCap) == 0 {
		w.Stats.Failures.Add(1)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out: %w", err)
		}
		return nil, fmt.Errorf("empty screenshot")
	}

	scrapeGlobals := `() => (function () {
const t = window.document.createElement("iframe");
t.src = "about:blank";
  window.document.body.appendChild(t);
  const g = Object.keys(t.contentWindow);
  window.document.body.removeChild(t);
  const f = Object.keys(window).filter((key) => {
    const d = g.includes(key);
    return !d;
  });
  return f;
})()`

	okeys, err := page.Eval(scrapeGlobals)
	if err != nil {
		w.Logger.Tracef("failed to scrape globals from %s: %v", url, err)
	} else {
		res.DOM["globals"] = okeys.Value.JSON("", "")
	}

	w.Stats.Captures.Add(1)
	if len(bestCap) > 0 {
		err = nil
	}

	res.Error = err
	res.Image = bestCap
	return res, nil
}

// Cleanup cleans up browser resources with locking
func (w *Webshot) Cleanup() {
	w.Lock()
	defer w.Unlock()
	w.cleanup()
}

// cleanup performs the actual cleanup of browser resources, without a lock
func (w *Webshot) cleanup() {
	defer func() {
		if r := recover(); r != nil {
			if w.Logger != nil {
				w.Logger.Warnf("panic while cleaning up webshot: %v", r)
			} else {
				logrus.Warnf("panic while cleaning up webshot: %v", r)
			}
		}
	}()
	b := w.Browser
	if b != nil {
		l := b.GetLauncher()
		if l != nil {
			l.Cleanup()
		}
		b.Close()
		w.Browser = nil
	}
	w.deleteProfileDir(w.TempDir)
}

func (w *Webshot) deleteProfileDir(profileDir string) {
	defer func() {
		if r := recover(); r != nil {
			w.Logger.Warnf("panic while deleting profile dir: %v", r)
		}
	}()
	// The calling go process must stick around for a bit for this to work.
	for range 5 {
		os.RemoveAll(profileDir)
		time.Sleep(time.Second)
	}
}

func (w *Webshot) GetBrowserPath() string {
	return w.LaunchedChromiumPath
}

func (w *Webshot) GetBrowserVersion() string {
	return w.LaunchedVersion
}

func (w *Webshot) GetUsername() string {
	return w.Username
}

func (w *Webshot) GetUID() int {
	return w.Setuid
}

func (w *Webshot) GetGID() int {
	return w.Setgid
}

func resolvePreferredUIDGID() (int, int, string) {
	userInfo, err := user.Current()
	username := "root"
	if err == nil {
		username = userInfo.Username
	}
	if runtime.GOOS == "windows" {
		return 0, 0, username
	}
	if os.Geteuid() != 0 {
		return os.Geteuid(), os.Getegid(), username
	}
	defaultUsers := []string{"webshot-chrome", "nobody", "daemon"}
	var preferredUsers []string
	// Check for any users specified in the environment variable
	for envUser := range strings.SplitSeq(os.Getenv("WEBSHOT_CHROMIUM_USER"), ",") {
		if envUserClean := strings.TrimSpace(envUser); envUserClean != "" {
			preferredUsers = append(preferredUsers, envUserClean)
		}
	}
	// Resolve the first matching user in priority order
	preferredUsers = append(preferredUsers, defaultUsers...)
	for _, pu := range preferredUsers {
		user, err := user.Lookup(pu)
		if err == nil {
			userUid, err := strconv.Atoi(user.Uid)
			if err != nil {
				continue
			}
			userGid, err := strconv.Atoi(user.Gid)
			if err != nil {
				continue
			}
			if userUid != 0 && userGid != 0 {
				return userUid, userGid, pu
			}
		}
	}
	return os.Geteuid(), os.Getegid(), username
}

// LimitedDiscardingBuffer is an io.Writer that stores up to maxLen bytes and discards any additional
// data written to it, always returning len(inp), nil
type LimitedDiscardingBuffer struct {
	buf    []byte
	maxLen int
	curLen int
}

func NewLimitedBuffer(maxLen int) *LimitedDiscardingBuffer {
	return &LimitedDiscardingBuffer{
		buf:    make([]byte, 0, maxLen),
		maxLen: maxLen,
		curLen: 0,
	}
}

func (lb *LimitedDiscardingBuffer) Write(p []byte) (n int, err error) {
	remaining := lb.maxLen - len(lb.buf)
	if remaining <= 0 {
		return len(p), nil
	}
	lb.buf = append(lb.buf, p[:min(remaining, len(p))]...)
	return len(p), nil
}

func (lb *LimitedDiscardingBuffer) String() string {
	return string(lb.buf)
}

// GetExecutablePath returns the full path to the running binary
func GetExecutablePath() string {
	filename, _ := os.Executable()
	filename, _ = filepath.Abs(filename)
	return filename
}

// GetExecutableDir returns the full path to the running binary's directory
func GetExecutableDir() string {
	return filepath.Dir(GetExecutablePath())
}

func GetwebshotChromiumCacheDir() string {
	if dir := os.Getenv("WEBSHOT_CHROMIUM_CACHE_DIR"); dir != "" {
		return dir
	}
	execDir := GetExecutableDir()
	if runtime.GOOS == "windows" {
		// Use a subdirectory of the installation path
		return filepath.Join(execDir, "chromium")
	}
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(execDir, home) {
		// Running from within a user home directory
		return filepath.Join(home, ".cache", "webshot", "chromium")
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		// Use XDG cache path if set
		return filepath.Join(v, "webshot", "chromium")
	}
	if home != "" {
		// Use the cache directory in the user's home
		return filepath.Join(home, ".cache", "webshot", "chromium")
	}
	// Non-root execution with no HOME should use a relative path
	return filepath.Join(execDir, "chromium")
}

// GetDefaultOptions returns a reasonable set of the defaults
func GetDefaultOptions(logger *logrus.Entry) []WebshotOption {
	screenshotOpts := []WebshotOption{
		WithLogger(logger),
		WithCacheDir(GetwebshotChromiumCacheDir()),
		WithTimeout(time.Second * 15),
		WithLaunchTimeout(time.Second * 30),
	}

	// Determine whether to disable the system installation of Chromium/Chrome/Edge/etc
	useSystem := true
	if v, err := strconv.ParseBool(os.Getenv("WEBSHOT_CHROMIUM_IGNORE_SYSTEM")); err == nil && v {
		useSystem = false
	}
	screenshotOpts = append(screenshotOpts, WithUseSystemChromium(useSystem))

	// Determine whether to automatically install Chromium if no other option is found
	useAutomaticInstall := false
	if v, err := strconv.ParseBool(os.Getenv("WEBSHOT_CHROMIUM_AUTOMATIC_INSTALL")); err == nil && v {
		useAutomaticInstall = true
	}
	screenshotOpts = append(screenshotOpts, WithUseAutomaticInstall(useAutomaticInstall))

	if v, err := strconv.ParseBool(os.Getenv("WEBSHOT_CHROMIUM_DEBUG")); err == nil && v {
		logger.Logger.SetLevel(logrus.TraceLevel)
	}

	return screenshotOpts
}
