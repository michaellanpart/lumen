package infer

import (
	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/types"
)

// ParamBorrow infers, for every non-Copy *value* parameter (`p T` where
// T is a struct or enum) and every value receiver (`func (r T) m()`),
// whether the body actually mutates that binding. If it does not, the
// parameter / receiver is flagged as a "borrow" — meaning borrowck will
// not treat passing a variable into it as a move.
//
// This is the v0.7 "move-vs-borrow" inference: users write the obvious
// `func take(p Point)` without any `&`/`*`/`move` ceremony, and the
// language quietly does the right thing at call sites.
//
// Mutation detection reuses `nameIsMutated` (the same scanner used by
// SelfMut and ParamMut), so the criteria are consistent across passes:
// any place-write rooted at the binding, any `&mut` rooted at it, any
// rebinding, and any call to a currently-`&mut self` method on it.
//
// The pass iterates to a fixpoint so that a function whose only "use"
// of its param is to forward it to another function gets the same
// answer as the callee — once the callee has been classified.
//
// Cbackend lowering is intentionally unchanged: borrowed params are
// still emitted by value in C. The flag is purely a borrow-checker
// hint. Perf-oriented `const T *` lowering can be layered in later.
func ParamBorrow(prog *ast.Program, info *types.Info) {
	type entry struct {
		sig  *types.FnSig
		body *ast.Block
		// names parallel to sig.Params (receiver-excluded).
		names []string
	}
	var fns []entry
	add := func(sig *types.FnSig, fn *ast.FnDecl) {
		if sig == nil || fn == nil || fn.Body == nil {
			return
		}
		names := make([]string, 0, len(sig.Params))
		for _, ap := range fn.Params {
			if ap.IsSelf {
				continue
			}
			names = append(names, ap.Name)
		}
		fns = append(fns, entry{sig, fn.Body, names})
	}
	for _, it := range prog.Items {
		switch d := it.(type) {
		case *ast.FnDecl:
			add(findFreeSig(info, d), d)
		case *ast.ImplBlock:
			for _, m := range d.Methods {
				add(findSigFor(info, m), m)
			}
		}
	}
	for {
		changed := false
		for _, e := range fns {
			// Value receiver: if body never mutates it, mark as borrow.
			if e.sig.Self == types.SelfValue && !e.sig.SelfBorrow && e.sig.SelfName != "" {
				if !nameIsMutated(e.body, e.sig.SelfName, info) {
					e.sig.SelfBorrow = true
					changed = true
				}
			}
			// Value params of non-Copy type.
			for i := range e.sig.Params {
				p := &e.sig.Params[i]
				if p.Borrow {
					continue
				}
				if p.Ty.Kind != types.KStruct && p.Ty.Kind != types.KEnum {
					continue
				}
				if i >= len(e.names) {
					continue
				}
				if !nameIsMutated(e.body, e.names[i], info) {
					p.Borrow = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
}
