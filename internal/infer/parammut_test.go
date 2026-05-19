package infer

import (
	"testing"

	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/parser"
	"github.com/lumen-lang/lumen/internal/types"
)

func parseAndInferAll(t *testing.T, src string) (*types.Info, map[string]*types.FnSig) {
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
	ParamMut(prog, info)
	ParamBorrow(prog, info)
	Mutability(prog, info)
	return info, info.Fns
}

func paramTy(fn *types.FnSig, idx int) types.Type {
	if fn == nil || idx >= len(fn.Params) {
		return types.Type{}
	}
	return fn.Params[idx].Ty
}

func TestParamReadOnly(t *testing.T) {
	src := `
type P struct { v int64 }

func get(p *P) int64 { return p.v }
`
	_, fns := parseAndInferAll(t, src)
	pt := paramTy(fns["get"], 0)
	if pt.Kind != types.KRef {
		t.Fatalf("param ty: want KRef, got %v", pt.Kind)
	}
	if pt.Mut {
		t.Errorf("get(p): want shared ref, got &mut")
	}
}

func TestParamMutInferred(t *testing.T) {
	src := `
type P struct { v int64 }

func bump(p *P) { p.v = p.v + 1 }
`
	_, fns := parseAndInferAll(t, src)
	pt := paramTy(fns["bump"], 0)
	if !pt.Mut {
		t.Errorf("bump(p): want &mut after inference, got shared ref")
	}
}

func TestParamMutFixpoint(t *testing.T) {
	src := `
type P struct { v int64 }

func bump(p *P) { p.v = p.v + 1 }
func bumpTwice(q *P) {
    bump(q)
    bump(q)
}
`
	_, fns := parseAndInferAll(t, src)
	if !paramTy(fns["bump"], 0).Mut {
		t.Errorf("bump(p): want &mut")
	}
	if !paramTy(fns["bumpTwice"], 0).Mut {
		t.Errorf("bumpTwice(q): want &mut (propagated via bump)")
	}
}

func TestAutoBorrowRecorded(t *testing.T) {
	src := `
type P struct { v int64 }

func get(p *P) int64 { return p.v }

func main() {
    a := P{v: 7}
    println(get(a))
}
`
	prog, errs := parser.Parse("test.lm", src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	info, terrs := types.Check(prog)
	if len(terrs) > 0 {
		t.Fatalf("type: %v", terrs)
	}
	if len(info.AutoBorrow) == 0 {
		t.Fatalf("expected at least one auto-borrow recorded, got 0")
	}
}

func TestAutoBorrowMutCascade(t *testing.T) {
	// When `a` is auto-borrowed to a &mut T param, the local binding
	// must remain mutable (Mutability pass must NOT demote it to const).
	src := `
type P struct { v int64 }

func bump(p *P) { p.v = p.v + 1 }

func main() {
    a := P{v: 0}
    bump(a)
    println(a.v)
}
`
	prog, errs := parser.Parse("test.lm", src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	info, terrs := types.Check(prog)
	if len(terrs) > 0 {
		t.Fatalf("type: %v", terrs)
	}
	SelfMut(prog, info)
	ParamMut(prog, info)
	Mutability(prog, info)

	// Locate `a` LetStmt in main.
	var mainFn = info.Fns["main"]
	if mainFn == nil || mainFn.Decl == nil || mainFn.Decl.Body == nil {
		t.Fatal("main fn not found")
	}
	found := false
	for _, s := range mainFn.Decl.Body.Stmts {
		ls, ok := s.(*ast.LetStmt)
		if !ok {
			continue
		}
		bp, ok := ls.Pattern.(*ast.BindPat)
		if !ok || bp.Name != "a" {
			continue
		}
		found = true
		if !ls.Mut {
			t.Errorf("let a: want Mut=true (mutated via auto-borrow), got false")
		}
	}
	if !found {
		t.Fatal("could not find `let a` in main")
	}
}
