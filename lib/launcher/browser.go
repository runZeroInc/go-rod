package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// ChromeForTestingLatestDownloadsURL is the URL to fetch the latest Chromium-for-Testing metadata.
const ChromeForTestingLatestDownloadsURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

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

// ResolveChromeForTestingPlatform maps OS and architecture to Chromium-for-Testing platform strings.
func ResolveChromeForTestingPlatform(os string, arch string) string {
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
		case "amd64":
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
	// First try Google Chromium-for-Testing for common platforms
	if cftPlatform := ResolveChromeForTestingPlatform(os, arch); cftPlatform != "" {
		var latestJSON GoogleCFTLatestDownloadsMeta
		if err := downloadJSON(ChromeForTestingLatestDownloadsURL, &latestJSON); err != nil {
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
		if err := downloadJSON(PlaywrightBrowserMetaURL, &latestJSON); err != nil {
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

// downloadJSON fetches JSON data from the specified URL and decodes it into the target structure.
func downloadJSON(url string, target any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request for latest metadata: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch latest metadata: %w", err)
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("failed to decode metadata response: %w", err)
	}
	return nil
}

// DefaultBrowserDir for downloaded browser. For unix is "$HOME/.cache/rod/browser",
// for Windows it's "%APPDATA%\rod\browser".
var DefaultBrowserDir = filepath.Join(map[string]string{
	"windows": os.Getenv("APPDATA"),
	"darwin":  filepath.Join(os.Getenv("HOME"), ".cache"),
	"linux":   filepath.Join(os.Getenv("HOME"), ".cache"),
}[runtime.GOOS], "rod", "browser")

// Browser is a helper to download browser smartly.
type Browser struct {
	Context                context.Context
	OS                     string
	Arch                   string
	RootDir                string
	Logger                 *logrus.Logger
	HTTPClient             *http.Client
	UseSystemChrome        bool
	UseChromePath          string
	UseAutomaticInstall    bool
	UseAutomaticValidation bool

	downloadURL string
	revision    int
}

func (b *Browser) String() string {
	return fmt.Sprintf("Browser{OS: %s, Arch: %s, RootDir: %s, UseSystemChrome: %v, UseChromePath: %s, UseAutomaticInstall: %v, UseAutomaticValidation: %v, Revision: %d, DownloadURL: %s}",
		b.OS, b.Arch, b.RootDir, b.UseSystemChrome, b.UseChromePath, b.UseAutomaticInstall, b.UseAutomaticValidation, b.revision, b.downloadURL)
}

func (b *Browser) GetDownloadURL() string {
	return b.downloadURL
}

func (b *Browser) GetRevision() int {
	return b.revision
}

// BrowserOption defines a function type for configuring Browser instances.
type BrowserOption func(*Browser)

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

func WithRootDir(dir string) BrowserOption {
	return func(b *Browser) {
		b.RootDir = dir
	}
}

func WithUseAutomaticInstall(auto bool) BrowserOption {
	return func(b *Browser) {
		b.UseAutomaticInstall = auto
	}
}
func WithAutomaticValidation(auto bool) BrowserOption {
	return func(b *Browser) {
		b.UseAutomaticValidation = auto
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

// NewBrowser defines a Browser with user-provided options
func NewBrowser(options ...BrowserOption) (*Browser, error) {
	b := &Browser{
		Context: context.Background(),
		RootDir: DefaultBrowserDir,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Minute,
		},
	}

	for _, option := range options {
		option(b)
	}

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

	if b.UseAutomaticValidation {
		if err := b.Validate(); err != nil {
			if !b.UseAutomaticInstall {
				return nil, err
			}
			b.Logger.Errorf("browser validation failed: %v", err)
		}
	}

	if b.UseAutomaticInstall {
		dpath, rev, err := ResolveLatestDownloadURL(b.OS, b.Arch)
		if err != nil {
			return nil, err
		}

		if dpath == "" {
			return nil, fmt.Errorf("unsupported platform")
		}
		b.downloadURL = dpath

		b.revision, err = strconv.Atoi(rev)
		if err != nil {
			return nil, fmt.Errorf("bad revision '%s': %w", rev, err)
		}
	}

	return b, nil
}

// NewBrowserMust with default values.
func NewBrowserMust(opts ...BrowserOption) *Browser {
	b, err := NewBrowser(opts...)
	if err != nil {
		panic(err)
	}
	return b
}

// Dir to download the browser.
func (b *Browser) DownloadDir() string {
	return filepath.Join(b.RootDir, fmt.Sprintf("chromium-%d", b.revision))
}

var BinPaths = map[string]string{
	//"darwin":  "Chromium.app/Contents/MacOS/Chromium",
	"darwin":  "Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
	"linux":   "chrome",
	"windows": "chrome.exe",
}

// BinPath to download the browser executable.
func (b *Browser) BinPath() string {
	bin, ok := BinPaths[b.OS]
	if !ok {
		return ""
	}
	return filepath.Join(b.DownloadDir(), filepath.FromSlash(bin))
}

// Download the browser to [Browser.BinPath].
func (b *Browser) Download() error {
	b.Logger.Printf("downloading chromium revision %d from %s to %s (%s-%s)", b.revision, b.downloadURL, b.DownloadDir(), b.OS, b.Arch)
	if err := os.MkdirAll(b.DownloadDir(), 0o755); err != nil {
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
	n, err := io.Copy(tmpFile, res.Body)
	if err != nil {
		return fmt.Errorf("failed to download zip: %w", err)
	}
	b.Logger.Printf("downloaded %d bytes", n)
	_ = tmpFile.Close()
	_ = res.Body.Close()

	fd, err := os.Open(tmpName)
	if err != nil {
		return fmt.Errorf("failed to open zip file: %w", err)
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat temp file: %w", err)
	}

	zr, err := zip.NewReader(fd, fi.Size())
	if err != nil {
		return fmt.Errorf("failed to read zip file: %w", err)
	}

	for _, f := range zr.File {
		// Replace backslashes with forward slashes out of paranoia
		dname, err := cleanZipFileName(f.Name, 1)
		if err != nil {
			b.Logger.Printf("skipping extracting of %s: %v", f.Name, err)
			continue
		}

		fpath, err := filepath.Abs(filepath.Join(b.DownloadDir(), dname))
		if err != nil {
			b.Logger.Printf("failed to get absolute path for %q: %v", dname, err)
			continue
		}

		// lc.Logger.Printf("extracting %q to %q", f.Name, fpath)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, 0o755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", fpath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0o755); err != nil {
			return fmt.Errorf("failed to create directory for file %s: %w", fpath, err)
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", fpath, err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip entry %s: %w", f.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		if err != nil {
			_ = rc.Close()
			_ = outFile.Close()
			return fmt.Errorf("failed to extract file %s: %w", fpath, err)
		}

		_ = rc.Close()
		_ = outFile.Close()
	}

	b.Logger.Println("extraction completed")

	return nil
}

// Get is a smart helper to get the browser executable path.
// If [Browser.BinPath] is not valid it will auto download the browser to [Browser.BinPath].
func (b *Browser) Get() (string, error) {
	if b.Validate() == nil {
		return b.BinPath(), nil
	}

	return b.BinPath(), b.Download()
}

// MustGet is similar with Get.
func (b *Browser) MustGet() string {
	p, err := b.Get()
	utils.E(err)
	return p
}

// Validate identifies and validates the browser binary.
func (b *Browser) Validate() error {
	chromePaths := b.ResolveChromePaths(b.OS)
	for _, p := range chromePaths {
		b.Logger.Printf("validating browser at %s", p)
		if err := b.validateAtPath(p); err == nil {
			b.Logger.Printf("found valid browser at %s", p)
			b.UseChromePath = p
			return nil
		} else {
			b.Logger.Printf("invalid browser at %s: %v", p, err)
		}
	}
	return fmt.Errorf("no valid browser found")
}

// ResolveChromePaths returns a list of possibly usable Chrome executables.
func (b *Browser) ResolveChromePaths(srcOS string) []string {
	paths := []string{}

	// If a path is specified, only use this path and don't fall back
	if b.UseChromePath != "" {
		paths = []string{b.UseChromePath}
	} else {
		// Check the go-rod browser cache
		if cacheDir, err := GetDefaultBrowserCacheDir(srcOS); err == nil {
			latestRev, err := os.ReadFile(filepath.Join(cacheDir, "LATEST.txt"))
			if err == nil {
				latestRevInt, err := strconv.Atoi(strings.TrimSpace(string(latestRev)))
				if err == nil && latestRevInt > 0 {
					paths = append(paths, filepath.Join(cacheDir, fmt.Sprintf("chromium-%d", latestRevInt)))
				}
			}
		}
		// Check the common system chrome paths
		if b.UseSystemChrome {
			paths = append(paths, GetDefaultSystemChromeDirs(srcOS)...)
		}
	}

	paths = append(paths, b.BinPath())

	return paths
}

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
				paths = append(paths, filepath.Join(dirPath, appName))
			}
		}
	}
	return paths
}

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

func (b *Browser) validateAtPath(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		return err
	}

	// TODO: Handle privilege dropping with the sandbox on Linux (prepare a new temp dir)

	cmd := exec.Command(path, "--headless", "--no-sandbox",
		"--use-mock-keychain", "--disable-dev-shm-usage",
		"--disable-gpu", "--dump-dom", "about:blank")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "error while loading shared libraries") {
			// When the os is missing some dependencies for chromium we treat it as valid binary.
			return nil
		}
		return fmt.Errorf("failed to run the browser: %w%s", err, out)
	}
	if !bytes.Contains(out, []byte(`<html><head></head><body></body></html>`)) {
		return errors.New("the browser executable doesn't support headless mode")
	}

	return nil
}

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

func GetDefaultBrowserCacheDir(srcOS string) (string, error) {
	pathSuffix := []string{"rod", "browser"}
	if cacheDir := os.Getenv("XDG_CACHE_HOME"); cacheDir != "" {
		if st, err := os.Stat(cacheDir); err == nil && st.IsDir() {
			tpath := append([]string{cacheDir}, pathSuffix...)
			return filepath.Join(tpath...), nil
		}
	}
	for _, homeDir := range getDefaultHomeDir(srcOS) {
		if st, err := os.Stat(homeDir); err == nil && st.IsDir() {
			tpath := append([]string{homeDir, ".cache"}, pathSuffix...)
			return filepath.Join(tpath...), nil
		}
	}
	return "", fmt.Errorf("failed to get default browser dir")
}

// LookPath searches for the browser executable from often used paths on current operating system.
func LookPath() (found string, has bool) {
	list := map[string][]string{
		"darwin": {
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
		},
		"linux": {
			"chrome",
			"google-chrome",
			"/usr/bin/google-chrome",
			"microsoft-edge",
			"/usr/bin/microsoft-edge",
			"chromium",
			"chromium-browser",
			"google-chrome-stable",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/snap/bin/chromium",
			"/data/data/com.termux/files/usr/bin/chromium-browser",
		},
		"openbsd": {
			"chrome",
			"chromium",
		},
		"windows": append([]string{"chrome", "edge"}, expandWindowsExePaths(
			`Google\Chrome\Application\chrome.exe`,
			`Chromium\Application\chrome.exe`,
			`Microsoft\Edge\Application\msedge.exe`,
		)...),
	}[runtime.GOOS]

	for _, path := range list {
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

	if bin, has := LookPath(); has {
		p := openExec(bin, url)
		_ = p.Start()
		_ = p.Process.Release()
	}
}

func expandWindowsExePaths(list ...string) []string {
	newList := []string{}
	for _, p := range list {
		newList = append(
			newList,
			filepath.Join(os.Getenv("ProgramFiles"), p),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), p),
			filepath.Join(os.Getenv("LocalAppData"), p),
		)
	}

	return newList
}

// cleanZipFileName cleans the zip file name trimming traversal sequences and removing
// the specified number of leading path segments.
func cleanZipFileName(fname string, depth int) (string, error) {
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
	return filepath.Join(bits[depth:]...), nil
}
