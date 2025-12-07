package input_test

import (
	"testing"

	"github.com/runZeroInc/go-rod/lib/input"
	"github.com/runZeroInc/go-rod/lib/proto"
	"github.com/runZeroInc/go-rod/pkg/got"
)

func TestMouseEncode(t *testing.T) {
	g := got.T(t)

	b, flag := input.EncodeMouseButton([]proto.InputMouseButton{proto.InputMouseButtonLeft})

	g.Eq(b, proto.InputMouseButtonLeft)
	g.Eq(flag, 1)
}
