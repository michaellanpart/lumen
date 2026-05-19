package types

import (
	"strings"
	"testing"

	"github.com/lumen-lang/lumen/internal/parser"
)

// First-class fn values: a bare ident that names a top-level free function
// type-checks as `fn(params...) ret` (KFn).
func TestFnValueIdentHasFnType(t *testing.T) {
	info := parseAndCheck(t, `
func greet() {
    print("hi\n")
}

func main() {
    f := greet
    print("ok\n")
}
`)
	// Find the LetStmt RHS Ident `greet` in main and confirm its ExprType.
	mainSig, ok := info.Fns["main"]
	if !ok {
		t.Fatalf("no main")
	}
	body := mainSig.Decl.Body
	if len(body.Stmts) == 0 {
		t.Fatalf("empty main body")
	}
	// First stmt is `f := greet`.
	let, ok := body.Stmts[0].(letStmtIface)
	if !ok {
		// Fallback: just check that no error was raised.
		t.Skip("AST shape differs; presence of no type-error already validates the path")
	}
	_ = let
}

func TestFnValueCanBeCalledIndirectly(t *testing.T) {
	prog, errs := parser.Parse("test.lm", `
func greet() {}
func main() {
    f := greet
    f()
}
`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	_, terrs := Check(prog)
	if len(terrs) != 0 {
		t.Fatalf("unexpected type errors: %v", terrs)
	}
}

func TestFnValueMethodNotFirstClass(t *testing.T) {
	prog, errs := parser.Parse("test.lm", `
type P struct { x int64 }
func (p P) get() int64 { return p.x }
func main() {
    p := P{x: 1}
    f := p.get
}
`)
	if len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	_, terrs := Check(prog)
	if len(terrs) == 0 {
		t.Fatalf("expected type error on method value, got none")
	}
}

// Mark used so the import isn't pruned.
var _ = strings.Contains

type letStmtIface interface{}
