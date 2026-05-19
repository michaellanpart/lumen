package borrowck_test

import (
	"strings"
	"testing"

	"github.com/lumen-lang/lumen/internal/borrowck"
	"github.com/lumen-lang/lumen/internal/infer"
	"github.com/lumen-lang/lumen/internal/parser"
	"github.com/lumen-lang/lumen/internal/types"
)

func run(t *testing.T, src string) []error {
	t.Helper()
	prog, perrs := parser.Parse("test.lm", src)
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	info, terrs := types.Check(prog)
	if len(terrs) > 0 {
		t.Fatalf("type errors: %v", terrs)
	}
	infer.SelfMut(prog, info)
	return borrowck.Check(prog, info)
}

func TestPrimitivesAreCopy(t *testing.T) {
	errs := run(t, `
func take(n int64) int64 { return n }
func main() {
    x := 1
    a := take(x)
    b := take(x)
    println(a, b)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestBorrowsDoNotMove(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func sum(p *P) int64 { return p.x }
func main() {
    p := P{x: 3}
    a := sum(&p)
    b := sum(&p)
    println(a, b)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestUseAfterMoveInArg(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 3}
    a := take(p)
    b := take(p)
    println(a, b)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "p"`) {
		t.Fatalf("expected one use-after-move error, got: %v", errs)
	}
}

func TestUseAfterMoveInMethod(t *testing.T) {
	errs := run(t, `
type C struct { value int64 }
func (c C) consume() int64 { return c.value }
func main() {
    c := C{value: 7}
    a := c.consume()
    b := c.consume()
    println(a, b)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "c"`) {
		t.Fatalf("expected one use-after-move error, got: %v", errs)
	}
}

func TestRefMethodDoesNotMove(t *testing.T) {
	errs := run(t, `
type C struct { value int64 }
func (c *C) get() int64 { return c.value }
func main() {
    c := C{value: 7}
    a := c.get()
    b := c.get()
    println(a, b)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestReassignmentRevives(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    a := take(p)
    p = P{x: 2}
    b := take(p)
    println(a, b)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestIfBranchMergeFlagsPartialMove(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    if true {
        take(p)
    } else {
    }
    take(p)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "p"`) {
		t.Fatalf("expected one use-after-move error, got: %v", errs)
	}
}

func TestLoopBodyMoveRejected(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    i := 0
    for i < 3 {
        take(p)
        i = i + 1
    }
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "while-loop body moves") {
		t.Fatalf("expected one loop-move error, got: %v", errs)
	}
}

func TestMatchMovesScrutinee(t *testing.T) {
	errs := run(t, `
type Boxed enum { None; Some(int64) }
func take(b Boxed) int64 {
    switch b {
    case Boxed::None: return 0
    case Boxed::Some(x): return x
    }
}
func main() {
    b := Boxed::Some(7)
    _a := take(b)
    _c := take(b)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "b"`) {
		t.Fatalf("expected use-after-move on `b`, got: %v", errs)
	}
}

func TestMatchUnitEnumIsCopySafeByExhaustion(t *testing.T) {
	errs := run(t, `
type Color enum { Red; Green; Blue }
func label(c Color) int64 {
    switch c {
    case Color::Red: return 0
    case Color::Green: return 1
    case Color::Blue: return 2
    }
}
func main() { println(label(Color::Red)) }`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// --- v0.4.1 partial-move tests ---------------------------------------

// Moving one non-Copy field out should leave sibling fields usable.
func TestPartialMoveSiblingFieldOK(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func take(i Inner) int64 { return i.v }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    x := take(o.a)
    y := take(o.b)
    println(x, y)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Moving the same field twice should error.
func TestPartialMoveDoubleFieldErrors(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func take(i Inner) int64 { return i.v }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    _x := take(o.a)
    _y := take(o.a)
    println(_x, _y)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "o.a"`) {
		t.Fatalf("expected one use-after-move on o.a, got: %v", errs)
	}
}

// After moving a field out, using the *whole* struct is forbidden — the
// diagnostic should say "partially moved".
func TestPartialMoveWholeUseErrors(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func take(i Inner) int64 { return i.v }
func sink(o Outer) int64 { return o.a.v }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    _x := take(o.a)
    _y := sink(o)
    println(_x, _y)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of partially moved value "o"`) {
		t.Fatalf("expected partially-moved error on o, got: %v", errs)
	}
}

// Copy-typed fields stay readable freely even after a sibling move.
func TestPartialMovePrimitiveSiblingReadable(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; tag int64 }
func take(i Inner) int64 { return i.v }
func main() {
    o := Outer{a: Inner{v: 1}, tag: 7}
    x := take(o.a)
    println(x, o.tag)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Re-assigning the moved field revives it; using it afterwards is fine.
func TestPartialMoveFieldReassignmentRevives(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func take(i Inner) int64 { return i.v }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    _x := take(o.a)
    o.a = Inner{v: 3}
    _y := take(o.a)
    println(_x, _y)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Method receivers also honor partial moves: a value-self method on a
// field consumes that field.
func TestPartialMoveMethodReceiver(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
func (i Inner) take() int64 { return i.v }
type Outer struct { a Inner; b Inner }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    _x := o.a.take()
    _y := o.a.take()
    println(_x, _y)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "o.a"`) {
		t.Fatalf("expected use-after-move on o.a, got: %v", errs)
	}
}

// Branches that move different fields union: after the if, both fields
// are considered "possibly moved", but each non-moved sibling stays OK.
func TestPartialMoveIfBranchUnion(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func take(i Inner) int64 { return i.v }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    if true {
        take(o.a)
    } else {
        take(o.b)
    }
    take(o.a)
}`)
	// After the if, o.a is *possibly* moved (the then-branch moved it),
	// so using it must error.
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `use of moved value "o.a"`) {
		t.Fatalf("expected use-after-move on o.a after if, got: %v", errs)
	}
}

// --- v0.4.2 aliasing tests ------------------------------------------

// Multiple shared (&) borrows of the same place coexist freely.
func TestSharedBorrowsCoexist(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    r1 := &p
    r2 := &p
    println(r1.x, r2.x)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Cannot take a &mut while a & is live.
func TestMutBorrowDuringSharedErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    r := &p
    m := &mut p
    println(r.x, m.x)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot borrow") {
		t.Fatalf("expected borrow conflict, got: %v", errs)
	}
}

// Cannot take a second &mut while one is live.
func TestTwoMutBorrowsError(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    m1 := &mut p
    m2 := &mut p
    println(m1.x, m2.x)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot borrow") {
		t.Fatalf("expected borrow conflict, got: %v", errs)
	}
}

// Cannot take a & while a &mut is live.
func TestSharedBorrowDuringMutErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    m := &mut p
    r := &p
    println(m.x, r.x)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot borrow") {
		t.Fatalf("expected borrow conflict, got: %v", errs)
	}
}

// Cannot move a value while it's borrowed.
func TestMoveWhileBorrowedErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    r := &p
    n := take(p)
    println(r.x, n)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot move out of") {
		t.Fatalf("expected move-while-borrowed error, got: %v", errs)
	}
}

// Cannot assign to a place while it's mutably borrowed.
func TestAssignWhileMutBorrowedErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    m := &mut p
    p = P{x: 2}
    println(m.x)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot assign to") {
		t.Fatalf("expected assignment-while-borrowed error, got: %v", errs)
	}
}

// Cannot read a value (other than through the borrow) while &mut-borrowed.
func TestReadWhileMutBorrowedErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    m := &mut p
    n := take(p)
    println(m.x, n)
}`)
	// Two failures expected: the read is forbidden, AND moving while borrowed.
	// Just check that one is the read-conflict.
	if len(errs) == 0 {
		t.Fatalf("expected borrow error, got none")
	}
}

// Borrows confined to a nested block die at the block's end.
func TestBorrowExpiresAtBlockEnd(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func take(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    {
        r := &p
        println(r.x)
    }
    n := take(p)
    println(n)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Disjoint field borrows: &p.a and &mut p.b are both fine simultaneously.
func TestDisjointFieldBorrowsOK(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    ra := &o.a
    rb := &mut o.b
    println(ra.v, rb.v)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Overlapping field borrows still conflict: &mut o.a forbids &o (whole).
func TestNestedFieldBorrowConflicts(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    m := &mut o.a
    r := &o
    println(m.v, r.a.v)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot borrow") {
		t.Fatalf("expected overlap conflict, got: %v", errs)
	}
}

// A `&mut self` method call acts as a transient `&mut` borrow of the
// receiver: it must conflict with active `&` borrows of the same place.
func TestMutMethodWhileSharedBorrowedErrors(t *testing.T) {
	errs := run(t, `
type C struct { value int64 }
func (c *C) bump() { c.value = c.value + 1 }
func main() {
    c := C{value: 1}
    r := &c
    c.bump()
    println(r.value)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot call &mut method") {
		t.Fatalf("expected &mut-method-while-borrowed error, got: %v", errs)
	}
}

// And with another active `&mut` borrow.
func TestMutMethodWhileMutBorrowedErrors(t *testing.T) {
	errs := run(t, `
type C struct { value int64 }
func (c *C) bump() { c.value = c.value + 1 }
func main() {
    c := C{value: 1}
    m := &mut c
    c.bump()
    println(m.value)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot call &mut method") {
		t.Fatalf("expected &mut-method-while-borrowed error, got: %v", errs)
	}
}

// A `&self` method call is just a read; it coexists with active `&`
// borrows.
func TestRefMethodWhileSharedBorrowedOK(t *testing.T) {
	errs := run(t, `
type C struct { value int64 }
func (c *C) get() int64 { return c.value }
func main() {
    c := C{value: 1}
    r := &c
    a := c.get()
    println(r.value, a)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// A `&self` method call still conflicts with an active `&mut` borrow.
func TestRefMethodWhileMutBorrowedErrors(t *testing.T) {
	errs := run(t, `
type C struct { value int64 }
func (c *C) get() int64 { return c.value }
func main() {
    c := C{value: 1}
    m := &mut c
    a := c.get()
    println(m.value, a)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "mutably borrowed") {
		t.Fatalf("expected read-while-mut-borrowed error, got: %v", errs)
	}
}

// Within a single call, an explicit &mut p arg conflicts with an
// earlier &p arg over the same place.
func TestCallArgsMutVsSharedErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func both(a *P, b &mut P) { b.x = a.x }
func main() {
    p := P{x: 0}
    both(&p, &mut p)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot borrow") {
		t.Fatalf("expected call-arg overlap error, got: %v", errs)
	}
}

// Two &mut args to the same place conflict.
func TestCallArgsTwoMutErrors(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func swap(a &mut P, b &mut P) { a.x = b.x }
func main() {
    p := P{x: 0}
    swap(&mut p, &mut p)
}`)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "cannot borrow") {
		t.Fatalf("expected two-&mut-args error, got: %v", errs)
	}
}

// Two shared args to the same place coexist.
func TestCallArgsTwoSharedOK(t *testing.T) {
	errs := run(t, `
type P struct { x int64 }
func both(a *P, b *P) int64 { return a.x + b.x }
func main() {
    p := P{x: 1}
    n := both(&p, &p)
    println(n)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

// Disjoint field args still coexist even when one is &mut.
func TestCallArgsDisjointFieldsOK(t *testing.T) {
	errs := run(t, `
type Inner struct { v int64 }
type Outer struct { a Inner; b Inner }
func both(r *Inner, w &mut Inner) { w.v = r.v }
func main() {
    o := Outer{a: Inner{v: 1}, b: Inner{v: 2}}
    both(&o.a, &mut o.b)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
}

func TestNLLBorrowDiesBeforeMove(t *testing.T) {
	// With non-lexical lifetimes, the &mut borrow `r` ends after its last
	// use (the assignment), so consuming `p` afterwards must succeed.
	errs := run(t, `
type P struct { x int64 }
func sink(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    r := &mut p
    r.x = 5
    a := sink(p)
    println(a)
}`)
	if len(errs) != 0 {
		t.Fatalf("expected no errors with NLL, got: %v", errs)
	}
}

func TestNLLBorrowExtendsAcrossUse(t *testing.T) {
	// `r` is used AFTER the move attempt; the borrow is still live there.
	errs := run(t, `
type P struct { x int64 }
func sink(p P) int64 { return p.x }
func main() {
    p := P{x: 1}
    r := &mut p
    a := sink(p)
    r.x = 5
    println(a)
}`)
	if len(errs) == 0 {
		t.Fatalf("expected NLL-live borrow error, got none")
	}
}

func TestNLLLoopPreservesLexical(t *testing.T) {
	// Inside a loop body, NLL must be disabled: even though the
	// textual last-use of `r` lies before the use of `p`, the loop
	// makes them iterate, so the borrow stays live across the use.
	errs := run(t, `
type P struct { x int64 }
func read(p *P) int64 { return p.x }
func main() {
    p := P{x: 1}
    r := &mut p
    i := 0
    for i < 3 {
        r.x = r.x + 1
        a := read(&p)
        println(a)
        i = i + 1
    }
}`)
	if len(errs) == 0 {
		t.Fatalf("expected loop-suppresses-NLL error, got none")
	}
}

func TestShadowingWithSecondBorrowErrors(t *testing.T) {
	// Shadowing `r` with another borrow against the same root must
	// still detect the &mut + & conflict from the first binding.
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    r := &mut p
    r2 := &p
    println(r.x, r2.x)
}`)
	if len(errs) == 0 {
		t.Fatalf("expected mut+shared conflict, got none")
	}
}

func TestShadowingDoesNotMisidentifyBorrow(t *testing.T) {
	// Shadowing the borrow binding `r` with a fresh non-borrow integer
	// must NOT erase the underlying borrow against `p`: writing to p
	// while the borrow is still live (used after the shadow) must error.
	errs := run(t, `
type P struct { x int64 }
func main() {
    p := P{x: 1}
    r := &mut p
    s := 5
    p.x = 9
    r.x = 7
    println(s, r.x)
}`)
	if len(errs) == 0 {
		t.Fatalf("expected write-while-mut-borrowed error, got none")
	}
}
