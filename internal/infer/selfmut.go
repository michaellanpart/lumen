package infer

import (
	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/types"
)

// SelfMut infers, for every method whose receiver is currently `&self`
// (i.e. `func (r *T) m()` per the v0.6 surface), whether the body actually
// mutates the receiver — and if so, upgrades it to `&mut self`.
//
// A receiver `r` is considered mutated iff the body contains:
//
//   - `r.f = …`        place write through the receiver
//   - `r[i] = …`       index write through the receiver
//   - `*r = …`         deref write
//   - `&mut r` / `&mut r.f` / `&mut r[i]`     mutable borrow rooted at r
//   - `r.m(…)`         where m's current `Self` is `SelfRefMut`
//   - `r = …`          rebinding (rare — receivers aren't lvalues today,
//     but we treat this as evidence anyway)
//
// The pass iterates to a fixpoint so that `M` calling `self.N()` correctly
// upgrades `M` when `N` itself is upgraded in a later iteration.
//
// Method bodies that were already declared `func (r T) m()` (SelfValue)
// or have no receiver are skipped.
func SelfMut(prog *ast.Program, info *types.Info) {
	// Collect every method FnSig along with the AST body and receiver name.
	type methodEntry struct {
		sig  *types.FnSig
		body *ast.Block
		recv string
	}
	var methods []methodEntry
	for _, it := range prog.Items {
		ib, ok := it.(*ast.ImplBlock)
		if !ok {
			continue
		}
		for _, m := range ib.Methods {
			sig := findSigFor(info, m)
			if sig == nil || sig.Self == types.SelfNone || sig.Self == types.SelfValue {
				continue
			}
			if m.Body == nil {
				continue
			}
			methods = append(methods, methodEntry{sig, m.Body, sig.SelfName})
		}
	}
	// Iterate to fixpoint. Each pass: any method whose receiver shows a
	// mutating use under current Self assignments is upgraded.
	for {
		changed := false
		for _, e := range methods {
			if e.sig.Self == types.SelfRefMut {
				continue
			}
			if nameIsMutated(e.body, e.recv, info) {
				e.sig.Self = types.SelfRefMut
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// findSigFor returns the FnSig associated with this method's AST decl.
// We match on (Owner, Name) using info.Methods, which is the
// declaration-order list populated during type-check.
func findSigFor(info *types.Info, fn *ast.FnDecl) *types.FnSig {
	for _, sig := range info.Methods {
		if sig.Decl == fn {
			return sig
		}
	}
	return nil
}

// nameIsMutated reports whether any expression in body treats the binding
// `name` as a mutable place. Shared by `SelfMut` (receiver) and `ParamMut`
// (function parameters).
//
// Mutating uses detected: `name = ...`, `name.f = ...`, `name[i] = ...`,
// `*name = ...`, `&mut name` (and rooted variants), and `name.m(...)` where
// `m` is currently `SelfRefMut`.
func nameIsMutated(b *ast.Block, name string, info *types.Info) bool {
	r := &mutScan{name: name, info: info}
	r.block(b)
	return r.mutated
}

type mutScan struct {
	name    string
	info    *types.Info
	mutated bool
}

func (r *mutScan) block(b *ast.Block) {
	if b == nil || r.mutated {
		return
	}
	for _, s := range b.Stmts {
		if r.mutated {
			return
		}
		switch s := s.(type) {
		case *ast.LetStmt:
			r.expr(s.Value)
		case *ast.ExprStmt:
			r.expr(s.X)
		}
	}
	if !r.mutated && b.Tail != nil {
		r.expr(b.Tail)
	}
}

func (r *mutScan) expr(e ast.Expr) {
	if e == nil || r.mutated {
		return
	}
	switch x := e.(type) {
	case *ast.AssignExpr:
		// `r = …`, `r.f = …`, `r[i] = …`, `*r = …` all mutate the receiver.
		switch lv := x.L.(type) {
		case *ast.Ident:
			if lv.Name == r.name {
				r.mutated = true
				return
			}
		case *ast.FieldAccess, *ast.IndexExpr:
			if placeRoot(lv) == r.name {
				r.mutated = true
				return
			}
		case *ast.DerefExpr:
			if rootIdent(lv.X) == r.name {
				r.mutated = true
				return
			}
		}
		r.expr(x.L)
		r.expr(x.R)
	case *ast.RefExpr:
		if x.Mut && placeRoot(x.X) == r.name {
			r.mutated = true
			return
		}
		r.expr(x.X)
	case *ast.MethodCall:
		// `r.m(...)` where m is &mut self mutates r. Use the CURRENT Self
		// assignment from info so the fixpoint can propagate.
		if rootIdent(x.Recv) == r.name {
			if m := lookupMethod(x, r.info); m != nil && m.Self == types.SelfRefMut {
				r.mutated = true
				return
			}
		}
		// Auto-borrow: passing `r` (or a place rooted at r) as a non-ref arg
		// to a `&mut T` param implicitly takes `&mut r` and so mutates r.
		// Propagation: passing r itself (already a ref) to a `&mut T` param
		// means r must also be upgraded to `&mut T`.
		if m := lookupMethod(x, r.info); m != nil {
			for i, a := range x.Args {
				if i >= len(m.Params) {
					break
				}
				pt := m.Params[i].Ty
				if pt.Kind == types.KRef && pt.Mut {
					if r.info.AutoBorrow[a] && placeRoot(a) == r.name {
						r.mutated = true
						return
					}
					if id, ok := a.(*ast.Ident); ok && id.Name == r.name {
						r.mutated = true
						return
					}
				}
			}
		}
		r.expr(x.Recv)
		for _, a := range x.Args {
			r.expr(a)
		}
	case *ast.Block:
		r.block(x)
	case *ast.IfExpr:
		r.expr(x.Cond)
		r.block(x.Then)
		r.expr(x.Else)
	case *ast.WhileExpr:
		r.expr(x.Cond)
		r.block(x.Body)
	case *ast.ForExpr:
		r.expr(x.Iter)
		r.block(x.Body)
	case *ast.LoopExpr:
		r.block(x.Body)
	case *ast.MatchExpr:
		r.expr(x.Scrut)
		for _, a := range x.Arms {
			r.expr(a.Body)
		}
	case *ast.Call:
		// Auto-borrow to a &mut T free-fn param mutates the arg's root.
		// Propagation: passing r (already a ref) to a `&mut T` param also
		// counts as a mutating use of r.
		if id, ok := x.Callee.(*ast.Ident); ok {
			if sig := r.info.Fns[id.Name]; sig != nil {
				for i, a := range x.Args {
					if i >= len(sig.Params) {
						break
					}
					pt := sig.Params[i].Ty
					if pt.Kind == types.KRef && pt.Mut {
						if r.info.AutoBorrow[a] && placeRoot(a) == r.name {
							r.mutated = true
							return
						}
						if aid, ok := a.(*ast.Ident); ok && aid.Name == r.name {
							r.mutated = true
							return
						}
					}
				}
			}
		}
		r.expr(x.Callee)
		for _, a := range x.Args {
			r.expr(a)
		}
	case *ast.Binary:
		r.expr(x.L)
		r.expr(x.R)
	case *ast.Unary:
		r.expr(x.X)
	case *ast.FieldAccess:
		r.expr(x.X)
	case *ast.IndexExpr:
		r.expr(x.X)
		r.expr(x.I)
	case *ast.DerefExpr:
		r.expr(x.X)
	case *ast.ReturnExpr:
		r.expr(x.X)
	case *ast.BreakExpr:
		r.expr(x.X)
	case *ast.StructLit:
		for _, f := range x.Fields {
			r.expr(f.Value)
		}
	}
}

// rootIdent returns the identifier name at the base of e, or "" if e is
// not rooted at an identifier.
func rootIdent(e ast.Expr) string {
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

// lookupMethod resolves a MethodCall against info to find its FnSig.
// Returns nil if recv type / method cannot be resolved.
func lookupMethod(mc *ast.MethodCall, info *types.Info) *types.FnSig {
	recvTy, ok := info.ExprTypes[mc.Recv]
	if !ok {
		return nil
	}
	var st *types.StructTy
	switch recvTy.Kind {
	case types.KStruct:
		st = recvTy.Struct
	case types.KRef:
		if recvTy.Inner != nil && recvTy.Inner.Kind == types.KStruct {
			st = recvTy.Inner.Struct
		}
	}
	if st == nil {
		return nil
	}
	return st.Methods[mc.Method]
}
