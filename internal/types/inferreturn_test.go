package types

import (
	"strings"
	"testing"

	"github.com/lumen-lang/lumen/internal/parser"
)

func parseAndCheck(t *testing.T, src string) *Info {
	t.Helper()
	prog, errs := parser.Parse("test.lm", src)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	info, terrs := Check(prog)
	if len(terrs) != 0 {
		t.Fatalf("type errors: %v", terrs)
	}
	return info
}

func TestReturnInferLiteral(t *testing.T) {
	info := parseAndCheck(t, `
func answer() {
    return 42
}
func main() { println(answer()) }
`)
	if got := info.Fns["answer"].Return.Kind; got != KI64 {
		t.Fatalf("answer return = %v; want KI64", got)
	}
}

func TestReturnInferParamPassthrough(t *testing.T) {
	info := parseAndCheck(t, `
func id(x int64) {
    return x
}
func main() { println(id(7)) }
`)
	if got := info.Fns["id"].Return.Kind; got != KI64 {
		t.Fatalf("id return = %v; want KI64", got)
	}
}

func TestReturnInferStructLit(t *testing.T) {
	info := parseAndCheck(t, `
type P struct { x int64; y int64 }
func mk() {
    return P{x: 1, y: 2}
}
func main() { p := mk(); println(p.x, p.y) }
`)
	sig := info.Fns["mk"]
	if sig.Return.Kind != KStruct || sig.Return.Struct == nil || sig.Return.Struct.Name != "P" {
		t.Fatalf("mk return = %v; want struct P", sig.Return)
	}
}

func TestReturnInferForwardCall(t *testing.T) {
	// `caller` is declared before `callee`, exercising the fixpoint.
	info := parseAndCheck(t, `
func caller() {
    return callee()
}
func callee() {
    return 5
}
func main() { println(caller()) }
`)
	if got := info.Fns["caller"].Return.Kind; got != KI64 {
		t.Fatalf("caller return = %v; want KI64", got)
	}
	if got := info.Fns["callee"].Return.Kind; got != KI64 {
		t.Fatalf("callee return = %v; want KI64", got)
	}
}

func TestReturnInferOmittedDefaultsUnit(t *testing.T) {
	info := parseAndCheck(t, `
func noop() {
    println("hi")
}
func main() { noop() }
`)
	if got := info.Fns["noop"].Return.Kind; got != KUnit {
		t.Fatalf("noop return = %v; want KUnit", got)
	}
}

func TestReturnInferValueReceiver(t *testing.T) {
	info := parseAndCheck(t, `
type Pt struct { x int64; y int64 }
func (p Pt) magnitude_sq() {
    return p.x * p.x + p.y * p.y
}
func main() {
    p := Pt{x: 3, y: 4}
    println(p.magnitude_sq())
}
`)
	pt := info.Structs["Pt"]
	if pt == nil {
		t.Fatal("struct Pt not found")
	}
	m := pt.Methods["magnitude_sq"]
	if m == nil {
		t.Fatal("method magnitude_sq not found")
	}
	if got := m.Return.Kind; got != KI64 {
		t.Fatalf("magnitude_sq return = %v; want KI64", got)
	}
}

func TestReturnInferExplicitAnnotationUnchanged(t *testing.T) {
	// When the user writes an explicit return type, the inferencer must
	// not overwrite it.
	info := parseAndCheck(t, `
func two() int64 {
    return 2
}
func main() { println(two()) }
`)
	if got := info.Fns["two"].Return.Kind; got != KI64 {
		t.Fatalf("two return = %v; want KI64", got)
	}
}

func TestReturnInferMismatchStillErrors(t *testing.T) {
	// Once the return is inferred as i64, an explicit `return "..."` in
	// the same body must still raise a type error.
	prog, perrs := parser.Parse("test.lm", `
func bad() {
    if true { return 1 }
    return "x"
}
func main() { println(bad()) }
`)
	if len(perrs) != 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	_, terrs := Check(prog)
	if len(terrs) == 0 {
		t.Fatal("expected a type error for mismatched return values")
	}
	joined := ""
	for _, e := range terrs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "return") {
		t.Fatalf("expected a return-type mismatch error; got: %s", joined)
	}
}

// --- Tests for trailing-expression-as-block-tail (no `return` keyword) ---

func TestReturnInferTailLiteral(t *testing.T) {
	info := parseAndCheck(t, `
func one() {
    1
}
func main() { println(one()) }
`)
	if got := info.Fns["one"].Return.Kind; got != KI64 {
		t.Fatalf("one return = %v; want KI64", got)
	}
}

func TestReturnInferTailBinary(t *testing.T) {
	info := parseAndCheck(t, `
func square(x int64) {
    x * x
}
func main() { println(square(5)) }
`)
	if got := info.Fns["square"].Return.Kind; got != KI64 {
		t.Fatalf("square return = %v; want KI64", got)
	}
}

func TestReturnInferTailStructLit(t *testing.T) {
	info := parseAndCheck(t, `
type P struct { x int64; y int64 }
func origin() {
    P{x: 0, y: 0}
}
func main() { o := origin(); println(o.x, o.y) }
`)
	sig := info.Fns["origin"]
	if sig.Return.Kind != KStruct || sig.Return.Struct == nil || sig.Return.Struct.Name != "P" {
		t.Fatalf("origin return = %v; want struct P", sig.Return)
	}
}

func TestReturnInferTailCall(t *testing.T) {
	info := parseAndCheck(t, `
func helper() {
    42
}
func wrapper() {
    helper()
}
func main() { println(wrapper()) }
`)
	if got := info.Fns["wrapper"].Return.Kind; got != KI64 {
		t.Fatalf("wrapper return = %v; want KI64", got)
	}
}

func TestReturnInferTailUnitCallNoError(t *testing.T) {
	// A fn whose tail is a unit-typed call (like println) must infer
	// unit, not error out.
	info := parseAndCheck(t, `
func greet() {
    println("hi")
}
func main() { greet() }
`)
	if got := info.Fns["greet"].Return.Kind; got != KUnit {
		t.Fatalf("greet return = %v; want KUnit", got)
	}
}
