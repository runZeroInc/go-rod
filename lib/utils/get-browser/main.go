// Package main ...
package main

import (
	"fmt"
	"runtime"

	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/utils"
)

func main() {
	b, err := launcher.NewBrowser(runtime.GOOS, runtime.GOARCH)
	utils.E(err)

	p, err := b.Get()
	utils.E(err)

	fmt.Println(p)
}
