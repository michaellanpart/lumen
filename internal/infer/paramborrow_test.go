package infer

import (
	"testing"

	"github.com/lumen-lang/lumen/internal/types"
)

// Non-mutating value param of struct type → flagged as Borrow.
func TestParamBorrowStructReadOnly(t *testing.T) {
	src := `
type P struct { v int64 }
func get(p P) int64 { return p.v }
`
	_, fns := parseAndInferAll(t, src)
	get := fns["get"]
	if get == nil || len(get.Params) != 1 {
		t.Fatalf("expected fn get with 1 param, got %+v", get)
	}
	if !get.Params[0].Borrow {
		t.Fatalf("expected get(p P).Borrow = true, got false; Ty=%+v", get.Params[0].Ty)
	}
}

// Param of struct type that the body mutates → NOT flagged as Borrow.
func TestParamBorrowMutatedNotBorrowed(t *testing.T) {
	src := `
type P struct { v int64 }
func bump(p P) { p.v = p.v + 1 }
`
	_, fns := parseAndInferAll(t, src)
	bump := fns["bump"]
	if bump == nil || len(bump.Params) != 1 {
		t.Fatalf("expected fn bump with 1 param")
	}
	if bump.Params[0].Borrow {
		t.Fatalf("expected bump(p P).Borrow = false (body mutates p.v)")
	}
}

// Non-mutating value param of enum type → flagged as Borrow.
func TestParamBorrowEnumReadOnly(t *testing.T) {
	src := `
type E enum { None; Some(int64) }
func unbox(e E) int64 {
    switch e {
    case E::None: return 0
    case E::Some(x): return x
    }
}
`
	_, fns := parseAndInferAll(t, src)
	unbox := fns["unbox"]
	if unbox == nil || len(unbox.Params) != 1 {
		t.Fatalf("expected fn unbox with 1 param")
	}
	if !unbox.Params[0].Borrow {
		t.Fatalf("expected unbox(e E).Borrow = true")
	}
}

// Copy params (int64) are not affected by ParamBorrow.
func TestParamBorrowSkipsCopy(t *testing.T) {
	src := `
func id(x int64) int64 { return x }
`
	_, fns := parseAndInferAll(t, src)
	id := fns["id"]
	if id == nil || len(id.Params) != 1 {
		t.Fatalf("expected fn id with 1 param")
	}
	if id.Params[0].Borrow {
		t.Fatalf("expected id(x int64).Borrow = false (Copy param)")
	}
	if id.Params[0].Ty.Kind != types.KI64 {
		t.Fatalf("expected param Ty=int64, got %+v", id.Params[0].Ty)
	}
}

// Value receiver of read-only method → SelfBorrow set.
func TestSelfBorrowValueReceiverReadOnly(t *testing.T) {
	src := `
type C struct { v int64 }
func (c C) read() int64 { return c.v }
`
	info, _ := parseAndInferAll(t, src)
	var read *types.FnSig
	for _, m := range info.Methods {
		if m.Owner == "C" && m.Name == "read" {
			read = m
			break
		}
	}
	if read == nil {
		t.Fatalf("method C::read not found")
	}
	if read.Self != types.SelfValue {
		t.Fatalf("expected Self=SelfValue, got %v", read.Self)
	}
	if !read.SelfBorrow {
		t.Fatalf("expected SelfBorrow = true")
	}
}

// Value receiver that mutates self stays !SelfBorrow.
func TestSelfBorrowValueReceiverMutatedNot(t *testing.T) {
	src := `
type C struct { v int64 }
func (c C) bump() { c.v = c.v + 1 }
`
	info, _ := parseAndInferAll(t, src)
	var bump *types.FnSig
	for _, m := range info.Methods {
		if m.Owner == "C" && m.Name == "bump" {
			bump = m
			break
		}
	}
	if bump == nil {
		t.Fatalf("method C::bump not found")
	}
	if bump.SelfBorrow {
		t.Fatalf("expected SelfBorrow = false (body mutates c.v)")
	}
}
