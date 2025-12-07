package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/runZeroInc/go-rod/lib/utils"
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

const ChromeForTestingLatestDownloadsURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"

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

const PlaywrightBrowserMetaURL = "https://raw.githubusercontent.com/microsoft/playwright/refs/heads/main/packages/playwright-core/browsers.json"
const PlaywrightLinuxArm64URL = "https://playwright.azureedge.net/builds/chromium/%s/chromium-linux-arm64.zip"

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
	Context context.Context

	// Operating system
	OS string

	// CPU architecture
	Arch string

	// RootDir to download different browser versions.
	RootDir string

	// Log to print output
	Logger utils.Logger

	// Revision
	Revision int

	// DownloadURL for the browser
	DownloadURL string

	// HTTPClient to download the browser
	HTTPClient *http.Client
}

// NewBrowser with default values.
func NewBrowser(srcOS, srcArch string) (*Browser, error) {
	b := &Browser{
		Context: context.Background(),
		RootDir: DefaultBrowserDir,
		Logger:  log.New(os.Stdout, "[launcher.Browser]", log.LstdFlags),
		OS:      srcOS,
		Arch:    srcArch,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Minute,
		},
		Revision: 0,
	}

	dpath, rev, err := ResolveLatestDownloadURL(b.OS, b.Arch)
	if err != nil {
		return nil, err
	}

	if dpath == "" {
		return nil, fmt.Errorf("unsupported platform")
	}
	b.DownloadURL = dpath

	b.Revision, err = strconv.Atoi(rev)
	if err != nil {
		return nil, fmt.Errorf("bad revision '%s': %w", rev, err)
	}

	return b, nil
}

// Dir to download the browser.
func (lc *Browser) DownloadDir() string {
	return filepath.Join(lc.RootDir, fmt.Sprintf("chromium-%d", lc.Revision))
}

var BinPaths = map[string]string{
	"darwin":  "Chromium.app/Contents/MacOS/Chromium",
	"linux":   "chrome",
	"windows": "chrome.exe",
}

// BinPath to download the browser executable.
func (lc *Browser) BinPath() string {
	bin, ok := BinPaths[lc.OS]
	if !ok {
		return ""
	}
	return filepath.Join(lc.DownloadDir(), filepath.FromSlash(bin))
}

// Resolve and download the correct URL for the platform
func (lc *Browser) Download() error {
	lc.Logger.Println(fmt.Sprintf("downloading chromium revision %d from %s to %s (%s-%s)", lc.Revision, lc.DownloadURL, lc.DownloadDir(), lc.OS, lc.Arch))
	if err := os.MkdirAll(lc.DownloadDir(), 0o755); err != nil {
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "go-rod-chromium-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	req, err := http.NewRequestWithContext(lc.Context, http.MethodGet, lc.DownloadURL, nil)
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
	lc.Logger.Println(fmt.Sprintf("downloaded %d bytes\n", n))
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
		// Trim the first part of the name from the path
		dname, trimmed, ok := strings.Cut(f.Name, "/")
		if ok {
			dname = trimmed
		}
		dname = filepath.Clean(dname)
		fpath := filepath.Join(lc.DownloadDir(), dname)

		lc.Logger.Println(fmt.Sprintf("extracting %s to %s\n", f.Name, fpath))

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

	lc.Logger.Println("extraction completed")

	return nil
}

// Get is a smart helper to get the browser executable path.
// If [Browser.BinPath] is not valid it will auto download the browser to [Browser.BinPath].
func (lc *Browser) Get() (string, error) {

	if lc.Validate() == nil {
		return lc.BinPath(), nil
	}

	return lc.BinPath(), lc.Download()
}

// MustGet is similar with Get.
func (lc *Browser) MustGet() string {
	p, err := lc.Get()
	utils.E(err)
	return p
}

// Validate returns nil if the browser executable is valid.
// If the executable is malformed it will return error.
func (lc *Browser) Validate() error {
	_, err := os.Stat(lc.BinPath())
	if err != nil {
		return err
	}

	cmd := exec.Command(lc.BinPath(), "--headless", "--no-sandbox",
		"--use-mock-keychain", "--disable-dev-shm-usage",
		"--disable-gpu", "--dump-dom", "about:blank")
	b, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(b), "error while loading shared libraries") {
			// When the os is missing some dependencies for chromium we treat it as valid binary.
			return nil
		}

		return fmt.Errorf("failed to run the browser: %w\n%s", err, b)
	}
	if !bytes.Contains(b, []byte(`<html><head></head><body></body></html>`)) {
		return errors.New("the browser executable doesn't support headless mode")
	}

	return nil
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
