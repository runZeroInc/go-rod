package rod

import (
	"context"
	"sync"
	"testing"
)

// TestPageContextRebindsHelpers verifies that Page.Context(ctx) returns a
// clone whose Mouse, Keyboard, and Touch helpers route their CDP calls
// through the clone (and therefore use the new ctx) rather than continuing
// to point at the original Page (which would use its original, typically
// untimeout'd context).
//
// Regression test for a deadlock seen in production where page.Mouse.MoveTo
// on a page returned by Page.Context(timeoutCtx) ignored the timeout because
// Mouse.page still pointed at the parent Page; a stuck CDP request on a
// hung renderer blocked the screenshot worker indefinitely.
func TestPageContextRebindsHelpers(t *testing.T) {
	parentCtx := context.Background()
	parent := &Page{
		ctx:         parentCtx,
		helpersLock: &sync.Mutex{},
	}
	parent.newMouse().newKeyboard().newTouch()

	if parent.Mouse.page != parent {
		t.Fatalf("parent.Mouse.page = %p, want %p", parent.Mouse.page, parent)
	}
	if parent.Keyboard.page != parent {
		t.Fatalf("parent.Keyboard.page = %p, want %p", parent.Keyboard.page, parent)
	}
	if parent.Touch.page != parent {
		t.Fatalf("parent.Touch.page = %p, want %p", parent.Touch.page, parent)
	}

	childCtx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	child := parent.Context(childCtx)

	if child == parent {
		t.Fatal("Page.Context returned the same pointer; expected a clone")
	}
	if child.GetContext() != childCtx {
		t.Fatalf("child.ctx = %v, want childCtx", child.GetContext())
	}

	if child.Mouse == nil || child.Mouse == parent.Mouse {
		t.Fatalf("child.Mouse not rebound: parent=%p child=%p", parent.Mouse, child.Mouse)
	}
	if child.Mouse.page != child {
		t.Fatalf("child.Mouse.page = %p, want clone %p", child.Mouse.page, child)
	}
	if child.Mouse.page.GetContext() != childCtx {
		t.Fatal("child.Mouse.page.ctx does not match clone ctx; CDP calls would use wrong ctx")
	}

	if child.Keyboard == nil || child.Keyboard == parent.Keyboard {
		t.Fatalf("child.Keyboard not rebound: parent=%p child=%p", parent.Keyboard, child.Keyboard)
	}
	if child.Keyboard.page != child {
		t.Fatalf("child.Keyboard.page = %p, want clone %p", child.Keyboard.page, child)
	}

	if child.Touch == nil || child.Touch == parent.Touch {
		t.Fatalf("child.Touch not rebound: parent=%p child=%p", parent.Touch, child.Touch)
	}
	if child.Touch.page != child {
		t.Fatalf("child.Touch.page = %p, want clone %p", child.Touch.page, child)
	}

	// Parent must remain untouched so existing references continue to work.
	if parent.Mouse.page != parent {
		t.Fatalf("parent.Mouse.page mutated to %p, want %p", parent.Mouse.page, parent)
	}
	if parent.GetContext() != parentCtx {
		t.Fatal("parent.ctx mutated by Context() call")
	}
}

// TestPageContextNilHelpers ensures Context() does not panic when input
// helpers were never initialized (e.g. a Page constructed without going
// through the standard newPage path).
func TestPageContextNilHelpers(t *testing.T) {
	parent := &Page{
		ctx:         context.Background(),
		helpersLock: &sync.Mutex{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	child := parent.Context(ctx)
	if child.Mouse != nil || child.Keyboard != nil || child.Touch != nil {
		t.Fatal("Context() created helpers that did not exist on the parent")
	}
}
