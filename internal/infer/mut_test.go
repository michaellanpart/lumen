package infer

import (
	"testing"

	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/parser"
	"github.com/lumen-lang/lumen/internal/types"
)

func parseAndInfer(t *testing.T, src string) *ast.Program {
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
	Mutability(prog, info)
	return prog
}

// firstFnNamed returns the body of the first FnDecl with the given name,
// searching free functions and impl-block methods.
func firstFnNamed(p *ast.Program, name string) *ast.FnDecl {
	for _, it := range p.Items {
		if fn, ok := it.(*ast.FnDecl); ok && fn.Name == name {
			return fn
		}
		if ib, ok := it.(*ast.ImplBlock); ok {
			for _, m := range ib.Methods {
				if m.Name == name {
					return m
				}
			}
		}
	}
	return nil
}

func letMut(fn *ast.FnDecl, varName string) (mut, found bool) {
	var visit func(b *ast.Block)
	visit = func(b *ast.Block) {
		for _, s := range b.Stmts {
			if l, ok := s.(*ast.LetStmt); ok {
				if bp, ok := l.Pattern.(*ast.BindPat); ok && bp.Name == varName {
					mut, found = l.Mut, true
					return
				}
			}
		}
	}
	visit(fn.Body)
	return
}

func TestInferImmutableLocal(t *testing.T) {
	src := `
func main() {
    x := 10
    y := x + 1
    println(y)
}`
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	for _, name := range []string{"x", "y"} {
		mut, ok := letMut(fn, name)
		if !ok {
			t.Fatalf("missing let %q", name)
		}
		if mut {
			t.Errorf("%s: want immutable, got mut", name)
		}
	}
}

func TestInferReassignedLocal(t *testing.T) {
	src := `
func main() {
    x := 0
    x = x + 1
    println(x)
}`
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	mut, ok := letMut(fn, "x")
	if !ok {
		t.Fatal("missing let x")
	}
	if !mut {
		t.Errorf("x: want mut, got immutable")
	}
}

func TestInferMutRef(t *testing.T) {
	src := `
type P struct { v int64 }

func main() {
    a := P{v: 1}
    r := &mut a
    r.v = 2
    println(a.v)
}`
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	mut, ok := letMut(fn, "a")
	if !ok {
		t.Fatal("missing let a")
	}
	if !mut {
		t.Errorf("a: want mut (taken &mut), got immutable")
	}
}

func TestInferFieldWrite(t *testing.T) {
	src := `
type P struct { v int64 }

func main() {
    a := P{v: 1}
    a.v = 2
    println(a.v)
}`
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	mut, ok := letMut(fn, "a")
	if !ok {
		t.Fatal("missing let a")
	}
	if !mut {
		t.Errorf("a: want mut (field written), got immutable")
	}
}

func TestInferReadOnlyMethod(t *testing.T) {
	src := `
type C struct { v int64 }

func (c C) get() int64 {
    return c.v
}

func main() {
    a := C{v: 5}
    println(a.get())
}`
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	mut, ok := letMut(fn, "a")
	if !ok {
		t.Fatal("missing let a")
	}
	if mut {
		t.Errorf("a: want immutable (only value-self method called), got mut")
	}
}

func TestInferMutMethod(t *testing.T) {
	src := `
type C struct { v int64 }

func (c *C) tick() {
    c.v = c.v + 1
}

func main() {
    a := C{v: 0}
    a.tick()
    println(a.v)
}`
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	mut, ok := letMut(fn, "a")
	if !ok {
		t.Fatal("missing let a")
	}
	if !mut {
		t.Errorf("a: want mut (*C method called), got immutable")
	}
}

func TestInferSharedRefStaysImmutable(t *testing.T) {
	src := `
type P struct { v int64 }

func read(p *P) int64 {
    return p.v
}

func main() {
    a := P{v: 7}
    println(read(&a))
}`
	// Note: under the v0.6 convention `*T` parameters are shared refs unless
	// the call-site uses `&mut`. So passing `&a` should leave `a` immutable.
	prog := parseAndInfer(t, src)
	fn := firstFnNamed(prog, "main")
	mut, ok := letMut(fn, "a")
	if !ok {
		t.Fatal("missing let a")
	}
	if mut {
		t.Errorf("a: want immutable (only &a taken), got mut")
	}
}
