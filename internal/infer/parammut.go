package infer

import (
	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/types"
)

// ParamMut infers, for every reference-typed parameter (i.e. parsed as
// `*T` or `&T` — both produce `RefType{Mut:false}` today), whether the
// function body mutates through it. If so, the parameter's type is
// upgraded to `&mut T` (`RefType{Mut:true}`).
//
// This is the parameter analogue of `SelfMut` — the same mutation
// scanner walks the body looking for mutating uses of the param's name.
//
// The pass iterates to a fixpoint so that `f(p)` which only does
// `g(p)` correctly upgrades after `g`'s own parameter is upgraded in a
// later iteration.
//
// Note: explicit user-written `&mut T` annotations already have Mut=true
// and are skipped on every iteration. We never *downgrade* mut→shared.
func ParamMut(prog *ast.Program, info *types.Info) {
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
		// Map FnDecl param names to FnSig.Params (excluding the receiver).
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
			for i := range e.sig.Params {
				p := &e.sig.Params[i]
				if p.Ty.Kind != types.KRef {
					continue
				}
				if p.Ty.Mut {
					continue
				}
				if i >= len(e.names) {
					continue
				}
				if nameIsMutated(e.body, e.names[i], info) {
					p.Ty.Mut = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
}

// findFreeSig returns the FnSig for a free function declaration.
func findFreeSig(info *types.Info, fn *ast.FnDecl) *types.FnSig {
	for _, sig := range info.Fns {
		if sig.Decl == fn {
			return sig
		}
	}
	return nil
}
