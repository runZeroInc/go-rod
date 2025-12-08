// Package main ...
package main

import (
	"fmt"

	"github.com/runZeroInc/go-rod/lib/launcher"
	"github.com/runZeroInc/go-rod/lib/utils"
)

func main() {
	b, err := launcher.NewBrowser()
	utils.E(err)

	p, err := b.Get()
	utils.E(err)

	fmt.Println(p)
}
