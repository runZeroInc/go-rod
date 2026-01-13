package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/runZeroInc/go-rod/lib/utils"
	"github.com/runZeroInc/go-rod/pkg/gson"
)

var _ io.Writer = &ChromiumOutputParser{}

// ChromiumOutputParser to get control url from stderr.
type ChromiumOutputParser struct {
	URL            chan string
	SandboxWarning chan string
	Buffer         string // buffer for the browser stdout

	lock *sync.Mutex
	ctx  context.Context
	done bool
}

// NewChromiumOutputParser instance.
func NewChromiumOutputParser() *ChromiumOutputParser {
	return &ChromiumOutputParser{
		URL:  make(chan string),
		lock: &sync.Mutex{},
		ctx:  context.Background(),
	}
}

var (
	regWS      = regexp.MustCompile(`ws://.+/`)
	regSignal  = regexp.MustCompile(`^Received signal (.+)`)
	regSandbox = regexp.MustCompile(`--no-sandbox|If you want to live dangerously|No usable sandbox`)
)

// Context sets the context.
func (r *ChromiumOutputParser) Context(ctx context.Context) *ChromiumOutputParser {
	r.ctx = ctx
	return r
}

// Write interface.
func (r *ChromiumOutputParser) Write(p []byte) (n int, err error) {
	r.lock.Lock()
	defer r.lock.Unlock()

	if !r.done {
		r.Buffer += string(p)

		// Search for the websocket URL and return it to the channel
		str := regWS.FindString(r.Buffer)
		if str != "" {
			u, err := url.Parse(strings.TrimSpace(str))
			if err == nil {
				select {
				case <-r.ctx.Done():
				case r.URL <- "http://" + u.Host:
				}
			}
			r.done = true
			r.Buffer = ""
		}

		// Search for sandbox warnings
		if str := regSandbox.FindString(r.Buffer); str != "" {
			select {
			case <-r.ctx.Done():
			case r.SandboxWarning <- r.Buffer:
			}
			r.Buffer = ""
		}

		// Search for `Received signal` messages indicating a crash or self-termination
		if str := regSignal.FindString(r.Buffer); str != "" {
			r.done = true
			r.Buffer = ""
		}

		if len(r.Buffer) > 4096 {
			r.Buffer = r.Buffer[128:]
		}
	}

	return len(p), nil
}

// Err returns the common error parsed from stdout and stderr.
func (r *ChromiumOutputParser) Err() error {
	r.lock.Lock()
	defer r.lock.Unlock()

	msg := "[launcher] Failed to get the debug url: "

	if strings.Contains(r.Buffer, "error while loading shared libraries") {
		msg = "[launcher] Failed to launch the browser, the doc might help https://go-rod.github.io/#/compatibility?id=os: "
	}

	return errors.New(msg + r.Buffer)
}

// MustResolveURL is similar to ResolveURL.
func MustResolveURL(u string) string {
	u, err := ResolveURL(u)
	utils.E(err)
	return u
}

var (
	regPort     = regexp.MustCompile(`^\:?(\d+)$`)
	regProtocol = regexp.MustCompile(`^\w+://`)
)

// ResolveURL by requesting the u, it will try best to normalize the u.
// The format of u can be "9222", ":9222", "host:9222", "ws://host:9222", "wss://host:9222",
// "https://host:9222" "http://host:9222". The return string will look like:
// "ws://host:9222/devtools/browser/4371405f-84df-4ad6-9e0f-eab81f7521cc"
func ResolveURL(u string) (string, error) {
	if u == "" {
		u = "9222"
	}

	u = strings.TrimSpace(u)
	u = regPort.ReplaceAllString(u, "127.0.0.1:$1")

	if !regProtocol.MatchString(u) {
		u = "http://" + u
	}

	parsed, err := url.Parse(u)
	if err != nil {
		return "", err
	}

	parsed = toHTTP(*parsed)
	parsed.Path = "/json/version"

	req, err := http.NewRequest("GET", parsed.String(), nil)
	if err != nil {
		return "", fmt.Errorf("resolve url failed to create request for %s: %w", parsed.String(), err)
	}

	ctx, cancel := context.WithTimeout(req.Context(), time.Minute)
	defer cancel()

	req = req.WithContext(ctx)
	client := http.DefaultClient
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve url failed to request %s: %w", parsed.String(), err)
	}

	data, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024))
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()

	if err != nil {
		return "", fmt.Errorf("resolve url failed to read response from %s: %w", parsed.String(), err)
	}

	wsURL := gson.New(data).Get("webSocketDebuggerUrl").Str()

	parsedWS, err := url.Parse(wsURL)
	if err != nil {
		return "", fmt.Errorf("resolve url failed to parse websocket url %s: %w", wsURL, err)
	}

	parsedWS.Host = parsed.Host

	return parsedWS.String(), nil
}
