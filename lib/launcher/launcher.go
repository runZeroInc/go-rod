// Package launcher for launching browser utils.
package launcher

import (
	"bufio"
	"context"
	"crypto"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runZeroInc/go-rod/lib/defaults"
	"github.com/runZeroInc/go-rod/lib/launcher/flags"
	"github.com/runZeroInc/go-rod/lib/utils"
	"github.com/sirupsen/logrus"
)

// DefaultLaunchTimeout sets a limit for getting the debug url when launching the browser.
const DefaultLaunchTimeout = time.Minute * 3

// DefaultUserDataDirPrefix ...
var DefaultUserDataDirPrefix = filepath.Join(os.TempDir(), "go-rod-user-data")

var DefaultExecFlags = map[string][]string{
	// use random port by default (0)
	"remote-debugging-port": {defaults.Port},

	// enable headless by default
	"headless": nil,

	// to disable the init blank window
	"no-first-run":      nil,
	"no-startup-window": nil,

	"disable-features": {
		"site-per-process", // See https://github.com/puppeteer/puppeteer/issues/2548
		"TranslateUI",
		"OptimizationGuideModelDownloading", "OptimizationHintsFetching", "OptimizationTargetPrediction", "OptimizationHints",
	},

	"allow-chrome-scheme-url":                            nil, // Allow chrome:// URLs in headless mode
	"allow-pre-commit-input":                             nil,
	"bwsi":                                               nil,
	"disable-background-networking":                      nil,
	"disable-background-timer-throttling":                nil,
	"disable-backgrounding-occluded-windows":             nil,
	"disable-breakpad":                                   nil, // prevent crash dumps: https://github.com/runZeroInc/platform/issues/19900
	"disable-client-side-phishing-detection":             nil,
	"disable-component-extensions-with-background-pages": nil,
	"disable-component-update":                           nil,
	"disable-crash-reporter":                             nil,
	"disable-default-apps":                               nil,
	"disable-dev-shm-usage":                              nil, // Workaround for limited VM environments
	"disable-gpu":                                        nil,
	"disable-hang-monitor":                               nil,
	"disable-infobars":                                   nil,
	"disable-ipc-flooding-protection":                    nil,
	"disable-notifications":                              nil,
	"disable-plugins":                                    nil,
	"disable-popup-blocking":                             nil,
	"disable-prompt-on-repost":                           nil,
	"disable-renderer-backgrounding":                     nil,
	"disable-search-engine-choice-screen":                nil,
	"disable-site-isolation-trials":                      nil,
	"disable-sync":                                       nil,
	"disable-translate":                                  nil,
	"enable-automation":                                  nil,
	"enable-features":                                    {"NetworkService", "NetworkServiceInProcess"},
	"enable-logging":                                     {"stderr"},
	"export-tagged-pdf":                                  nil,
	"force-color-profile":                                {"srgb"},
	"generate-pdf-document-outline":                      nil,
	"hide-scrollbars":                                    nil,
	"ignore-certificate-errors":                          nil,
	"metrics-recording-only":                             nil,
	"mute-audio":                                         nil,
	"no-crashpad":                                        nil,
	"no-default-browser-check":                           nil,
	"password-store":                                     {"basic"},
	"safebrowsing-disable-auto-update":                   nil,
	"use-mock-keychain":                                  nil, // Avoid macOS keychain prompts
	"log-level":                                          {"1"},
}

// Launcher is a helper to launch browser binary smartly.
type Launcher struct {
	Flags         map[flags.Flag][]string `json:"flags"`
	Browser       *Browser
	ctx           context.Context
	ctxCancel     func()
	logger        *logrus.Entry
	parser        *ChromiumOutputParser
	pid           int
	exit          chan struct{}
	launchTimeout time.Duration
	isLaunched    int32  // zero means not launched
	launchErr     error  // stores the real error from browser process
	launchErrLock sync.Mutex
}

// New returns a launcher instance with the configured options.
func New(opts ...BrowserOption) (*Launcher, error) {
	conf := &Browser{}
	for _, opt := range opts {
		opt(conf)
	}
	if conf.Logger == nil {
		conf.Logger = logrus.StandardLogger().WithField("component", "launcher")
	}

	// Create a temporary user data dir for this browser instance
	profileDir, err := os.MkdirTemp(conf.TempDir, "go-rod-launcher-*")
	if err != nil {
		return nil, fmt.Errorf("mktemp user data dir %s: %w", profileDir, err)
	}

	execFlags := GetExecFlags(conf)
	execFlags[flags.UserDataDir] = []string{profileDir}

	// Configure the top-level context
	var srcContext context.Context
	if conf.Context != nil {
		srcContext = conf.Context
	} else {
		srcContext = context.Background()
	}

	ctx, cancel := context.WithCancel(srcContext)
	earlyCleanup := func() {
		cancel()
		_ = os.RemoveAll(profileDir)
	}

	browser, err := NewBrowser(opts...)
	if err != nil {
		earlyCleanup()
		return nil, err
	}

	l := &Launcher{
		ctx:           ctx,
		ctxCancel:     cancel,
		Flags:         execFlags,
		exit:          make(chan struct{}),
		Browser:       browser,
		parser:        NewChromiumOutputParser(),
		logger:        conf.Logger,
		launchTimeout: browser.LaunchTimeout,
	}
	l = l.WindowSize(conf.WindowWidth, conf.WindowHeight)

	return l, nil
}

func NewMust(opts ...BrowserOption) *Launcher {
	l, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return l
}

// NewUserMode is a preset to enable reusing current user data. Useful for automation of personal browser.
// If you see any error, it may because you can't launch debug port for existing browser, the solution is to
// completely close the running browser. Unfortunately, there's no API for rod to tell it automatically yet.
func NewUserMode(opts ...BrowserOption) (*Launcher, error) {
	ctx, cancel := context.WithCancel(context.Background())

	opts = append(opts,
		WithExecFlags(map[string]string{
			"remote-debugging-port": "37112",
			"no-startup-window":     "",
		}),
		WithoutExecFlags([]string{
			"headless",
		}),
	)

	b, err := NewBrowser(opts...)
	if err != nil {
		cancel()
		panic(err)
	}
	return &Launcher{
		ctx:       ctx,
		ctxCancel: cancel,
		Flags:     GetExecFlags(b),
		Browser:   b,
		exit:      make(chan struct{}),
		parser:    NewChromiumOutputParser(),
		logger:    b.Logger,
	}, nil
}

func NewUserModeMust() *Launcher {
	l, err := New()
	if err != nil {
		panic(err)
	}
	return l
}

// NewAppMode is a preset to run the browser like a native application.
// The u should be a URL.
func NewAppMode(u string) (*Launcher, error) {
	l, err := New()
	if err != nil {
		return nil, err
	}
	l.Browser.SetEnv("GOOGLE_API_KEY", "no")
	l.Set(flags.App, u).
		Headless(false).
		Delete("no-startup-window").
		Delete("enable-automation")
	return l, nil
}

func NewAppModeMust(u string) *Launcher {
	l, err := NewAppMode(u)
	if err != nil {
		panic(err)
	}
	return l
}

func GetExecFlags(conf *Browser) map[flags.Flag][]string {
	// Configure the execution flags for this launch
	execFlags := make(map[flags.Flag][]string, len(DefaultExecFlags))
	for fk, fv := range DefaultExecFlags {
		execFlags[flags.Flag(fk)] = fv
	}
	if defaults.Show {
		delete(execFlags, flags.Headless)
	}
	if defaults.Devtools {
		execFlags["auto-open-devtools-for-tabs"] = nil
	}
	if inContainer || conf.NoSandbox || (runtime.GOOS == "linux" && os.Geteuid() == 0 && conf.UID == 0) {
		execFlags[flags.NoSandbox] = nil
	}
	if defaults.Proxy != "" {
		execFlags[flags.ProxyServer] = []string{defaults.Proxy}
	}

	if conf.WindowWidth != 0 && conf.WindowHeight != 0 {
		execFlags[flags.WindowSize] = []string{
			fmt.Sprintf("%d,%d", conf.WindowWidth, conf.WindowHeight),
		}
	}
	if conf.UserAgent != "" {
		execFlags["user-agent"] = []string{conf.UserAgent}
	}

	// Clear any flags specified by the user
	for _, k := range conf.WithoutExecFlags {
		delete(execFlags, flags.Flag(k))
	}

	// Overwrite any flags specified by the user
	for k, v := range conf.WithExecFlags {
		execFlags[flags.Flag(k)] = []string{v}
	}
	return execFlags
}

// Context sets the context.
func (l *Launcher) Context(ctx context.Context) *Launcher {
	ctx, cancel := context.WithCancel(ctx)
	l.ctx = ctx
	l.parser.Context(ctx)
	l.ctxCancel = cancel
	return l
}

// LaunchTimeout sets the timeout for getting the debug url when launching the browser.
func (l *Launcher) LaunchTimeout(timeout time.Duration) *Launcher {
	l.launchTimeout = timeout
	return l
}

// Set a command line argument when launching the browser.
// Be careful the first argument is a flag name, it shouldn't contain values. The values the will be joined with comma.
// A flag can have multiple values. If no values are provided the flag will be a boolean flag.
// You can use the [Launcher.FormatArgs] to debug the final CLI arguments.
// List of available flags: https://peter.sh/experiments/chromium-command-line-switches
func (l *Launcher) Set(name flags.Flag, values ...string) *Launcher {
	name.Check()
	l.Flags[name.NormalizeFlag()] = values
	return l
}

// Get flag's first value.
func (l *Launcher) Get(name flags.Flag) string {
	if list, has := l.GetFlags(name); has {
		return list[0]
	}
	return ""
}

// Has flag or not.
func (l *Launcher) Has(name flags.Flag) bool {
	_, has := l.GetFlags(name)
	return has
}

// GetFlags from settings.
func (l *Launcher) GetFlags(name flags.Flag) ([]string, bool) {
	flag, has := l.Flags[name.NormalizeFlag()]
	return flag, has
}

// Append values to the flag.
func (l *Launcher) Append(name flags.Flag, values ...string) *Launcher {
	flags, has := l.GetFlags(name)
	if !has {
		flags = []string{}
	}
	return l.Set(name, append(flags, values...)...)
}

// Delete a flag.
func (l *Launcher) Delete(name flags.Flag) *Launcher {
	delete(l.Flags, name.NormalizeFlag())
	return l
}

// Revision of the browser to auto download.
func (l *Launcher) Revision(rev int) *Launcher {
	l.Browser.latestRevision = rev
	return l
}

// Headless switch. Whether to run browser in headless mode. A mode without visible UI.
// Note that modern Chrome versions have deprecated the old headless mode and all --headless is new mode.
func (l *Launcher) Headless(enable bool) *Launcher {
	if enable {
		return l.Set(flags.Headless)
	}
	return l.Delete(flags.Headless)
}

// NoSandbox switch. Whether to run browser in no-sandbox mode.
// Linux users may face "running as root without --no-sandbox is not supported" in some Linux/Chrome combinations.
// This function helps switch mode easily.
// Be aware disabling sandbox is not trivial. Use at your own risk.
// Related doc: https://bugs.chromium.org/p/chromium/issues/detail?id=638180
func (l *Launcher) NoSandbox(enable bool) *Launcher {
	if enable {
		return l.Set(flags.NoSandbox)
	}
	return l.Delete(flags.NoSandbox)
}

// XVFB enables to run browser in by XVFB. Useful when you want to run headful mode on linux.
func (l *Launcher) XVFB(v bool) *Launcher {
	l.Browser.SetXVFB(v)
	return l
}

// Bin overrides the chrome binary path.
func (l *Launcher) Bin(cpath string) *Launcher {
	l.Browser.SetChromiumBinary(cpath)
	return l
}

// GetBin returns the chrome binary path.
func (l *Launcher) GetBin() string {
	return l.Browser.chromiumBinary
}

// GetBin returns the chrome binary path.
func (l *Launcher) GetBinVersion() string {
	return l.Browser.chromiumVersion
}

// Preferences set chromium user preferences, such as set the default search engine or disable the pdf viewer.
// The pref is a json string, the doc is here
// https://src.chromium.org/viewvc/chrome/trunk/src/chrome/common/pref_names.cc
func (l *Launcher) Preferences(pref string) *Launcher {
	return l.Set(flags.Preferences, pref)
}

// AlwaysOpenPDFExternally switch.
// It will set chromium user preferences to enable the always_open_pdf_externally option.
func (l *Launcher) AlwaysOpenPDFExternally() *Launcher {
	return l.Set(flags.Preferences, `{"plugins":{"always_open_pdf_externally": true}}`)
}

// Devtools switch to auto open devtools for each tab.
func (l *Launcher) Devtools(autoOpenForTabs bool) *Launcher {
	if autoOpenForTabs {
		return l.Set("auto-open-devtools-for-tabs")
	}
	return l.Delete("auto-open-devtools-for-tabs")
}

// IgnoreCerts configure the Chrome's ignore-certificate-errors-spki-list argument with the public keys.
func (l *Launcher) IgnoreCerts(pks []crypto.PublicKey) error {
	spkis := make([]string, 0, len(pks))

	for _, pk := range pks {
		spki, err := certSPKI(pk)
		if err != nil {
			return fmt.Errorf("certSPKI: %w", err)
		}
		spkis = append(spkis, string(spki))
	}

	l.Set("ignore-certificate-errors-spki-list", spkis...)

	return nil
}

// UserDataDir is where the browser will look for all of its state, such as cookie and cache.
// When set to empty, browser will use current OS home dir.
// Related doc: https://chromium.googlesource.com/chromium/src/+/master/docs/user_data_dir.md
func (l *Launcher) UserDataDir(dir string) *Launcher {
	if dir == "" {
		l.Delete(flags.UserDataDir)
	} else {
		l.Set(flags.UserDataDir, dir)
	}
	return l
}

// ProfileDir is the browser profile the browser will use.
// When set to empty, the profile 'Default' is used.
// Related article: https://superuser.com/a/377195
func (l *Launcher) ProfileDir(dir string) *Launcher {
	if dir == "" {
		l.Delete(flags.ProfileDir)
	} else {
		l.Set(flags.ProfileDir, dir)
	}
	return l
}

// RemoteDebuggingPort to launch the browser. Zero for a random port. Zero is the default value.
// If it's not zero, the launcher will try to reconnect to it first, if the reconnection fails
// it will launch a new browser.
func (l *Launcher) RemoteDebuggingPort(port int) *Launcher {
	return l.Set(flags.RemoteDebuggingPort, fmt.Sprintf("%d", port))
}

// Proxy for the browser.
func (l *Launcher) Proxy(host string) *Launcher {
	return l.Set(flags.ProxyServer, host)
}

// WindowSize for the browser.
func (l *Launcher) WindowSize(x, y int) *Launcher {
	return l.Set(flags.WindowSize, fmt.Sprintf("%d,%d", x, y))
}

// WindowPosition for the browser.
func (l *Launcher) WindowPosition(x, y int) *Launcher {
	return l.Set(flags.WindowPosition, fmt.Sprintf("%d,%d", x, y))
}

// WorkingDir to launch the browser process.
func (l *Launcher) WorkingDir(path string) *Launcher {
	l.Browser.workingDir = path
	return l
}

// Env to launch the browser process. The default value is [os.Environ]().
// Usually you use it to set the timezone env. Such as:
//
//	Env(append(os.Environ(), "TZ=Asia/Tokyo")...)
func (l *Launcher) Env(env ...string) *Launcher {
	for _, s := range env {
		bits := strings.SplitN(s, "=", 2)
		if len(bits) == 1 {
			bits = append(bits, "")
		}
		l.Browser.SetEnv(bits[0], bits[1])
	}
	return l
}

// StartURL to launch.
func (l *Launcher) StartURL(u string) *Launcher {
	return l.Set("", u)
}

// FormatArgs returns the formatted arg list for cli.
func (l *Launcher) FormatArgs() ([]string, error) {
	execArgs := []string{}
	for k, v := range l.Flags {
		if k == flags.Arguments {
			continue
		}

		// fix a bug of chrome, if path is not absolute chrome will hang
		if k == flags.UserDataDir {
			abs, err := filepath.Abs(v[0])
			if err != nil {
				return execArgs, fmt.Errorf("failed to resolve profile dir %s: %v", v[0], err)
			}
			v[0] = abs
		}

		str := "--" + string(k)
		if v != nil {
			str += "=" + strings.Join(v, ",")
		}
		execArgs = append(execArgs, str)
	}

	execArgs = append(execArgs, l.Flags[flags.Arguments]...)
	sort.Strings(execArgs)
	return execArgs, nil
}

// Logger to handle stdout and stderr from browser.
// For example, pipe all browser output to stdout:
//
//	launcher.New().Logger(os.Stdout)
func (l *Launcher) Logger(logger *logrus.Entry) *Launcher {
	l.logger = logger
	return l
}

// MustLaunch is similar to Launch.
func (l *Launcher) MustLaunch() string {
	u, err := l.Launch()
	utils.E(err)
	return u
}

// Launch a standalone temp browser instance and returns the debug url.
// bin and profileDir are optional, set them to empty to use the default values.
// If you want to reuse sessions, such as cookies, set the [Launcher.UserDataDir] to the same location.
//
// Please note launcher can only be used once.
func (l *Launcher) Launch() (string, error) {
	if l.hasLaunched() {
		return "", ErrAlreadyLaunched
	}

	bin := l.Browser.GetChromiumBinary()
	if bin == "" {
		return "", fmt.Errorf("chrome path not resolved: %v", l.Browser)
	}

	l.setupUserPreferences()

	var cmd *exec.Cmd

	args, err := l.FormatArgs()
	if err != nil {
		return "", err
	}

	port := l.Get(flags.RemoteDebuggingPort)
	u, err := ResolveURL(port)
	if err == nil {
		return u, nil
	}

	cmd = exec.CommandContext(l.ctx, bin, args...) //nolint:gosec

	l.setupCmd(l.ctx, cmd, l.Browser.UID, l.Browser.GID)

	if err := ensureUserPermissions(l.Browser.UID, l.Browser.GID, l.Get(flags.UserDataDir), l.GetBin()); err != nil {
		l.logger.Errorf("failed to ensure user permissions: %v", err)
	}

	// Force a wait delay
	cmd.WaitDelay = time.Second * 1

	err = cmd.Start()
	if err != nil {
		return "", err
	}

	l.pid = cmd.Process.Pid

	go func() {
		err := cmd.Wait()
		if err != nil {
			l.launchErrLock.Lock()
			l.launchErr = err
			l.launchErrLock.Unlock()
		}
		l.ctxCancel()
		close(l.exit)
		killLeftoverProcesses(l.pid, bin)
	}()

	lt := l.launchTimeout
	if lt == 0 {
		lt = DefaultLaunchTimeout
	}
	t := time.NewTimer(lt)

	// Prevent a stall if the Chrome process doesn't launch correctly
	go func() {
		select {
		case <-t.C:
			l.ctxCancel()
		case <-l.exit:
		}
	}()

	// Wait for the Devtools debug URL
	u, err = l.getURL()

	t.Stop()

	if err != nil {
		killLeftoverProcesses(l.pid, bin)
		l.ctxCancel()
		return "", err
	}
	return ResolveURL(u)
}

func (l *Launcher) hasLaunched() bool {
	return !atomic.CompareAndSwapInt32(&l.isLaunched, 0, 1)
}

func (l *Launcher) setupUserPreferences() {
	userDir := l.Get(flags.UserDataDir)
	pref := l.Get(flags.Preferences)

	if userDir == "" || pref == "" {
		return
	}

	userDir, err := filepath.Abs(userDir)
	utils.E(err)

	profile := l.Get(flags.ProfileDir)
	if profile == "" {
		profile = "Default"
	}

	path := filepath.Join(userDir, profile, "Preferences")

	err = utils.OutputFile(path, pref)
	if err != nil {
		l.logger.Errorf("failed to write user preferences to %s: %v", path, err)
	}
}

func (l *Launcher) setupCmd(ctx context.Context, cmd *exec.Cmd, uid, gid int) {
	l.osSetupCmd(ctx, cmd, uid, gid)
	cmd.Dir = l.Browser.workingDir

	if l.Browser.WithEnv != nil {
		cmd.Env = envMapToSlice(l.Browser.WithEnv)
	} else {
		cmd.Env = os.Environ()
	}

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	outToLog := func(r io.Reader, n string) {
		defer func() {
			if r := recover(); r != nil {
				l.logger.Errorf("panic in cmd %s reader: %v", n, r)
			}
		}()
		s := bufio.NewScanner(r)
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line == "" {
				continue
			}
			l.logger.Debugf("chrome-%s: %s", n, line)
		}
	}

	go outToLog(stdoutR, "stdout")
	go outToLog(stderrR, "stderr")

	cmd.Stdout = io.MultiWriter(stdoutW, l.parser)
	cmd.Stderr = io.MultiWriter(stderrW, l.parser)
}

func (l *Launcher) getURL() (u string, err error) {
	select {
	case <-l.ctx.Done():
		// If we have a stored launch error (from cmd.Wait()), return that instead of context.Err()
		l.launchErrLock.Lock()
		storedErr := l.launchErr
		l.launchErrLock.Unlock()
		
		if storedErr != nil {
			err = storedErr
		} else {
			err = l.ctx.Err()
		}
	case u = <-l.parser.URL:
	case <-l.exit:
		err = l.parser.Err()
	}
	return
}

// PID returns the browser process pid.
func (l *Launcher) PID() int {
	return l.pid
}

// Kill the browser process.
func (l *Launcher) Kill() {
	// TODO: If we kill too fast, the browser's children processes may not be ready.
	// Browser does not have an API to tell if the children processes are ready.
	utils.Sleep(1)

	if l.PID() == 0 { // avoid killing the current process
		return
	}

	killGroup(l.PID())
	p, err := os.FindProcess(l.PID())
	if err == nil {
		_ = p.Kill()
	}
	killLeftoverProcesses(l.PID(), l.GetBin())
}

// Cleanup wait until the Browser exits and remove [flags.UserDataDir].
func (l *Launcher) Cleanup() {
	l.ctxCancel()
	<-l.exit
	l.Kill()
	dir := l.Get(flags.UserDataDir)
	_ = os.RemoveAll(dir)
}

func ensureUserPermissions(uid, gid int, userDir, binPath string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if os.Geteuid() != 0 {
		return nil
	}
	if uid == 0 {
		return nil
	}
	var res error

	if err := ensureUserPermissionsUserDir(uid, gid, userDir); err != nil {
		res = errors.Join(res, fmt.Errorf("user dir permissions: %w", err))
	}
	if err := ensureUserPermissionsBinary(uid, gid, binPath); err != nil {
		res = errors.Join(res, fmt.Errorf("binary permissions: %w", err))
	}
	return res
}

func ensureUserPermissionsUserDir(uid, gid int, userDir string) error {
	// Validate user dir
	if userDir == "" {
		return fmt.Errorf("no user-data-dir")
	}
	st, err := os.Stat(userDir)
	if err != nil {
		return fmt.Errorf("userdir %s: %w", userDir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("userdir %s is not a directory", userDir)
	}

	err = filepath.Walk(userDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == userDir {
			// Ensure that the user directory is owned by the user
			if err := os.Chown(path, uid, gid); err != nil {
				return fmt.Errorf("chown path %s: %w", path, err)
			}
			return nil
		}
		// Try to ensure all leading paths are 755
		_ = os.Chmod(path, 0o755) //nolint:gosec
		return nil
	})
	if err != nil {
		return fmt.Errorf("userdir path %s: %w", userDir, err)
	}
	return nil
}

func ensureUserPermissionsBinary(uid, gid int, binPath string) error {
	// Validate bin path
	if binPath == "" {
		return fmt.Errorf("no binary path")
	}
	st, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("bin path %s: %w", binPath, err)
	}
	if st.IsDir() {
		return fmt.Errorf("bin path %s is a directory", binPath)
	}

	err = filepath.Walk(binPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		_ = os.Chmod(path, 0o755) //nolint:gosec
		return nil
	})
	if err != nil {
		return fmt.Errorf("bin path %s: %w", binPath, err)
	}
	return nil
}
