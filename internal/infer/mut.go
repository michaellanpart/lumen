// Package infer implements compile-time inference passes that run between
// type-checking and borrow-checking. Each pass narrows the AST so the user
// doesn't have to write the corresponding ceremony (mut, &/&mut, etc.).
//
// The first pass (Mutability) demotes `LetStmt.Mut` from the parser default
// (true) to false when the introduced binding is never mutated. This drives
// `const` qualifiers in the C backend and sets up future passes.
package infer

import (
	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/types"
)

// Mutability walks each function body and demotes `LetStmt.Mut` to false
// when no mutating use of the introduced name is found in the same fn.
//
// Rules — a name `x` is considered MUTATED if any of these appear:
//
//   - `x = …`                           (reassignment)
//   - `x.f = …`  / `x[i] = …`           (place write rooted at x)
//   - `&mut x`   / `&mut x.f`           (mutable borrow rooted at x)
//   - `x.m(…)`   where `m` takes `&mut self` and `x` is a value (auto-&mut)
//
// Crossing a `*p` (DerefExpr) breaks the chain — mutating `*p` mutates the
// pointee, not `p` itself.
//
// The analysis is intra-procedural and conservative w.r.t. shadowing: if
// any `LetStmt` named `x` is mutated, every `LetStmt` named `x` in the same
// function is left mutable. This may over-mark in pathological shadowing
// cases, which is always sound.
func Mutability(prog *ast.Program, info *types.Info) {
	for _, it := range prog.Items {
		switch d := it.(type) {
		case *ast.FnDecl:
			inferFn(d, info)
		case *ast.ImplBlock:
			for _, m := range d.Methods {
				inferFn(m, info)
			}
		}
	}
}

func inferFn(fn *ast.FnDecl, info *types.Info) {
	if fn.Body == nil {
		return
	}
	lets := []*ast.LetStmt{}
	collectLets(fn.Body, &lets)
	if len(lets) == 0 {
		return
	}
	mutated := map[string]bool{}
	walkExprStmt(fn.Body, info, mutated)
	for _, l := range lets {
		bp, ok := l.Pattern.(*ast.BindPat)
		if !ok {
			continue // leave non-trivial patterns mut for now
		}
		if !mutated[bp.Name] {
			l.Mut = false
		}
	}
}

func collectLets(b *ast.Block, out *[]*ast.LetStmt) {
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			*out = append(*out, s)
			walkExprForLets(s.Value, out)
		case *ast.ExprStmt:
			walkExprForLets(s.X, out)
		}
	}
	if b.Tail != nil {
		walkExprForLets(b.Tail, out)
	}
}

func walkExprForLets(e ast.Expr, out *[]*ast.LetStmt) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.Block:
		collectLets(x, out)
	case *ast.IfExpr:
		walkExprForLets(x.Cond, out)
		collectLets(x.Then, out)
		walkExprForLets(x.Else, out)
	case *ast.WhileExpr:
		walkExprForLets(x.Cond, out)
		collectLets(x.Body, out)
	case *ast.ForExpr:
		walkExprForLets(x.Iter, out)
		collectLets(x.Body, out)
	case *ast.LoopExpr:
		collectLets(x.Body, out)
	case *ast.MatchExpr:
		walkExprForLets(x.Scrut, out)
		for _, a := range x.Arms {
			walkExprForLets(a.Body, out)
		}
	case *ast.Call:
		walkExprForLets(x.Callee, out)
		for _, a := range x.Args {
			walkExprForLets(a, out)
		}
	case *ast.MethodCall:
		walkExprForLets(x.Recv, out)
		for _, a := range x.Args {
			walkExprForLets(a, out)
		}
	case *ast.Binary:
		walkExprForLets(x.L, out)
		walkExprForLets(x.R, out)
	case *ast.Unary:
		walkExprForLets(x.X, out)
	case *ast.AssignExpr:
		walkExprForLets(x.L, out)
		walkExprForLets(x.R, out)
	case *ast.FieldAccess:
		walkExprForLets(x.X, out)
	case *ast.IndexExpr:
		walkExprForLets(x.X, out)
		walkExprForLets(x.I, out)
	case *ast.DerefExpr:
		walkExprForLets(x.X, out)
	case *ast.RefExpr:
		walkExprForLets(x.X, out)
	case *ast.ReturnExpr:
		walkExprForLets(x.X, out)
	case *ast.BreakExpr:
		walkExprForLets(x.X, out)
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExprForLets(f.Value, out)
		}
	}
}

// walkExprStmt walks the function body recording mutating uses into `mut`.
func walkExprStmt(b *ast.Block, info *types.Info, mut map[string]bool) {
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			walkExpr(s.Value, info, mut)
		case *ast.ExprStmt:
			walkExpr(s.X, info, mut)
		}
	}
	if b.Tail != nil {
		walkExpr(b.Tail, info, mut)
	}
}

func walkExpr(e ast.Expr, info *types.Info, mut map[string]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ast.AssignExpr:
		if n := placeRoot(x.L); n != "" {
			mut[n] = true
		}
		walkExpr(x.L, info, mut)
		walkExpr(x.R, info, mut)
	case *ast.RefExpr:
		if x.Mut {
			if n := placeRoot(x.X); n != "" {
				mut[n] = true
			}
		}
		walkExpr(x.X, info, mut)
	case *ast.MethodCall:
		// Auto-&mut: a value receiver passed to a &mut-self method requires
		// the binding to be mutable.
		if methodNeedsMut(x, info) {
			if n := placeRoot(x.Recv); n != "" {
				mut[n] = true
			}
		}
		// Auto-borrow: passing a value as a non-ref arg to a `&mut T` param
		// implicitly takes `&mut x`, so the arg's root must be mutable.
		if m := lookupMethod(x, info); m != nil {
			for i, a := range x.Args {
				if i >= len(m.Params) {
					break
				}
				pt := m.Params[i].Ty
				if pt.Kind == types.KRef && pt.Mut && info.AutoBorrow[a] {
					if n := placeRoot(a); n != "" {
						mut[n] = true
					}
				}
			}
		}
		walkExpr(x.Recv, info, mut)
		for _, a := range x.Args {
			walkExpr(a, info, mut)
		}
	case *ast.Block:
		walkExprStmt(x, info, mut)
	case *ast.IfExpr:
		walkExpr(x.Cond, info, mut)
		walkExprStmt(x.Then, info, mut)
		walkExpr(x.Else, info, mut)
	case *ast.WhileExpr:
		walkExpr(x.Cond, info, mut)
		walkExprStmt(x.Body, info, mut)
	case *ast.ForExpr:
		walkExpr(x.Iter, info, mut)
		walkExprStmt(x.Body, info, mut)
	case *ast.LoopExpr:
		walkExprStmt(x.Body, info, mut)
	case *ast.MatchExpr:
		walkExpr(x.Scrut, info, mut)
		for _, a := range x.Arms {
			walkExpr(a.Body, info, mut)
		}
	case *ast.Call:
		// Auto-borrow: arg to a `&mut T` free-fn param mutates arg's root.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if sig := info.Fns[id.Name]; sig != nil {
				for i, a := range x.Args {
					if i >= len(sig.Params) {
						break
					}
					pt := sig.Params[i].Ty
					if pt.Kind == types.KRef && pt.Mut && info.AutoBorrow[a] {
						if n := placeRoot(a); n != "" {
							mut[n] = true
						}
					}
				}
			}
		}
		walkExpr(x.Callee, info, mut)
		for _, a := range x.Args {
			walkExpr(a, info, mut)
		}
	case *ast.Binary:
		walkExpr(x.L, info, mut)
		walkExpr(x.R, info, mut)
	case *ast.Unary:
		walkExpr(x.X, info, mut)
	case *ast.FieldAccess:
		walkExpr(x.X, info, mut)
	case *ast.IndexExpr:
		walkExpr(x.X, info, mut)
		walkExpr(x.I, info, mut)
	case *ast.DerefExpr:
		walkExpr(x.X, info, mut)
	case *ast.ReturnExpr:
		walkExpr(x.X, info, mut)
	case *ast.BreakExpr:
		walkExpr(x.X, info, mut)
	case *ast.StructLit:
		for _, f := range x.Fields {
			walkExpr(f.Value, info, mut)
		}
	}
}

// placeRoot returns the identifier name at the base of a place expression
// (`x`, `x.f`, `x[i]`), or "" if the place crosses a `*p` deref or is not
// rooted at an identifier.
func placeRoot(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.FieldAccess:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		default:
			return ""
		}
	}
}

// methodNeedsMut reports whether a method call site requires its receiver
// to be a mutable binding. True iff:
//   - the receiver is a value type (not already a ref), AND
//   - the resolved method takes `&mut self`.
//
// If the method cannot be resolved (unknown struct / method, or recv type
// not recorded), we conservatively return true so we don't accidentally
// demote a binding that will later need to be mutable.
func methodNeedsMut(mc *ast.MethodCall, info *types.Info) bool {
	recvTy, ok := info.ExprTypes[mc.Recv]
	if !ok {
		return true
	}
	if recvTy.Kind == types.KRef {
		// Caller already has a ref; no auto-borrow needed from the binding.
		return false
	}
	st := structFromType(recvTy)
	if st == nil {
		return false
	}
	m, ok := st.Methods[mc.Method]
	if !ok {
		return true
	}
	return m.Self == types.SelfRefMut
}

func structFromType(t types.Type) *types.StructTy {
	if t.Kind == types.KStruct {
		return t.Struct
	}
	return nil
}
