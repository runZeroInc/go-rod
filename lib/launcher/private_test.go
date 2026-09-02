package launcher

import (
	"net/url"
	"os"
	"os/exec"
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

	l := NewMust()

	g.False(l.Has(flags.Headless))

	g.True(l.Has(flags.NoSandbox))

	g.True(l.Has("auto-open-devtools-for-tabs"))
}

// TestProxyOptions covers a fence rather than a convenience, so it asserts the flags reach the
// command line rather than that the fields were assigned.
func TestProxyOptions(t *testing.T) {
	g := setup(t)
	defer defaults.ResetWith("")

	// Explicit beats the process-global. A caller whose proxy IS the security boundary cannot have
	// it silently replaced by whatever ROD=proxy= happened to be set to.
	defaults.Proxy = "127.0.0.1:1"
	l := NewMust(WithProxy("127.0.0.1:8899"), WithProxyBypassList("<-loopback>"))
	g.Eq("127.0.0.1:8899", l.Get(flags.ProxyServer))
	g.Eq("<-loopback>", l.Get(flags.ProxyBypassList))

	// The global still applies when this launch names no proxy of its own, so existing callers
	// that rely on ROD=proxy= are unaffected.
	l = NewMust()
	g.Eq("127.0.0.1:1", l.Get(flags.ProxyServer))

	// And neither flag is set when nothing asks for one: an unconditional --proxy-bypass-list would
	// change the behaviour of every caller that has never heard of it.
	defaults.Proxy = ""
	l = NewMust()
	g.False(l.Has(flags.ProxyServer))
	g.False(l.Has(flags.ProxyBypassList))
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
