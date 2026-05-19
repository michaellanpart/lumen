package types

import (
	"github.com/lumen-lang/lumen/internal/ast"
)

// inferReturns fills in `sig.Return` for every function whose AST decl
// omitted the return type. It runs after Pass 1 (signature collection)
// and before Pass 2 (body checking), so callees seen during body
// type-check have the inferred return type available.
//
// The algorithm is a fixpoint over a simple syntactic deduction: for
// each unresolved function we walk the body's tail expression and any
// `return X` statements with `guessExprType` — a partial type
// inferencer that uses only stable information (param types, the self
// type, other functions' already-known return types, struct/enum
// declarations).
//
// Functions that cannot be resolved after the fixpoint settle to
// `TUnit` (the natural default for a body whose tail produces no
// value).
//
// Limitations (acceptable for v0.7):
//   - Recursive functions with omitted return types may need an
//     explicit annotation if the inferencer can't see a non-recursive
//     branch first.
//   - Local-variable lookups aren't supported (we'd need a full check
//     to know their types).
func (c *checker) inferReturns(prog *ast.Program) {
	type entry struct {
		sig  *FnSig
		decl *ast.FnDecl
	}
	var pending []entry
	for _, sig := range c.info.Fns {
		if sig.Decl != nil && sig.Decl.Return == nil {
			pending = append(pending, entry{sig, sig.Decl})
		}
	}
	for _, sig := range c.info.Methods {
		if sig.Decl != nil && sig.Decl.Return == nil {
			pending = append(pending, entry{sig, sig.Decl})
		}
	}
	for {
		changed := false
		for _, e := range pending {
			if e.sig.Return.Kind != KUnknown {
				continue
			}
			t := c.guessFnReturn(e.sig, e.decl)
			if t.Kind != KUnknown {
				e.sig.Return = t
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// Anything still KUnknown defaults to unit.
	for _, e := range pending {
		if e.sig.Return.Kind == KUnknown {
			e.sig.Return = TUnit
		}
	}
}

// guessFnReturn merges the tail-expression guess with the guesses from
// every `return X` in the body. The first non-Unknown type wins.
func (c *checker) guessFnReturn(sig *FnSig, decl *ast.FnDecl) Type {
	if decl.Body == nil {
		return TUnit
	}
	// Look at explicit `return X` statements first; they directly state
	// the intended type.
	if t := c.guessReturnsInBlock(decl.Body, sig); t.Kind != KUnknown {
		return t
	}
	// Fall back to the block's tail expression — but only when it's an
	// actual tail (Block.Tail). A trailing ExprStmt is a statement whose
	// value is discarded, so the block evaluates to unit.
	if decl.Body.Tail != nil {
		if t := c.guessExprType(decl.Body.Tail, sig); t.Kind != KUnknown {
			return t
		}
		return Type{Kind: KUnknown}
	}
	return TUnit
}

// guessReturnsInBlock walks every statement and nested expression
// looking for `return X` and reports the first concretely-typed X.
func (c *checker) guessReturnsInBlock(b *ast.Block, sig *FnSig) Type {
	if b == nil {
		return Type{Kind: KUnknown}
	}
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			if t := c.guessReturnsInExpr(s.Value, sig); t.Kind != KUnknown {
				return t
			}
		case *ast.ExprStmt:
			if t := c.guessReturnsInExpr(s.X, sig); t.Kind != KUnknown {
				return t
			}
		}
	}
	if b.Tail != nil {
		if t := c.guessReturnsInExpr(b.Tail, sig); t.Kind != KUnknown {
			return t
		}
	}
	return Type{Kind: KUnknown}
}

func (c *checker) guessReturnsInExpr(e ast.Expr, sig *FnSig) Type {
	if e == nil {
		return Type{Kind: KUnknown}
	}
	switch x := e.(type) {
	case *ast.ReturnExpr:
		if x.X == nil {
			return TUnit
		}
		return c.guessExprType(x.X, sig)
	case *ast.IfExpr:
		if t := c.guessReturnsInBlock(x.Then, sig); t.Kind != KUnknown {
			return t
		}
		return c.guessReturnsInExpr(x.Else, sig)
	case *ast.Block:
		return c.guessReturnsInBlock(x, sig)
	case *ast.MatchExpr:
		for _, a := range x.Arms {
			if t := c.guessReturnsInExpr(a.Body, sig); t.Kind != KUnknown {
				return t
			}
		}
	case *ast.WhileExpr:
		return c.guessReturnsInBlock(x.Body, sig)
	case *ast.ForExpr:
		return c.guessReturnsInBlock(x.Body, sig)
	case *ast.LoopExpr:
		return c.guessReturnsInBlock(x.Body, sig)
	}
	return Type{Kind: KUnknown}
}

// guessExprType is a best-effort type guesser. It returns Type{Kind:
// KUnknown} when it can't decide — callers then either retry next
// fixpoint iteration or fall back to unit.
func (c *checker) guessExprType(e ast.Expr, sig *FnSig) Type {
	switch x := e.(type) {
	case *ast.IntLit:
		return Type{Kind: KI64}
	case *ast.FloatLit:
		return Type{Kind: KF64}
	case *ast.BoolLit:
		return Type{Kind: KBool}
	case *ast.StringLit:
		return Type{Kind: KString}
	case *ast.UnitLit:
		return TUnit
	case *ast.Ident:
		// Receiver?
		if sig.Self != SelfNone && x.Name == sig.SelfName {
			owner := c.info.Structs[sig.Owner]
			if owner == nil {
				return Type{Kind: KUnknown}
			}
			switch sig.Self {
			case SelfValue:
				return MkStruct(owner)
			case SelfRef:
				return MkRef(MkStruct(owner), false)
			case SelfRefMut:
				return MkRef(MkStruct(owner), true)
			}
		}
		// Parameter?
		for _, p := range sig.Params {
			if p.Name == x.Name {
				return p.Ty
			}
		}
		return Type{Kind: KUnknown}
	case *ast.Path:
		// Enum variant constructor used as a value (unit variants).
		if len(x.Segments) == 2 {
			if et, ok := c.info.Enums[x.Segments[0]]; ok {
				return MkEnum(et)
			}
		}
		return Type{Kind: KUnknown}
	case *ast.Binary:
		switch x.Op {
		case "==", "!=", "<", ">", "<=", ">=", "&&", "||":
			return Type{Kind: KBool}
		case "+", "-", "*", "/", "%":
			// Homogeneous arithmetic: type of the left operand.
			return c.guessExprType(x.L, sig)
		}
		return Type{Kind: KUnknown}
	case *ast.Unary:
		if x.Op == "!" {
			return Type{Kind: KBool}
		}
		return c.guessExprType(x.X, sig)
	case *ast.Call:
		// Look up callee's signature.
		switch cl := x.Callee.(type) {
		case *ast.Ident:
			if cl.Name == "println" || cl.Name == "print" {
				return TUnit
			}
			if s, ok := c.info.Fns[cl.Name]; ok {
				return s.Return
			}
		case *ast.Path:
			// `Owner::fn(...)` — associated function.
			if len(cl.Segments) == 2 {
				if st, ok := c.info.Structs[cl.Segments[0]]; ok {
					if m := st.Methods[cl.Segments[1]]; m != nil {
						return m.Return
					}
				}
				// Enum tuple variant constructor `E::V(...)`.
				if et, ok := c.info.Enums[cl.Segments[0]]; ok {
					return MkEnum(et)
				}
			}
		}
		return Type{Kind: KUnknown}
	case *ast.MethodCall:
		recvTy := c.guessExprType(x.Recv, sig)
		var st *StructTy
		switch recvTy.Kind {
		case KStruct:
			st = recvTy.Struct
		case KRef:
			if recvTy.Inner != nil && recvTy.Inner.Kind == KStruct {
				st = recvTy.Inner.Struct
			}
		}
		if st != nil {
			if m := st.Methods[x.Method]; m != nil {
				return m.Return
			}
		}
		return Type{Kind: KUnknown}
	case *ast.StructLit:
		if len(x.Path) >= 1 {
			name := x.Path[0]
			if name == "Self" && sig.Owner != "" {
				name = sig.Owner
			}
			if st, ok := c.info.Structs[name]; ok {
				return MkStruct(st)
			}
		}
		return Type{Kind: KUnknown}
	case *ast.FieldAccess:
		recvTy := c.guessExprType(x.X, sig)
		var st *StructTy
		switch recvTy.Kind {
		case KStruct:
			st = recvTy.Struct
		case KRef:
			if recvTy.Inner != nil && recvTy.Inner.Kind == KStruct {
				st = recvTy.Inner.Struct
			}
		}
		if st != nil {
			if f, _ := st.Field(x.Name); f != nil {
				return f.Ty
			}
		}
		return Type{Kind: KUnknown}
	case *ast.RefExpr:
		inner := c.guessExprType(x.X, sig)
		if inner.Kind == KUnknown {
			return Type{Kind: KUnknown}
		}
		return MkRef(inner, x.Mut)
	case *ast.DerefExpr:
		inner := c.guessExprType(x.X, sig)
		if inner.Kind == KRef && inner.Inner != nil {
			return *inner.Inner
		}
		return Type{Kind: KUnknown}
	case *ast.IfExpr:
		// Use the Then-branch tail; the type checker will enforce
		// arm-equality when the body is fully checked.
		if x.Then != nil && x.Then.Tail != nil {
			return c.guessExprType(x.Then.Tail, sig)
		}
		return Type{Kind: KUnknown}
	case *ast.MatchExpr:
		for _, a := range x.Arms {
			if t := c.guessExprType(a.Body, sig); t.Kind != KUnknown {
				return t
			}
		}
		return Type{Kind: KUnknown}
	case *ast.Block:
		if x.Tail != nil {
			return c.guessExprType(x.Tail, sig)
		}
		return TUnit
	case *ast.ReturnExpr:
		// `return X` as the tail expression: caller cares about X's type.
		if x.X == nil {
			return TUnit
		}
		return c.guessExprType(x.X, sig)
	case *ast.AssignExpr:
		return TUnit
	}
	return Type{Kind: KUnknown}
}
