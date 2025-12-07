// Package main ...
package main

import (
	"fmt"

	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/utils"
)

func main() {
	p, err := launcher.NewBrowser().Get()
	utils.E(err)

	fmt.Println(p)
}
