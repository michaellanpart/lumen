package interp

import (
	"testing"

	"github.com/lumen-lang/lumen/internal/parser"
)

// runWithResult parses src, installs a `__set_result(v)` builtin that captures
// the last value passed to it, runs main, and returns Show(captured).
// Returns "" if nothing was captured.
func runWithResult(t *testing.T, src string) string {
	t.Helper()
	prog, errs := parser.Parse("<test>", src)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	in := New()
	var captured Value
	in.globals.Define("__set_result", &BuiltinV{Name: "__set_result", Fn: func(args []Value) (Value, error) {
		if len(args) > 0 {
			captured = args[0]
		}
		return &UnitV{}, nil
	}})
	if err := in.Run(prog); err != nil {
		t.Fatalf("runtime error: %v", err)
	}
	if captured == nil {
		return ""
	}
	return Show(captured)
}

func TestArithmetic(t *testing.T) {
	src := `
func main() {
    __set_result(2 + 3*4 - 6/2)
}`
	got := runWithResult(t, src)
	if got != "11" {
		t.Errorf("expected 11, got %s", got)
	}
}

func TestFib(t *testing.T) {
	src := `
func fib(n int64) int64 {
    if n < 2 { return n }
    return fib(n-1) + fib(n-2)
}
func main() { __set_result(fib(10)) }`
	got := runWithResult(t, src)
	if got != "55" {
		t.Errorf("fib(10) expected 55, got %s", got)
	}
}

func TestStructAndMethod(t *testing.T) {
	src := `
type Point struct { x int64; y int64 }
func (p Point) sum() int64 { return p.x + p.y }
func main() {
    p := Point{x: 3, y: 4}
    __set_result(p.sum())
}`
	got := runWithResult(t, src)
	if got != "7" {
		t.Errorf("expected 7, got %s", got)
	}
}

func TestEnumMatch(t *testing.T) {
	src := `
type Option enum { None; Some(int64) }
func unwrap(o Option, fallback int64) int64 {
    switch o {
    case Option::None: return fallback
    case Option::Some(x): return x
    }
}
func main() {
    __set_result(unwrap(Option::Some(8), 0))
}`
	got := runWithResult(t, src)
	if got != "8" {
		t.Errorf("expected 8, got %s", got)
	}
}
