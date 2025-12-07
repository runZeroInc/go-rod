package launcher

import (
	"net/url"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/runZeroInc/go-rod/lib/defaults"
	"github.com/runZeroInc/go-rod/lib/launcher/flags"
	"github.com/runZeroInc/go-rod/lib/utils"
	"github.com/runZeroInc/go-rod/pkg/got"
)

var setup = got.Setup(nil)

func TestToHTTP(t *testing.T) {
	g := setup(t)

	u, _ := url.Parse("wss://a.com")
	g.Eq("https", toHTTP(*u).Scheme)

	u, _ = url.Parse("ws://a.com")
	g.Eq("http", toHTTP(*u).Scheme)
}

func TestToWS(t *testing.T) {
	g := setup(t)

	u, _ := url.Parse("https://a.com")
	g.Eq("wss", toWS(*u).Scheme)

	u, _ = url.Parse("http://a.com")
	g.Eq("ws", toWS(*u).Scheme)
}

func TestLaunchOptions(t *testing.T) {
	g := setup(t)

	defaults.Show = true
	defaults.Devtools = true
	inContainer = true

	// restore
	defer func() {
		defaults.ResetWith("")
		inContainer = utils.InContainer
	}()

	l := New()

	g.False(l.Has(flags.Headless))

	g.True(l.Has(flags.NoSandbox))

	g.True(l.Has("auto-open-devtools-for-tabs"))
}

func TestGetURLErr(t *testing.T) {
	g := setup(t)

	l := New()

	l.ctxCancel()
	_, err := l.getURL()
	g.Err(err)

	l = New()
	l.parser.lock.Lock()
	l.parser.Buffer = "err"
	l.parser.lock.Unlock()
	close(l.exit)
	_, err = l.getURL()
	g.Eq("[launcher] Failed to get the debug url: err", err.Error())
}

func TestURLParserErr(t *testing.T) {
	g := setup(t)

	u := &URLParser{
		Buffer: "error",
		lock:   &sync.Mutex{},
	}

	g.Eq(u.Err().Error(), "[launcher] Failed to get the debug url: error")

	u.Buffer = "/tmp/rod/chromium-818858/chrome: error while loading shared libraries: libgobject-2.0.so.0: cannot open shared object file: No such file or directory"
	g.Eq(u.Err().Error(), "[launcher] Failed to launch the browser, the doc might help https://go-rod.github.io/#/compatibility?id=os: /tmp/rod/chromium-818858/chrome: error while loading shared libraries: libgobject-2.0.so.0: cannot open shared object file: No such file or directory")
}

func TestTestOpen(_ *testing.T) {
	openExec = func(_ string, _ ...string) *exec.Cmd {
		cmd := exec.Command("not-exists")
		cmd.Process = &os.Process{}
		return cmd
	}
	defer func() { openExec = exec.Command }()

	Open("about:blank")
}
