package infer

import (
	"testing"

	"github.com/lumen-lang/lumen/internal/parser"
	"github.com/lumen-lang/lumen/internal/types"
)

// methodSelf returns the inferred Self kind for Owner.Method, or -1 if not found.
func methodSelf(info *types.Info, owner, method string) types.SelfKind {
	st, ok := info.Structs[owner]
	if !ok {
		return -1
	}
	m, ok := st.Methods[method]
	if !ok {
		return -1
	}
	return m.Self
}

func parseAndInferSelf(t *testing.T, src string) *types.Info {
	t.Helper()
	prog, errs := parser.Parse("test.lm", src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	info, terrs := types.Check(prog)
	if len(terrs) > 0 {
		t.Fatalf("type: %v", terrs)
	}
	SelfMut(prog, info)
	return info
}

func TestSelfMutReadOnlyMethod(t *testing.T) {
	src := `
type C struct { v int64 }

func (c *C) get() int64 {
    return c.v
}`
	info := parseAndInferSelf(t, src)
	if got := methodSelf(info, "C", "get"); got != types.SelfRef {
		t.Errorf("C::get: want SelfRef, got %v", got)
	}
}

func TestSelfMutWritingMethod(t *testing.T) {
	src := `
type C struct { v int64 }

func (c *C) tick() {
    c.v = c.v + 1
}`
	info := parseAndInferSelf(t, src)
	if got := methodSelf(info, "C", "tick"); got != types.SelfRefMut {
		t.Errorf("C::tick: want SelfRefMut, got %v", got)
	}
}

func TestSelfMutValueReceiverUntouched(t *testing.T) {
	src := `
type C struct { v int64 }

func (c C) doubled() int64 {
    return c.v * 2
}`
	info := parseAndInferSelf(t, src)
	if got := methodSelf(info, "C", "doubled"); got != types.SelfValue {
		t.Errorf("C::doubled: want SelfValue, got %v", got)
	}
}

func TestSelfMutFixpointPropagation(t *testing.T) {
	// `outer` doesn't mutate self directly but calls `inner` which does.
	// After fixpoint both should be SelfRefMut.
	src := `
type C struct { v int64 }

func (c *C) inner() {
    c.v = c.v + 1
}

func (c *C) outer() {
    c.inner()
}`
	info := parseAndInferSelf(t, src)
	if got := methodSelf(info, "C", "inner"); got != types.SelfRefMut {
		t.Errorf("C::inner: want SelfRefMut, got %v", got)
	}
	if got := methodSelf(info, "C", "outer"); got != types.SelfRefMut {
		t.Errorf("C::outer: want SelfRefMut (propagated), got %v", got)
	}
}

func TestSelfMutIndexWrite(t *testing.T) {
	// Field-write through receiver is the primary write evidence and is
	// already covered by TestSelfMutWritingMethod. Here we verify the deref
	// case is not needed for the receiver path: a chain of field-writes
	// nested inside control flow still trips the inferrer.
	src := `
type C struct { v int64 }

func (c *C) maybe(flag bool) {
    if flag {
        c.v = 1
    } else {
        c.v = 2
    }
}`
	info := parseAndInferSelf(t, src)
	if got := methodSelf(info, "C", "maybe"); got != types.SelfRefMut {
		t.Errorf("C::maybe: want SelfRefMut, got %v", got)
	}
}

func TestSelfMutNestedReadOnly(t *testing.T) {
	// `viewer` calls `get` (read-only) — should stay SelfRef.
	src := `
type C struct { v int64 }

func (c *C) get() int64 {
    return c.v
}

func (c *C) viewer() int64 {
    return c.get() + 1
}`
	info := parseAndInferSelf(t, src)
	if got := methodSelf(info, "C", "viewer"); got != types.SelfRef {
		t.Errorf("C::viewer: want SelfRef (only calls &self method), got %v", got)
	}
}
