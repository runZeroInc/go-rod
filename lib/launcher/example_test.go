package launcher_test

import (
	"os/exec"

	"github.com/runZeroInc/go-rod"
	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/utils"
	"github.com/sirupsen/logrus"
)

func Example_use_system_browser() {
	if path, exists := launcher.LookPath(); exists {
		u := launcher.NewMust().Bin(path).MustLaunch()
		rod.New().ControlURL(u).MustConnect()
	}
}

func Example_print_browser_CLI_output() {
	// Pipe the browser stderr and stdout to os.Stdout .
	u := launcher.NewMust().Logger(logrus.New().WithField("source", "test")).MustLaunch()
	rod.New().ControlURL(u).MustConnect()
}

func Example_custom_launch() {
	// get the browser executable path
	b, err := launcher.NewBrowser()
	utils.E(err)
	path := b.MustGet()

	// use the FormatArgs to construct args, this line is optional, you can construct the args manually
	l, err := launcher.New()
	if err != nil {
		panic(err)
	}
	args, err := l.FormatArgs()
	if err != nil {
		panic(err)
	}

	cmd := exec.Command(path, args...) //nolint:gosec
	parser := launcher.NewChromiumOutputParser()
	cmd.Stderr = parser
	utils.E(cmd.Start())
	u := launcher.MustResolveURL(<-parser.URL)

	rod.New().ControlURL(u).MustConnect()
}
