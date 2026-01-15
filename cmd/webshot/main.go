package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/runZeroInc/go-rod/cmd/webshot/chrome"
	"github.com/sirupsen/logrus"
)

func main() {
	lgr := logrus.New()
	lgr.SetFormatter(&logrus.TextFormatter{})
	lgr.SetLevel(logrus.DebugLevel)

	if len(os.Args) < 2 {
		lgr.Fatalf("usage: go run main.go [urls....]")
	}

	urls := os.Args[1:]

	opts := chrome.GetDefaultOptions(lgr.WithField("source", "webshot"))
	w := chrome.NewWebshot(opts...)

	defer w.Cleanup()

	if err := w.Init(); err != nil {
		logrus.Fatalf("failed to init webshot: %s", err)
	}

	logrus.Printf("using %s at path %s", w.LaunchedVersion, w.LaunchedChromiumPath)

	for _, url := range urls {
		stime := time.Now()
		res, err := w.Screenshot(url)
		if err != nil {
			logrus.Errorf("screenshot of %s failed after %s: %s", url, time.Since(stime), err)
			continue
		}
		raw := res.Image
		if len(raw) == 0 {
			logrus.Errorf("screenshot of %s returned empty image: %v", url, res.Error)
			continue
		}
		logrus.Printf("wrote image of %s to %s (%d bytes) in %s", url, "output.png", len(raw), time.Since(stime))
		logrus.Printf("res: %#v", res.CaptureMeta)
		if jv, ok := res.DOM["globals"]; ok {
			names := []string{}
			if err := json.Unmarshal([]byte(jv), &names); err == nil {
				logrus.Printf("globals: %+v", names)
			}
		}

		if err := writeFileChmod("output.png", raw, 0o644); err != nil {
			logrus.Errorf("failed to write output.png: %s", err)
		}
		logrus.Printf("stats: %s", w.GetStatsString())
	}
}

// writeFileChmod writes a file to disk with the specified permissions, ignoring umask
func writeFileChmod(name string, data []byte, perm os.FileMode) error {
	if err := os.WriteFile(name, data, perm); err != nil {
		return err
	}
	return os.Chmod(name, perm)
}
