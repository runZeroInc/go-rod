package rod_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runZeroInc/go-rod"
	"github.com/runZeroInc/go-rod/lib/cdp"
	"github.com/runZeroInc/go-rod/lib/defaults"
	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/proto"
	"github.com/runZeroInc/go-rod/lib/utils"
	"github.com/runZeroInc/go-rod/pkg/got"
	"github.com/runZeroInc/go-rod/pkg/gson"
	"github.com/sirupsen/logrus"
)

var TimeoutEach = flag.Duration("timeout-each", time.Minute, "timeout for each test")

var LogDir = slash(fmt.Sprintf("tmp/cdp-log/%s", time.Now().Format("2006-01-02_15-04-05")))

func init() {
	got.DefaultFlags("timeout=5m", "run=/")

	utils.E(os.MkdirAll(slash("tmp/cdp-log"), 0o755))

	b, err := launcher.NewBrowser()
	utils.E(err)
	b.MustGet()
}

var testerPool rod.Pool[G]

func TestMain(m *testing.M) {
	testerPool = newTesterPool()

	code := m.Run()
	if code != 0 {
		os.Exit(code)
	}

	testerPool.Cleanup(func(g *G) {
		g.browser.MustClose()
	})
}

// G is a tester. Testers are thread-safe, they shouldn't race each other.
type G struct {
	got.G

	// mock client for proxy the cdp requests
	mc *MockClient

	// a random browser instance from the pool. If you have changed state of it, you must reset it
	// or it may affect other test cases.
	browser *rod.Browser

	// a random page instance from the pool. If you have changed state of it, you must reset it
	// or it may affect other test cases.
	page *rod.Page

	// use it to cancel the TimeoutEach for current test case
	cancelTimeout func()
}

// If we don't use pool to cache, the total time will be much longer.
func newTesterPool() rod.Pool[G] {
	parallel := got.Parallel()
	if parallel == 0 {
		parallel = runtime.GOMAXPROCS(0)
	}

	fmt.Println("parallel test", parallel) //nolint: forbidigo

	return rod.NewPool[G](parallel)
}

func newTester() *G {
	u := launcher.NewMust().Set("proxy-bypass-list", "<-loopback>").NoSandbox(true).MustLaunch()

	mc := newMockClient(u)

	browser := rod.New().Client(mc).MustConnect().MustIgnoreCertErrors(false)

	pages := browser.MustPages()

	var page *rod.Page
	if pages.Empty() {
		page = browser.MustPage()
	} else {
		page = pages.First()
	}

	return &G{
		mc:      mc,
		browser: browser,
		page:    page,
	}
}

func setup(t *testing.T) G {
	t.Helper()

	if got.Parallel() != 1 {
		t.Parallel()
	}

	tester := testerPool.MustGet(newTester)
	t.Cleanup(func() { testerPool.Put(tester) })

	tester.G = got.New(t)
	tester.mc.t = t
	tester.mc.log.SetOutput(tester.Open(true, filepath.Join(LogDir, tester.mc.id, t.Name()+".log")))
	tester.page.MustNavigate("")

	return *tester
}

func (g G) enableCDPLog() {
	g.mc.principal.Logger(rod.DefaultLogger)
}

func (g G) dump(args ...interface{}) {
	g.Log(utils.Dump(args...))
}

func (g G) blank() string {
	return g.srcFile("./fixtures/blank.html")
}

func (g G) html(content string) string {
	return g.Serve().Route("/", "", content).URL()
}

// Get abs file path from fixtures folder, such as "file:///a/b/click.html".
// Usually the path can be used for html src attribute like:
//
//	<img src="file:///a/b">
func (g G) srcFile(path string) string {
	g.Helper()
	f, err := filepath.Abs(slash(path))
	g.E(err)
	return "file://" + f
}

func (g G) newPage(u ...string) *rod.Page {
	g.Helper()
	p := g.browser.MustPage(u...)
	g.Cleanup(func() {
		if !g.Failed() {
			p.MustClose()
		}
	})
	return p
}

type Call func(ctx context.Context, sessionID, method string, params interface{}) ([]byte, error)

var _ rod.CDPClient = &MockClient{}

type MockClient struct {
	sync.RWMutex
	id        string
	t         got.Testable
	log       *logrus.Logger
	principal *cdp.Client
	call      Call
	event     <-chan *cdp.Event
}

var mockClientCount int32

func newMockClient(u string) *MockClient {
	id := fmt.Sprintf("%02d", atomic.AddInt32(&mockClientCount, 1))

	// create init log file
	utils.E(os.MkdirAll(filepath.Join(LogDir, id), 0o755))
	f, err := os.Create(filepath.Join(LogDir, id, "_.log"))
	log := logrus.New()
	log.Out = f
	utils.E(err)

	client := cdp.New().Logger(utils.MultiLogger(defaults.CDP, log)).Start(cdp.MustConnectWS(u))

	return &MockClient{id: id, principal: client, log: log}
}

func (mc *MockClient) Event() <-chan *cdp.Event {
	if mc.event != nil {
		return mc.event
	}
	return mc.principal.Event()
}

func (mc *MockClient) Call(ctx context.Context, sessionID, method string, params interface{}) ([]byte, error) {
	return mc.getCall()(ctx, sessionID, method, params)
}

func (mc *MockClient) getCall() Call {
	mc.RLock()
	defer mc.RUnlock()

	if mc.call == nil {
		return mc.principal.Call
	}
	return mc.call
}

func (mc *MockClient) setCall(fn Call) {
	mc.Lock()
	defer mc.Unlock()

	if mc.call != nil {
		mc.t.Logf("leaking MockClient.stub")
		mc.t.Fail()
	}
	mc.call = fn
}

func (mc *MockClient) resetCall() {
	mc.Lock()
	defer mc.Unlock()
	mc.call = nil
}

// Use it to find out which cdp call to intercept. Put a print like log.Println("*****") after the cdp call you want to intercept.
// The output of the test should has something like:
//
//	[stubCounter] begin
//	[stubCounter] 1, proto.DOMResolveNode{}
//	[stubCounter] 1, proto.RuntimeCallFunctionOn{}
//	[stubCounter] 2, proto.RuntimeCallFunctionOn{}
//	01:49:43 *****
//
// So the 3rd call is the one we want to intercept, then you can use the output with s.at or s.errorAt.
func (mc *MockClient) stubCounter() {
	l := sync.Mutex{}
	mCount := map[string]int{}

	fmt.Fprintln(os.Stdout, "[stubCounter] begin")

	mc.setCall(func(ctx context.Context, sessionID, method string, params interface{}) ([]byte, error) {
		l.Lock()
		mCount[method]++
		m := fmt.Sprintf("%d, proto.%s{}", mCount[method], proto.GetType(method).Name())
		_, _ = fmt.Fprintln(os.Stdout, "[stubCounter]", m)
		l.Unlock()

		return mc.principal.Call(ctx, sessionID, method, params)
	})
}

type StubSend func() (gson.JSON, error)

// When call the cdp.Client.Call the nth time use fn instead.
// Use p to filter method.
func (mc *MockClient) stub(nth int, p proto.Request, fn func(send StubSend) (gson.JSON, error)) {
	if p == nil {
		mc.t.Logf("p must be specified")
		mc.t.FailNow()
	}

	count := int64(0)

	mc.setCall(func(ctx context.Context, sessionID, method string, params interface{}) ([]byte, error) {
		if method == p.ProtoReq() {
			if int(atomic.AddInt64(&count, 1)) == nth {
				mc.resetCall()
				j, err := fn(func() (gson.JSON, error) {
					b, err := mc.principal.Call(ctx, sessionID, method, params)
					return gson.New(b), err
				})
				if err != nil {
					return nil, err
				}
				return j.MarshalJSON()
			}
		}
		return mc.principal.Call(ctx, sessionID, method, params)
	})
}

// When call the cdp.Client.Call the nth time return error.
// Use p to filter method.
func (mc *MockClient) stubErr(nth int, p proto.Request) {
	mc.stub(nth, p, func(_ StubSend) (gson.JSON, error) {
		return gson.New(nil), errors.New("mock error")
	})
}

type MockRoundTripper struct {
	res *http.Response
	err error
}

func (mrt *MockRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return mrt.res, mrt.err
}

type MockReader struct {
	err error
}

func (mr *MockReader) Read(_ []byte) (n int, err error) {
	return 0, mr.err
}

func TestLintIgnore(t *testing.T) {
	t.Skip()

	_ = rod.Try(func() {
		tt := G{}
		tt.dump()
		tt.enableCDPLog()

		mc := &MockClient{}
		mc.stubCounter()
	})
}

var slash = filepath.FromSlash
