package launcher

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/runZeroInc/go-rod/lib/utils"
	"github.com/sirupsen/logrus"
)

/*
	Google's Chromium-for-Testing (CfT) project provides builds for macOS (Intel and ARM), Windows (x86 and x64), and Linux (x64):
	- https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json

	Playwright offers Chromium builds for Linux on ARM:
	- https://raw.githubusercontent.com/microsoft/playwright/refs/heads/main/packages/playwright-core/browsers.json
	- https://playwright.azureedge.net/builds/chromium/<revision>/chromium-linux-arm64.zip,

	Other platforms should use operating-specific packages to install Chromium or Chrome.
	Note that systems using MUSL instead of GLIBC (like Alpine Linux) require OS packages as well.
*/

// TODO: Consider using chromedriver instead of calling chrome directly:
// - https://github.com/VibiumDev/vibium/blob/main/clicker/internal/browser/installer.go

// ChromiumForTestingLatestDownloadsURL is the URL to fetch the latest Chromium-for-Testing metadata.
const ChromiumForTestingLatestDownloadsURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

// MaxPackageFileSize sets a per-file limit for package extraction.
const MaxPackageFileSize = 1024 * 1024 * 1024 // 1GiB

// MaxPackageTotalSize sets a total limit for package extraction.
const MaxPackageTotalSize = 1024 * 1024 * 1024 * 2 // 2GiB

// MaxMetdataTimeout defines how long to wait for a metadata response.
const MaxMetadataTimeout = time.Second * 10

// MaxMetdataSize defines an upper bound for metadata response size.
const MaxMetadataSize = 1024 * 1024 * 256

// GoogleCFTLatestDownloadsMeta represents the structure of the Chromium-for-Testing latest downloads metadata.
type GoogleCFTLatestDownloadsMeta struct {
	Timestamp time.Time `json:"timestamp"`
	Channels  struct {
		Stable struct {
			Channel   string `json:"channel"`
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
				Chromedriver []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chromedriver"`
				ChromeHeadlessShell []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome-headless-shell"`
			} `json:"downloads"`
		} `json:"Stable"`
		Beta struct {
			Channel   string `json:"channel"`
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
				Chromedriver []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chromedriver"`
				ChromeHeadlessShell []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome-headless-shell"`
			} `json:"downloads"`
		} `json:"Beta"`
		Dev struct {
			Channel   string `json:"channel"`
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
				Chromedriver []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chromedriver"`
				ChromeHeadlessShell []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome-headless-shell"`
			} `json:"downloads"`
		} `json:"Dev"`
		Canary struct {
			Channel   string `json:"channel"`
			Version   string `json:"version"`
			Revision  string `json:"revision"`
			Downloads struct {
				Chrome []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome"`
				Chromedriver []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chromedriver"`
				ChromeHeadlessShell []struct {
					Platform string `json:"platform"`
					URL      string `json:"url"`
				} `json:"chrome-headless-shell"`
			} `json:"downloads"`
		} `json:"Canary"`
	} `json:"channels"`
}

// ResolveChromiumForTestingPlatform maps OS and architecture to Chromium-for-Testing platform strings.
func ResolveChromiumForTestingPlatform(os string, arch string) string {
	switch os {
	case "darwin":
		switch arch {
		case "arm64":
			return "mac-arm64"
		case "amd64":
			return "mac-x64"
		}
	case "linux":
		if arch == "amd64" {
			return "linux64"
		}
	case "windows":
		switch arch {
		case "amd64",
			"arm64": // Windows ARM64 uses the x86_64 binary via emulation
			return "win64"
		case "386":
			return "win32"
		}
	}
	return ""
}

// PlaywrightBrowserMetaURL fetches the latest Playwright browsers version manifest.
const PlaywrightBrowserMetaURL = "https://raw.githubusercontent.com/microsoft/playwright/refs/heads/main/packages/playwright-core/browsers.json"

// PlaywrightLinuxArm64URL is the URL template for downloading Linux ARM64 Chromium builds from Playwright.
const PlaywrightLinuxArm64URL = "https://playwright.azureedge.net/builds/chromium/%s/chromium-linux-arm64.zip"

// PlaywrightBrowsersMeta represents the structure of the Playwright browsers metadata.
type PlaywrightBrowsersMeta struct {
	Comment  string `json:"comment"`
	Browsers []struct {
		Name              string            `json:"name"`
		Revision          string            `json:"revision"`
		InstallByDefault  bool              `json:"installByDefault"`
		BrowserVersion    string            `json:"browserVersion,omitempty"`
		Title             string            `json:"title,omitempty"`
		RevisionOverrides map[string]string `json:"revisionOverrides,omitempty"`
	} `json:"browsers"`
}

// ResolveLatestDownloadURL fetches the latest Chromium download URL for the specified OS and architecture.
// It returns an error if the platform is unsupported or if there are issues fetching or parsing the metadata.
func ResolveLatestDownloadURL(os string, arch string) (string, string, error) {
	// Microsoft's Playwright does not support Windows ARM64 and falls back to emulation using Win64
	if os == "windows" && arch == "arm64" {
		arch = "amd64"
	}
	// First try Google Chromium-for-Testing for common platforms
	if cftPlatform := ResolveChromiumForTestingPlatform(os, arch); cftPlatform != "" {
		var latestJSON GoogleCFTLatestDownloadsMeta
		if err := downloadAndParseJSON(ChromiumForTestingLatestDownloadsURL, &latestJSON); err != nil {
			return "", "", err
		}
		stable := latestJSON.Channels.Stable
		for _, download := range stable.Downloads.Chrome {
			if download.Platform == cftPlatform {
				return download.URL, stable.Revision, nil
			}
		}
	}
	// Special handling for Linux ARM64 via Playwright
	if os == "linux" && arch == "arm64" {
		var latestJSON PlaywrightBrowsersMeta
		if err := downloadAndParseJSON(PlaywrightBrowserMetaURL, &latestJSON); err != nil {
			return "", "", err
		}
		for _, browser := range latestJSON.Browsers {
			if browser.Name == "chromium" {
				return fmt.Sprintf(PlaywrightLinuxArm64URL, browser.Revision), browser.Revision, nil
			}
		}
	}
	return "", "", fmt.Errorf("unsupported platform: %s-%s", os, arch)
}

// downloadAndParseJSON fetches JSON data from the specified URL and decodes it into the target structure.
func downloadAndParseJSON(url string, target any) error {
	ctx, cancel := context.WithTimeout(context.Background(), MaxMetadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for latest metadata: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()

	// Set a reasonable maximum for the metadata file
	lr := io.LimitReader(resp.Body, MaxMetadataSize)
	decoder := json.NewDecoder(lr)
	err = decoder.Decode(target)

	// Drain the body
	io.Copy(io.Discard, resp.Body)

	if err != nil {
		return fmt.Errorf("failed to decode metadata response: %w", err)
	}
	return nil
}

// Browser is a helper to download browser smartly.
type Browser struct {
	Context             context.Context
	OS                  string
	Arch                string
	CacheDir            string
	TempDir             string
	Logger              *logrus.Logger
	HTTPClient          *http.Client
	Timeout             time.Duration
	UseSystemChrome     bool
	UseChromePath       string
	UseAutomaticInstall bool
	WindowWidth         int
	WindowHeight        int
	UserAgent           string
	WithExecFlags       map[string]string
	WithoutExecFlags    []string
	WithEnv             map[string]string
	UID                 int
	GID                 int
	NoSandbox           bool
	HideWindow          bool

	workingDir             string
	downloadURL            string
	chromeDownloadRevision int
	chromeVersion          string
	chromeBinary           string
	xvfbEnabled            bool
}

func (b *Browser) String() string {
	return fmt.Sprintf("Browser{OS: %s, Arch: %s, CacheDir: %s, UseSystemChrome: %v, UseChromePath: %s, UseAutomaticInstall: %v, Revision: %d, DownloadURL: %s}",
		b.OS, b.Arch, b.CacheDir, b.UseSystemChrome, b.UseChromePath, b.UseAutomaticInstall, b.chromeDownloadRevision, b.downloadURL)
}

func (b *Browser) GetDownloadURL() string {
	return b.downloadURL
}

func (b *Browser) GetDownloadRevision() int {
	return b.chromeDownloadRevision
}

func (b *Browser) GetChromeVersion() string {
	return b.chromeVersion
}

func (b *Browser) GetChromeBinary() string {
	return b.chromeBinary
}

func (b *Browser) SetChromeBinary(v string) {
	b.chromeBinary = v
}

func (b *Browser) GetWorkingDir() string {
	return b.workingDir
}

func (b *Browser) SetWorkingDir(v string) {
	b.workingDir = v
}

func (b *Browser) GetEnv() map[string]string {
	return b.WithEnv
}

func (b *Browser) SetEnv(k, v string) {
	b.WithEnv[k] = v
}

func (b *Browser) DeleteEnv(k, v string) {
	delete(b.WithEnv, k)
}

func (b *Browser) GetXVFB() bool {
	return b.xvfbEnabled
}

func (b *Browser) SetXVFB(v bool) {
	b.xvfbEnabled = v
}

// BrowserOption defines a function type for configuring Browser instances.
type BrowserOption func(*Browser)

func WithTimeout(timeout time.Duration) BrowserOption {
	return func(b *Browser) {
		b.Timeout = timeout
	}
}

func WithContext(ctx context.Context) BrowserOption {
	return func(b *Browser) {
		b.Context = ctx
	}
}

func WithOS(os string) BrowserOption {
	return func(b *Browser) {
		b.OS = os
	}
}

func WithArch(arch string) BrowserOption {
	return func(b *Browser) {
		b.Arch = arch
	}
}

func WithLogger(logger *logrus.Logger) BrowserOption {
	return func(b *Browser) {
		b.Logger = logger
	}
}

func WithHTTPClient(client *http.Client) BrowserOption {
	return func(b *Browser) {
		b.HTTPClient = client
	}
}

// WithCacheDir sets the downloaded browser cache directory.
func WithCacheDir(dir string) BrowserOption {
	return func(b *Browser) {
		b.CacheDir = dir
	}
}

// WithTempDir sets the temporary directory for browser operations.
func WithTempDir(dir string) BrowserOption {
	return func(b *Browser) {
		b.TempDir = dir
	}
}

func WithUseAutomaticInstall(auto bool) BrowserOption {
	return func(b *Browser) {
		b.UseAutomaticInstall = auto
	}
}

func WithUseChromePath(path string) BrowserOption {
	return func(b *Browser) {
		b.UseChromePath = path
	}
}

func WithUseSystemChrome(use bool) BrowserOption {
	return func(b *Browser) {
		b.UseSystemChrome = use
	}
}

func WithWindowSize(width, height int) BrowserOption {
	return func(b *Browser) {
		b.WindowWidth = width
		b.WindowHeight = height
	}
}

func WithUserAgent(ua string) BrowserOption {
	return func(b *Browser) {
		b.UserAgent = ua
	}
}

func WithExecFlags(f map[string]string) BrowserOption {
	return func(b *Browser) {
		b.WithExecFlags = f
	}
}

func WithoutExecFlags(v []string) BrowserOption {
	return func(b *Browser) {
		b.WithoutExecFlags = v
	}
}

func WithWorkingDir(dir string) BrowserOption {
	return func(b *Browser) {
		b.workingDir = dir
	}
}

func WithEnv(f map[string]string) BrowserOption {
	return func(b *Browser) {
		b.WithEnv = f
	}
}

func WithXVFB(v bool) BrowserOption {
	return func(b *Browser) {
		b.xvfbEnabled = v
	}
}

func WithUID(id int) BrowserOption {
	return func(b *Browser) {
		b.UID = id
	}
}

func WithGID(id int) BrowserOption {
	return func(b *Browser) {
		b.GID = id
	}
}

func WithNoSandbox(v bool) BrowserOption {
	return func(b *Browser) {
		b.NoSandbox = v
	}
}

func WithHideWindow(v bool) BrowserOption {
	return func(b *Browser) {
		b.HideWindow = v
	}
}

// NewBrowser defines a Browser with user-provided options.
func NewBrowser(options ...BrowserOption) (*Browser, error) {
	b := &Browser{
		UseAutomaticInstall: true,
		UseSystemChrome:     true,
		Context:             context.Background(),
		WithExecFlags:       map[string]string{},
		WithEnv:             map[string]string{},
		TempDir:             "",
		workingDir:          ".",
	}

	// Apply options
	for _, option := range options {
		option(b)
	}

	// Ensure a logger is specified
	if b.Logger == nil {
		b.Logger = logrus.New()
	}

	// Validate defaults

	if b.OS == "" {
		b.OS = runtime.GOOS
	}

	if b.Arch == "" {
		b.Arch = runtime.GOARCH
	}

	if b.Logger == nil {
		b.Logger = logrus.New()
	}

	if b.HTTPClient == nil {
		b.HTTPClient = &http.Client{
			Timeout: 15 * time.Minute,
		}
	}

	if b.CacheDir == "" {
		cacheDirs, err := GetDefaultBrowserCacheDirs(b.OS)
		if err != nil {
			return nil, err
		}
		b.CacheDir = cacheDirs[0]
	}

	// Prevent fallback if an explicit path is set
	if b.UseChromePath != "" {
		b.UseSystemChrome = false
		b.UseAutomaticInstall = false
	}

	// Validate that the binary is usable
	cpath, err := b.ChooseChromePath()
	if err == nil {
		b.chromeBinary = cpath
		return b, nil
	}

	if !b.UseAutomaticInstall {
		// No local installation and online updates are disabled
		return nil, err
	}

	b.Logger.Debugf("resolving the latest chrome manifest...")
	dpath, rev, err := ResolveLatestDownloadURL(b.OS, b.Arch)
	if err != nil {
		return nil, err
	}

	if dpath == "" {
		return nil, fmt.Errorf("unsupported platform")
	}

	// Store the download path and revision for the call to Download()
	b.downloadURL = dpath
	b.chromeDownloadRevision, err = strconv.Atoi(rev)
	if err != nil {
		return nil, fmt.Errorf("bad revision '%s': %w", rev, err)
	}

	b.Logger.Debugf("chrome revision %d found at %s", b.chromeDownloadRevision, b.downloadURL)

	if err := b.Download(); err != nil {
		return nil, fmt.Errorf("automatic install failed: %w", err)
	}

	cpath, err = b.ChooseChromePath()
	if err == nil {
		b.Logger.Debugf("using new executable %s", cpath)
		b.chromeBinary = cpath
		return b, nil
	}

	return b, fmt.Errorf("failed to use new download: %w", err)
}

// NewBrowserMust with default values.
func NewBrowserMust(opts ...BrowserOption) *Browser {
	b, err := NewBrowser(opts...)
	if err != nil {
		panic(err)
	}
	return b
}

// DownloadDir returns the browser download directory for this revision.
func (b *Browser) DownloadDir() string {
	return filepath.Join(b.CacheDir, fmt.Sprintf("chromium-%d", b.chromeDownloadRevision))
}

// BinPath returns the browser binary path.
func (b *Browser) BinPath() string {
	return b.UseChromePath
}

// Download the browser to the local cache
func (b *Browser) Download() error {
	// Resolve the latest download URL if not already set
	if b.chromeDownloadRevision == 0 || b.downloadURL == "" {
		dpath, rev, err := ResolveLatestDownloadURL(b.OS, b.Arch)
		if err != nil {
			return fmt.Errorf("resolve latest download URL: %w", err)
		}
		if dpath == "" {
			return fmt.Errorf("unsupported platform")
		}
		b.downloadURL = dpath
		b.chromeDownloadRevision, err = strconv.Atoi(rev)
		if err != nil {
			return fmt.Errorf("bad revision '%s': %w", rev, err)
		}
	}

	// Check to see if we've already downloaded this versions
	latestRev, err := os.ReadFile(filepath.Join(b.CacheDir, "LATEST.txt"))
	if err == nil {
		latestRevInt, err := strconv.Atoi(strings.TrimSpace(string(latestRev)))
		if err == nil && latestRevInt > 0 {
			if latestRevInt >= b.chromeDownloadRevision {
				b.Logger.Debugf("browser revision %d already installed (skipping %d)", latestRevInt, b.chromeDownloadRevision)
				return nil
			}
		}
	}

	// Prepare the download directory
	downloadDir := b.DownloadDir()
	b.Logger.Debugf("downloading chrome revision %d from %s to %s (%s-%s)", b.chromeDownloadRevision, b.downloadURL, downloadDir, b.OS, b.Arch)

	if err := os.MkdirAll(downloadDir, 0o755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	// Lock the specific Chrome revision against multiple updates
	fl := flock.New(downloadDir + ".lock")
	defer func() {
		err := fl.Unlock()
		if err != nil {
			b.Logger.Errorf("failed to unlock flock: %v", err)
		}
	}()

	ctx := context.Background()
	ok, err := fl.TryLockContext(ctx, time.Minute)
	if !ok {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}

	if err := os.MkdirAll(downloadDir, 0o755); err != nil { //nolint:gosec
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "go-rod-chromium-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	req, err := http.NewRequestWithContext(b.Context, http.MethodGet, b.downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed get zip: %w", err)
	}

	// Limit the maximum download size and check for truncation
	lr := io.LimitReader(res.Body, MaxPackageTotalSize)
	n, err := io.Copy(tmpFile, lr)
	if err == nil {
		// Drain the reader and check for truncation
		nLeft, _ := io.Copy(io.Discard, res.Body)
		if nLeft > 0 {
			err = fmt.Errorf("download exceeded size (%d/%d)", n, n+nLeft)
		}
	}

	_ = res.Body.Close()
	_ = tmpFile.Close()

	if err != nil {
		return fmt.Errorf("failed to download zip: %w", err)
	}

	b.Logger.Debugf("downloaded %d bytes", n)

	fd, err := os.Open(tmpName) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat temp file: %w", err)
	}

	var totalCount int64
	zr, err := zip.NewReader(fd, fi.Size())
	if err != nil {
		return fmt.Errorf("failed to read zip file: %w", err)
	}

	for _, f := range zr.File {

		fpath, err := cleanZipFileName(b.DownloadDir(), f.Name, 1)
		if err != nil {
			b.Logger.Debugf("skipping extracting of %s: %v", f.Name, err)
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, 0o755); err != nil { //nolint:gosec
				return fmt.Errorf("failed to create directory %s: %w", fpath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil { //nolint:gosec
			return fmt.Errorf("failed to create directory for file %s: %w", fpath, err)
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755) //nolint:gosec
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", fpath, err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip entry %s: %w", f.Name, err)
		}

		lr := io.LimitReader(rc, MaxPackageFileSize)
		cnt, err := io.Copy(outFile, lr)
		if err != nil {
			_ = rc.Close()
			_ = outFile.Close()
			return fmt.Errorf("failed to extract file %s: %w", fpath, err)
		}
		totalCount += cnt
		if totalCount > MaxPackageTotalSize {
			_ = rc.Close()
			_ = outFile.Close()
			return fmt.Errorf("maximum size reached: %d", totalCount)
		}

		_ = rc.Close()
		_ = outFile.Close()
	}

	fl2 := flock.New(filepath.Join(b.CacheDir, "LATEST.txt.lock"))
	defer func() {
		err := fl2.Unlock()
		if err != nil {
			b.Logger.Errorf("failed to unlock flock: %v", err)
		}
	}()

	latestFile := filepath.Join(b.CacheDir, "LATEST.txt")
	err = os.WriteFile(latestFile, []byte(strconv.Itoa(b.chromeDownloadRevision)), 0o644) //nolint:gosec
	if err != nil {
		b.Logger.Errorf("failed to write %s: %v", latestFile, err)
	}
	b.Logger.Debugf("installed revision %d", b.chromeDownloadRevision)

	return nil
}

// Get is a smart helper to get the browser executable path and version.
// If [Browser.BinPath] is not valid it will auto download the browser to [Browser.BinPath].
func (b *Browser) Get() (string, error) {
	// Try to validate existing browser binaries
	path, err := b.ChooseChromePath()
	if err == nil {
		return path, nil
	}
	if !b.UseAutomaticInstall {
		return "", err
	}

	// Try to download the latest browser binary into the cache
	err = b.Download()
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}

	// Validate again with the newly downloaded binary
	path, err = b.ChooseChromePath()
	return path, err
}

// MustGet is similar with Get.
func (b *Browser) MustGet() string {
	p, err := b.Get()
	utils.E(err)
	return p
}

// GetExecutable returns the first valid Chrome executable path found.
func (b *Browser) ChooseChromePath() (string, error) {
	chromePaths := b.ResolveChromePaths(b.OS)
	for _, p := range chromePaths {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		return p, nil
	}
	return "", fmt.Errorf("no executable found in %+v", chromePaths)
}

// ResolveChromePaths returns a list of possibly usable Chrome executables.
func (b *Browser) ResolveChromePaths(srcOS string) []string {
	paths := []string{}
	// If a path is specified, only use this path and don't fall back
	if b.UseChromePath != "" {
		paths = []string{b.UseChromePath}
	} else {
		paths = append(paths, ResolveChromePathsFromCache(srcOS, b.CacheDir)...)
		// Check the common system chrome paths
		if b.UseSystemChrome {
			paths = append(paths, ResolveChromePathsFromSystem(srcOS)...)
		}
	}
	return paths
}

// ResolveChromePathsFromCache returns a list of cached Chrome executable paths.
func ResolveChromePathsFromCache(srcOS string, extra ...string) []string {
	paths := []string{}

	// Start with any preferred cache directory from the caller
	cacheDirs := extra

	// Look for additional cache directories from the system
	systemCacheDirs, err := GetDefaultBrowserCacheDirs(srcOS)
	if err == nil {
		cacheDirs = append(cacheDirs, systemCacheDirs...)
	}

	latestPaths := []string{}

	// Search each cache directory for a LATEST.txt that points to the most
	// recently installed version of Chrome.
	for _, p := range cacheDirs {
		if p == "" {
			continue
		}
		latestRev, err := os.ReadFile(filepath.Join(p, "LATEST.txt")) //nolint:gosec
		if err == nil {
			latestRevInt, err := strconv.Atoi(strings.TrimSpace(string(latestRev)))
			if err == nil && latestRevInt > 0 {
				latestPaths = append(latestPaths, filepath.Join(p, fmt.Sprintf("chromium-%d", latestRevInt)))
			}
		}
	}

	for _, p := range latestPaths {
		for _, exe := range GetDefaultSystemChromeExecutables(srcOS) {
			paths = append(paths, filepath.Join(p, exe))
		}
	}

	return paths
}

// ResolveChromePathsFromSystem returns a list of system Chrome executable paths.
func ResolveChromePathsFromSystem(srcOS string) []string {
	paths := []string{}
	for _, p := range GetDefaultSystemChromeDirs(srcOS) {
		for _, exe := range GetDefaultSystemChromeExecutables(srcOS) {
			paths = append(paths, filepath.Join(p, exe))
		}
	}
	return paths
}

// GetDefaultSystemChromeDirs provides a prioritized list of directories where
// system-wide browser binaries are commonly found.
func GetDefaultSystemChromeDirs(srcOS string) []string {
	paths := []string{}
	switch srcOS {
	case "darwin":
		paths = append(paths, "/Applications", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin")

	case "linux":
		paths = append(paths,
			"/opt/google/chrome", "/opt/google/chrome-beta", "/opt/google/chrome-canary", "/opt/google/chrome-unstable",
			"/usr/bin", "/usr/local/bin",
			"/data/data/com.termux/files/usr/bin",
			"/opt/microsoft/msedge",
		)

	case "freebsd", "netbsd", "openbsd":
		paths = append(paths,
			"google-chrome",
			"chrome",
			"ungoogled-chromium",
			"chromium",
		)

	case "windows":
		appNames := []string{
			`Google\Chrome\Application`,
			`Google\Chrome Beta\Application`,
			`Google\Chrome Canary\Application`,
			`Google\Chrome SxS\Application`,
			`Google\Chrome Dev\Application`,
			`Chromium\Application`,
			`Microsoft\Edge\Application`,
		}

		envNames := []string{"LocalAppData", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"}
		for _, envName := range envNames {
			dirPath := os.Getenv(envName)
			if dirPath == "" {
				continue
			}
			for _, appName := range appNames {
				fileSep := `/`
				if srcOS == "windows" {
					fileSep = `\`
				}
				paths = append(paths, strings.Join([]string{dirPath, appName}, fileSep))
			}
		}
	}
	return paths
}

// GetDefaultSystemChromeExecutables returns an OS-specific list of relative paths to
// Chrome binaries, to be used with a list of directories to search.
func GetDefaultSystemChromeExecutables(srcOS string) []string {
	switch srcOS {
	case "windows":
		return []string{
			`chrome.exe`, `chromium.exe`, `msedge.exe`,
		}
	case "darwin":
		return []string{
			`Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing`,
			`Google Chrome.app/Contents/MacOS/Google Chrome`,
			`Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta`,
			`Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary`,
			`Google Chrome Dev.app/Contents/MacOS/Google Chrome Dev`,
			`Chromium.app/Contents/MacOS/Chromium`,
			`Microsoft Edge.app/Contents/MacOS/Microsoft Edge`,
			`chrome`, `chromium`, `microsoft-edge`,
		}
	}
	return []string{
		"chrome", "google-chrome", "google-chrome-beta", "google-chrome-canary", "google-chrome-unstable",
		"chromium", "chromium-browser", "microsoft-edge",
	}
}

func envMapToSlice(envMap map[string]string) []string {
	envSlice := []string{}
	for k, v := range envMap {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}
	return envSlice
}

// getDefaultHomeDir returns the preferred home directory in an OS-specific way.
func getDefaultHomeDir(srcOS string) []string {
	res := []string{}
	switch srcOS {
	case "windows":
		homeDirs := []string{"APPDATA", "HOME", "LOCALAPPDATA"}
		for _, homeDirEnv := range homeDirs {
			if homeDir := os.Getenv(homeDirEnv); homeDir != "" {
				res = append(res, homeDir)
			}
		}
	default:
		if homeDir := os.Getenv("HOME"); homeDir != "" {
			res = append(res, homeDir)
		}
	}
	return res
}

// GetDefaultBrowserCacheDir looks for the best cache location for browser downloads.
// This prefers ${ROD_BROWSER_CACHE} and falls back to ${XDG_CACHE_HOME} and finally
// ${HOME}/.cache.
func GetDefaultBrowserCacheDirs(srcOS string) ([]string, error) {
	res := []string{}
	if cacheDir := os.Getenv("ROD_BROWSER_CACHE"); cacheDir != "" {
		if st, err := os.Stat(cacheDir); err == nil && st.IsDir() {
			res = append(res, cacheDir)
		}
	}
	pathSuffix := []string{"rod", "browser"}
	if cacheDir := os.Getenv("XDG_CACHE_HOME"); cacheDir != "" {
		if st, err := os.Stat(cacheDir); err == nil && st.IsDir() {
			tpath := append([]string{cacheDir}, pathSuffix...)
			res = append(res, filepath.Join(tpath...))
		}
	}
	for _, homeDir := range getDefaultHomeDir(srcOS) {
		if st, err := os.Stat(homeDir); err == nil && st.IsDir() {
			tpath := append([]string{homeDir, ".cache"}, pathSuffix...)
			res = append(res, filepath.Join(tpath...))
		}
	}
	if len(res) == 0 {
		return nil, fmt.Errorf("failed to get default browser dir")
	}
	return res, nil
}

// LookPath searches for the preferred browser binary across OS-specific paths.
func LookPath() (found string, has bool) {
	for _, path := range ResolveChromePathsFromSystem(runtime.GOOS) {
		var err error
		found, err = exec.LookPath(path)
		has = err == nil
		if has {
			break
		}
	}
	return
}

// interface for testing.
var openExec = exec.Command

// Open tries to open the url via system's default browser.
func Open(url string) {
	// Windows doesn't support format [::]
	url = strings.Replace(url, "[::]", "[::1]", 1)

	// Prefer the system browser for the simplified Open() usage
	if bin, has := LookPath(); has {
		p := openExec(bin, url)
		_ = p.Start()
		_ = p.Process.Release()
	}
}

// cleanZipFileName cleans the zip file name by trimming traversal sequences and removing
// the specified number of leading path segments.
func cleanZipFileName(baseDir string, fname string, depth int) (string, error) {
	fname = strings.ReplaceAll(fname, "\\", "/")
	bits := []string{}
	for _, b := range strings.Split(fname, "/") {
		if b == "" || b == "." || b == ".." {
			continue
		}
		bits = append(bits, b)
	}
	if len(bits) <= depth {
		return "", fmt.Errorf("unexpected file name in zip: %s", fname)
	}

	relPath := filepath.Join(bits[depth:]...)
	absPath := filepath.Join(baseDir, relPath)

	// Ensure the resulting path is within the base directory
	if !strings.HasPrefix(absPath, filepath.Clean(baseDir)+string(os.PathSeparator)) {
		return "", fmt.Errorf("zip slip detected: %s", absPath)
	}
	return absPath, nil
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
