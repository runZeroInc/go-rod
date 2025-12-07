package launcher_test

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/launcher/flags"
	"github.com/runZeroInc/go-rod/pkg/got"
)

var setup = got.Setup(nil)

func TestLaunchUserMode(t *testing.T) {
	g := setup(t)

	l := launcher.NewUserMode()
	defer l.Kill()

	l.Kill() // empty kill should do nothing

	has := l.Has("not-exists")
	g.False(has)

	l.Append("test-append", "a")
	f := l.Get("test-append")
	g.Eq("a", f)

	dir := l.Get(flags.UserDataDir)
	port := 58472

	l = l.Context(g.Context()).Delete("test").Bin("").
		Revision(launcher.RevisionDefault).
		Logger(io.Discard).
		Leakless(false).Leakless(true).
		HeadlessNew(true).HeadlessNew(false).
		Headless(false).Headless(true).RemoteDebuggingPort(port).
		NoSandbox(true).NoSandbox(false).
		Devtools(true).Devtools(false).
		StartURL("about:blank").
		Proxy("test.com").
		UserDataDir("test").UserDataDir(dir).
		WorkingDir("").
		Env(append(os.Environ(), "TZ=Asia/Tokyo")...)

	g.Eq(l.FormatArgs(), []string /* len=6 cap=8 */ {
		"--headless",
		`--no-startup-window`,           /* len=19 */
		`--proxy-server=test.com`,       /* len=23 */
		`--remote-debugging-port=58472`, /* len=29 */
		"--test-append=a",
		"about:blank",
	})

	url := l.MustLaunch()

	g.Eq(url, launcher.NewUserMode().RemoteDebuggingPort(port).MustLaunch())
}

func TestUserModeErr(t *testing.T) {
	g := setup(t)

	_, err := launcher.NewUserMode().RemoteDebuggingPort(48277).Bin("not-exists").Launch()
	g.Err(err)

	_, err = launcher.NewUserMode().RemoteDebuggingPort(58217).Bin("echo").Launch()
	g.Err(err)
}

func TestAppMode(t *testing.T) {
	g := setup(t)

	l := launcher.NewAppMode("http://example.com")

	g.Eq(l.Get(flags.App), "http://example.com")
}

func TestGetWebSocketDebuggerURLErr(t *testing.T) {
	g := setup(t)

	_, err := launcher.ResolveURL("1://")
	g.Err(err)
}

func TestLaunchErr(t *testing.T) {
	g := setup(t)

	g.Panic(func() {
		launcher.New().Bin("not-exists").MustLaunch()
	})
	g.Panic(func() {
		launcher.New().Headless(false).Bin("not-exists").MustLaunch()
	})
	{
		l := launcher.New().XVFB()
		_, _ = l.Launch()
		l.Kill()
	}
}

var testProfileDir = flag.Bool("test-profile-dir", false, "set it to test profile dir")

func TestProfileDir(t *testing.T) {
	g := setup(t)

	url := launcher.New().Headless(false).
		ProfileDir("").ProfileDir("test-profile-dir")

	if !*testProfileDir {
		g.Skip("It's not CI friendly, so we skip it!")
	}

	url.MustLaunch()

	userDataDir := url.Get(flags.UserDataDir)
	file, err := os.Stat(filepath.Join(userDataDir, "test-profile-dir"))

	g.E(err)
	g.True(file.IsDir())
}

func TestIgnoreCerts(t *testing.T) {
	g := setup(t)

	// https://travistidwell.com/jsencrypt/demo/
	testData := []string{
		`-----BEGIN PUBLIC KEY-----
MIGeMA0GCSqGSIb3DQEBAQUAA4GMADCBiAKBgF9pr2zok5bivQIEUN7Y58a9uB1o
sroMt3hxNfzOh/G+sXgYPPoEl2/Ys/2zbvym7Ze0eGbb6FrV8aueg89TPTNWAKlN
N49q6S3zLG1WmI2rVYz4LtPgpg1YR9FQRIg4Ll0C02daufXgvUBGjIARH19FTw6P
61kEhnEQxUHhdAqbAgMBAAE=
-----END PUBLIC KEY-----
		`,
		`-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQCvBTz/TOYc66qB97OyYenSHk4T
hAUKX5RUWZ/80o0zyJoo1dfrrwW9PlT5o4DlGMs0NSbtJ8RMQRTLZwL/zxXjiEMv
dKFs2OrefYKANTc0e2XAtQAm3Is5Ro8AF1S4Fk+eZXr2yZtBRKXvhJ/A2bilVoSn
fmQnyBe7dVU43NXfrQIDAQAB
-----END PUBLIC KEY-----
		`,
	}

	keys := make([]crypto.PublicKey, 0, len(testData))

	for _, pubPEM := range testData {
		block, _ := pem.Decode([]byte(pubPEM))
		if block == nil {
			g.Fatal("failed to parse PEM block containing the public key")
			return // no-op because g.Fatal calls t.FailNow() but `staticcheck` doesn't know it
		}

		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			g.Fatalf("failed to parse DER encoded public key: " + err.Error())
		}

		keys = append(keys, pub)
	}

	l := launcher.New()

	err := l.IgnoreCerts(keys)
	if err != nil {
		g.Fatalf("IgnoreCerts: %s", err)
	}

	expected := "--ignore-certificate-errors-spki-list=" + strings.Join([]string{
		"+ZqfrXb+V/36nZecO59bghHlNhiHTzImjYLnNWGUd1I=",
		"llpTCSqZ2/IKsMg4tz+o1mCkXIOdKcM6sKu9kC6o7S4=",
	}, ",")

	g.Has(l.FormatArgs(), expected)
}

func TestIgnoreCerts_InvalidCert(t *testing.T) {
	g := setup(t)

	l := launcher.New()

	err := l.IgnoreCerts([]crypto.PublicKey{nil})
	if err == nil {
		g.Fatalf("IgnoreCerts: %s", err)
	}
}

func TestLaunchMultiTimes(t *testing.T) {
	g := setup(t)

	// first time launch, success.
	l := launcher.New()
	u, e := l.Launch()
	g.Neq(u, "")
	g.E(e)

	// second time launch, failed with ErrAlreadyLaunched.
	_, e = l.Launch()
	g.Eq(e, launcher.ErrAlreadyLaunched)
}

func Test_ResolveDownloadURL(t *testing.T) {
	type srcPlatform struct {
		os   string
		arch string
	}
	tests := []struct {
		name      string
		platform  srcPlatform
		wantMatch *regexp.Regexp
	}{
		{
			name: "linux-arm64",
			platform: srcPlatform{
				os:   "linux",
				arch: "arm64",
			},
			wantMatch: regexp.MustCompile(`https://playwright.azureedge.net/builds/chromium/\d+/chromium-linux-arm64.zip`),
		},
		{
			name: "linux-x64",
			platform: srcPlatform{
				os:   "linux",
				arch: "amd64",
			},
			wantMatch: regexp.MustCompile(`https://storage.googleapis.com/chrome-for-testing-public/[\d.]+/linux64/chrome-linux64.zip`),
		},
		{
			name: "windows-x64",
			platform: srcPlatform{
				os:   "windows",
				arch: "amd64",
			},
			wantMatch: regexp.MustCompile(`https://storage.googleapis.com/chrome-for-testing-public/[\d.]+/win64/chrome-win64.zip`),
		},
		{
			name: "windows-x86",
			platform: srcPlatform{
				os:   "windows",
				arch: "386",
			},
			wantMatch: regexp.MustCompile(`https://storage.googleapis.com/chrome-for-testing-public/[\d.]+/win32/chrome-win32.zip`),
		},
		{
			name: "macos-x64",
			platform: srcPlatform{
				os:   "darwin",
				arch: "amd64",
			},
			wantMatch: regexp.MustCompile(`https://storage.googleapis.com/chrome-for-testing-public/[\d.]+/mac-x64/chrome-mac-x64.zip`),
		},
		{
			name: "macos-arm64",
			platform: srcPlatform{
				os:   "darwin",
				arch: "arm64",
			},
			wantMatch: regexp.MustCompile(`https://storage.googleapis.com/chrome-for-testing-public/[\d.]+/mac-arm64/chrome-mac-arm64.zip`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, rev, err := launcher.ResolveLatestDownloadURL(tt.platform.os, tt.platform.arch)
			if err != nil {
				t.Fatalf("ResolveLatestDownloadURL() error = %v", err)
			}
			if !tt.wantMatch.MatchString(got) {
				t.Errorf("ResolveLatestDownloadURL() = %v, didn't match %v", got, tt.wantMatch)
			}
			v, err := strconv.Atoi(rev)
			if err != nil {
				t.Fatalf("ResolveLatestDownloadURL() revision parse error = %v", err)
			}
			if v <= 0 {
				t.Errorf("ResolveLatestDownloadURL() revision = %v, want a positive integer", rev)
			}
		})
	}
}

func Test_ResolveDownloader(t *testing.T) {
	b, err := launcher.NewBrowser(runtime.GOOS, runtime.GOARCH)
	t.Errorf("browser: %+v", b)
	if err != nil {
		t.Fatalf("NewBrowser() error = %v", err)
	}
	err = b.Download()
	if err != nil {
		t.Fatalf("Browser.Download() error = %v", err)
	}
}
