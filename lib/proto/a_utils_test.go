package proto_test

import (
	"testing"

	"github.com/runZeroInc/go-rod/lib/proto"
	"github.com/runZeroInc/go-rod/pkg/internal/got"
)

type T struct {
	got.G
}

func Test(t *testing.T) {
	got.Each(t, T{})
}

func (t T) PatternToReg() {
	t.Eq(``, proto.PatternToReg(""))
	t.Eq(`\A.*\z`, proto.PatternToReg("*"))
	t.Eq(`\A.\z`, proto.PatternToReg("?"))
	t.Eq(`\Aa\z`, proto.PatternToReg("a"))
	t.Eq(`\Aa.com/.*/test\z`, proto.PatternToReg("a.com/*/test"))
	t.Eq(`\A\?\*\z`, proto.PatternToReg(`\?\*`))
	t.Eq(`\Aa.com\?a=10&b=\*\z`, proto.PatternToReg(`a.com\?a=10&b=\*`))
}
