// Package interp is a tree-walking interpreter for Lumen v0.1.
//
// It does not implement the borrow checker, effect system, comptime, or
// monomorphization yet — those are scheduled for later phases. Generics
// are evaluated by erasure (the dynamic dispatch falls out for free).
package interp

import (
	"fmt"
	"strings"
	"sync"

	"github.com/lumen-lang/lumen/internal/ast"
)

// --- Values ---

type Value interface{ valueTag() }

type IntV struct{ V int64 }
type FloatV struct{ V float64 }
type BoolV struct{ V bool }
type StringV struct{ V string }
type CharV struct{ V rune }
type UnitV struct{}
type TupleV struct{ Elems []Value }
type ArrayV struct{ Elems []Value }
type StructV struct {
	Name   string
	Fields map[string]Value
	Order  []string // field declaration order
}
type EnumV struct {
	Enum    string
	Variant string
	Tuple   []Value
	Fields  map[string]Value
}
type FnV struct {
	Decl    *ast.FnDecl
	Closure *Env
	Self    Value // for methods bound via dot
}
type LambdaV struct {
	Lam     *ast.Lambda
	Closure *Env
}
type BuiltinV struct {
	Name string
	Fn   func(args []Value) (Value, error)
}
type RefV struct {
	Mut bool
	To  *Value
}

func (*IntV) valueTag()     {}
func (*FloatV) valueTag()   {}
func (*BoolV) valueTag()    {}
func (*StringV) valueTag()  {}
func (*CharV) valueTag()    {}
func (*UnitV) valueTag()    {}
func (*TupleV) valueTag()   {}
func (*ArrayV) valueTag()   {}
func (*StructV) valueTag()  {}
func (*EnumV) valueTag()    {}
func (*FnV) valueTag()      {}
func (*LambdaV) valueTag()  {}
func (*BuiltinV) valueTag() {}
func (*RefV) valueTag()     {}

// Show returns a human-readable representation of v.
func Show(v Value) string {
	switch x := v.(type) {
	case nil:
		return "<nil>"
	case *IntV:
		return fmt.Sprintf("%d", x.V)
	case *FloatV:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", x.V), "0"), ".")
	case *BoolV:
		if x.V {
			return "true"
		}
		return "false"
	case *StringV:
		return x.V
	case *CharV:
		return fmt.Sprintf("'%c'", x.V)
	case *UnitV:
		return "()"
	case *TupleV:
		parts := make([]string, len(x.Elems))
		for i, e := range x.Elems {
			parts[i] = Show(e)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ArrayV:
		parts := make([]string, len(x.Elems))
		for i, e := range x.Elems {
			parts[i] = Show(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *StructV:
		var sb strings.Builder
		sb.WriteString(x.Name)
		sb.WriteString(" { ")
		for i, n := range x.Order {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(n)
			sb.WriteString(": ")
			sb.WriteString(Show(x.Fields[n]))
		}
		sb.WriteString(" }")
		return sb.String()
	case *EnumV:
		if len(x.Tuple) > 0 {
			parts := make([]string, len(x.Tuple))
			for i, e := range x.Tuple {
				parts[i] = Show(e)
			}
			return fmt.Sprintf("%s::%s(%s)", x.Enum, x.Variant, strings.Join(parts, ", "))
		}
		if len(x.Fields) > 0 {
			parts := make([]string, 0, len(x.Fields))
			for k, v := range x.Fields {
				parts = append(parts, fmt.Sprintf("%s: %s", k, Show(v)))
			}
			return fmt.Sprintf("%s::%s { %s }", x.Enum, x.Variant, strings.Join(parts, ", "))
		}
		return fmt.Sprintf("%s::%s", x.Enum, x.Variant)
	case *FnV:
		return fmt.Sprintf("<fn %s>", x.Decl.Name)
	case *LambdaV:
		return "<lambda>"
	case *BuiltinV:
		return fmt.Sprintf("<builtin %s>", x.Name)
	case *RefV:
		return "&" + Show(*x.To)
	}
	return fmt.Sprintf("<%T>", v)
}

// --- Environment ---

type Env struct {
	vars   map[string]*Value
	parent *Env
}

func NewEnv(parent *Env) *Env { return &Env{vars: map[string]*Value{}, parent: parent} }

func (e *Env) Define(name string, v Value) {
	vv := v
	e.vars[name] = &vv
}

func (e *Env) Get(name string) (Value, bool) {
	if p, ok := e.vars[name]; ok {
		return *p, true
	}
	if e.parent != nil {
		return e.parent.Get(name)
	}
	return nil, false
}

func (e *Env) Slot(name string) (*Value, bool) {
	if p, ok := e.vars[name]; ok {
		return p, true
	}
	if e.parent != nil {
		return e.parent.Slot(name)
	}
	return nil, false
}

func (e *Env) Set(name string, v Value) bool {
	if p, ok := e.vars[name]; ok {
		*p = v
		return true
	}
	if e.parent != nil {
		return e.parent.Set(name, v)
	}
	return false
}

// --- Interpreter ---

type Interpreter struct {
	globals *Env
	// type registry
	structs map[string]*ast.StructDecl
	enums   map[string]*ast.EnumDecl
	// inherent methods: type name -> method name -> FnDecl
	methods map[string]map[string]*ast.FnDecl
	// trait impls: (traitName, typeName) -> method name -> FnDecl
	traitImpls map[string]map[string]map[string]*ast.FnDecl
	// Mu serializes re-entrant evaluation triggered by runtime bridges
	// (e.g. HTTP handlers invoked from net/http goroutines). The v0.1
	// tree-walking evaluator is not internally thread-safe.
	Mu sync.Mutex
}

func New() *Interpreter {
	in := &Interpreter{
		globals:    NewEnv(nil),
		structs:    map[string]*ast.StructDecl{},
		enums:      map[string]*ast.EnumDecl{},
		methods:    map[string]map[string]*ast.FnDecl{},
		traitImpls: map[string]map[string]map[string]*ast.FnDecl{},
	}
	registerPrelude(in)
	return in
}

// Run loads a program and (if a `main` exists) invokes it.
func (in *Interpreter) Run(prog *ast.Program) error {
	if err := in.Load(prog); err != nil {
		return err
	}
	mainV, ok := in.globals.Get("main")
	if !ok {
		return nil
	}
	fn, ok := mainV.(*FnV)
	if !ok {
		return fmt.Errorf("`main` is not a function")
	}
	_, err := in.callFn(fn, nil)
	return err
}

// Load registers all top-level items in the program.
func (in *Interpreter) Load(prog *ast.Program) error {
	for _, it := range prog.Items {
		switch d := it.(type) {
		case *ast.FnDecl:
			in.globals.Define(d.Name, &FnV{Decl: d, Closure: in.globals})
		case *ast.StructDecl:
			in.structs[d.Name] = d
		case *ast.EnumDecl:
			in.enums[d.Name] = d
			// register variant constructors under EnumName::Variant in globals
			for _, v := range d.Variants {
				key := d.Name + "::" + v.Name
				vv := v
				dv := d
				if v.IsUnit && len(v.Fields) == 0 && len(v.Tuple) == 0 {
					in.globals.Define(key, &EnumV{Enum: d.Name, Variant: v.Name})
				} else {
					in.globals.Define(key, &BuiltinV{
						Name: key,
						Fn: func(args []Value) (Value, error) {
							ev := &EnumV{Enum: dv.Name, Variant: vv.Name}
							if len(vv.Tuple) > 0 {
								if len(args) != len(vv.Tuple) {
									return nil, fmt.Errorf("variant %s expects %d args, got %d", key, len(vv.Tuple), len(args))
								}
								ev.Tuple = args
							}
							return ev, nil
						},
					})
				}
			}
		case *ast.TraitDecl:
			// traits are not yet enforced — record default methods for later
			_ = d
		case *ast.ImplBlock:
			in.registerImpl(d)
		case *ast.TypeAlias, *ast.UseDecl:
			// no-op for v0.1
		case *ast.ConstDecl:
			v, err := in.eval(d.Value, in.globals)
			if err != nil {
				return err
			}
			in.globals.Define(d.Name, v)
		}
	}
	return nil
}

func (in *Interpreter) registerImpl(ib *ast.ImplBlock) {
	tyName := typeName(ib.ForType)
	if ib.Trait == nil {
		// inherent impl
		m := in.methods[tyName]
		if m == nil {
			m = map[string]*ast.FnDecl{}
			in.methods[tyName] = m
		}
		for _, fn := range ib.Methods {
			m[fn.Name] = fn
			// Static (non-self) methods are also reachable as `Type::method`.
			if !hasSelfParam(fn) {
				in.globals.Define(tyName+"::"+fn.Name, &FnV{Decl: fn, Closure: in.globals})
			}
		}
		return
	}
	traitName := typeName(ib.Trait)
	if in.traitImpls[traitName] == nil {
		in.traitImpls[traitName] = map[string]map[string]*ast.FnDecl{}
	}
	if in.traitImpls[traitName][tyName] == nil {
		in.traitImpls[traitName][tyName] = map[string]*ast.FnDecl{}
	}
	for _, fn := range ib.Methods {
		in.traitImpls[traitName][tyName][fn.Name] = fn
		// also expose as inherent for simple dot-call resolution
		if in.methods[tyName] == nil {
			in.methods[tyName] = map[string]*ast.FnDecl{}
		}
		if _, exists := in.methods[tyName][fn.Name]; !exists {
			in.methods[tyName][fn.Name] = fn
		}
		if !hasSelfParam(fn) {
			in.globals.Define(tyName+"::"+fn.Name, &FnV{Decl: fn, Closure: in.globals})
		}
	}
}

func hasSelfParam(fn *ast.FnDecl) bool {
	return len(fn.Params) > 0 && fn.Params[0].IsSelf
}

func typeName(t ast.Type) string {
	switch x := t.(type) {
	case *ast.NamedType:
		return strings.Join(x.Path, "::")
	case *ast.RefType:
		return typeName(x.Inner)
	case *ast.TupleType:
		return "()"
	case *ast.ArrayType:
		return "[]"
	}
	return "?"
}

// --- Control flow signals ---

type returnSignal struct{ V Value }
type breakSignal struct{ V Value }
type continueSignal struct{}
type runtimeErr struct{ Msg string }

func (e *returnSignal) Error() string   { return "<return>" }
func (e *breakSignal) Error() string    { return "<break>" }
func (e *continueSignal) Error() string { return "<continue>" }
func (e *runtimeErr) Error() string     { return e.Msg }

// --- Evaluation ---

func (in *Interpreter) eval(e ast.Expr, env *Env) (Value, error) {
	switch x := e.(type) {
	case *ast.IntLit:
		return &IntV{V: x.Value}, nil
	case *ast.FloatLit:
		return &FloatV{V: x.Value}, nil
	case *ast.StringLit:
		return &StringV{V: x.Value}, nil
	case *ast.CharLit:
		return &CharV{V: x.Value}, nil
	case *ast.BoolLit:
		return &BoolV{V: x.Value}, nil
	case *ast.UnitLit:
		return &UnitV{}, nil
	case *ast.Ident:
		if v, ok := env.Get(x.Name); ok {
			return v, nil
		}
		return nil, fmt.Errorf("%s: undefined name `%s`", x.Pos(), x.Name)
	case *ast.Path:
		key := strings.Join(x.Segments, "::")
		if v, ok := env.Get(key); ok {
			return v, nil
		}
		// Also check globals for enum-variant constructors when env shadows them locally
		if v, ok := in.globals.Get(key); ok {
			return v, nil
		}
		return nil, fmt.Errorf("%s: undefined path `%s`", x.Pos(), key)
	case *ast.Block:
		return in.evalBlock(x, env)
	case *ast.IfExpr:
		c, err := in.eval(x.Cond, env)
		if err != nil {
			return nil, err
		}
		b, ok := c.(*BoolV)
		if !ok {
			return nil, fmt.Errorf("%s: if condition must be bool", x.Pos())
		}
		if b.V {
			return in.evalBlock(x.Then, env)
		}
		if x.Else != nil {
			return in.eval(x.Else, env)
		}
		return &UnitV{}, nil
	case *ast.WhileExpr:
		for {
			c, err := in.eval(x.Cond, env)
			if err != nil {
				return nil, err
			}
			b, ok := c.(*BoolV)
			if !ok {
				return nil, fmt.Errorf("%s: while condition must be bool", x.Pos())
			}
			if !b.V {
				break
			}
			if _, err := in.evalBlock(x.Body, env); err != nil {
				if _, ok := err.(*breakSignal); ok {
					break
				}
				if _, ok := err.(*continueSignal); ok {
					continue
				}
				return nil, err
			}
		}
		return &UnitV{}, nil
	case *ast.ForExpr:
		iter, err := in.eval(x.Iter, env)
		if err != nil {
			return nil, err
		}
		items, err := iterate(iter)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			inner := NewEnv(env)
			if !bindPattern(x.Pat, item, inner, in) {
				return nil, fmt.Errorf("%s: for-pattern did not match", x.Pos())
			}
			if _, err := in.evalBlock(x.Body, inner); err != nil {
				if _, ok := err.(*breakSignal); ok {
					return &UnitV{}, nil
				}
				if _, ok := err.(*continueSignal); ok {
					continue
				}
				return nil, err
			}
		}
		return &UnitV{}, nil
	case *ast.LoopExpr:
		for {
			if _, err := in.evalBlock(x.Body, env); err != nil {
				if bs, ok := err.(*breakSignal); ok {
					if bs.V != nil {
						return bs.V, nil
					}
					return &UnitV{}, nil
				}
				if _, ok := err.(*continueSignal); ok {
					continue
				}
				return nil, err
			}
		}
	case *ast.ReturnExpr:
		var v Value = &UnitV{}
		if x.X != nil {
			var err error
			v, err = in.eval(x.X, env)
			if err != nil {
				return nil, err
			}
		}
		return nil, &returnSignal{V: v}
	case *ast.BreakExpr:
		var v Value
		if x.X != nil {
			var err error
			v, err = in.eval(x.X, env)
			if err != nil {
				return nil, err
			}
		}
		return nil, &breakSignal{V: v}
	case *ast.ContinueExpr:
		return nil, &continueSignal{}
	case *ast.Binary:
		return in.evalBinary(x, env)
	case *ast.Unary:
		v, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		return applyUnary(x.Op, v)
	case *ast.RefExpr:
		v, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		return &RefV{Mut: x.Mut, To: &v}, nil
	case *ast.DerefExpr:
		v, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		if r, ok := v.(*RefV); ok {
			return *r.To, nil
		}
		return v, nil
	case *ast.CastExpr:
		v, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		return castValue(v, x.Ty)
	case *ast.AssignExpr:
		return in.evalAssign(x, env)
	case *ast.Call:
		return in.evalCall(x, env)
	case *ast.MethodCall:
		return in.evalMethodCall(x, env)
	case *ast.FieldAccess:
		return in.evalFieldAccess(x, env)
	case *ast.IndexExpr:
		return in.evalIndex(x, env)
	case *ast.MatchExpr:
		return in.evalMatch(x, env)
	case *ast.Lambda:
		return &LambdaV{Lam: x, Closure: env}, nil
	case *ast.StructLit:
		return in.evalStructLit(x, env)
	case *ast.TupleExpr:
		elems := make([]Value, len(x.Elems))
		for i, e := range x.Elems {
			v, err := in.eval(e, env)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		return &TupleV{Elems: elems}, nil
	case *ast.ArrayLit:
		elems := make([]Value, len(x.Elems))
		for i, e := range x.Elems {
			v, err := in.eval(e, env)
			if err != nil {
				return nil, err
			}
			elems[i] = v
		}
		return &ArrayV{Elems: elems}, nil
	case *ast.TryExpr:
		v, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		ev, ok := v.(*EnumV)
		if !ok {
			return nil, fmt.Errorf("%s: `?` applied to non-Result/Option value", x.Pos())
		}
		switch {
		case ev.Variant == "Ok" || ev.Variant == "Some":
			if len(ev.Tuple) == 1 {
				return ev.Tuple[0], nil
			}
			return &UnitV{}, nil
		case ev.Variant == "Err" || ev.Variant == "None":
			return nil, &returnSignal{V: ev}
		}
		return nil, fmt.Errorf("%s: `?` on unsupported variant %s", x.Pos(), ev.Variant)
	case *ast.SpawnExpr:
		// v0.1: run synchronously and return unit. A real scheduler arrives in v0.6.
		if _, err := in.evalBlock(x.Body, env); err != nil {
			return nil, err
		}
		return &UnitV{}, nil
	}
	return nil, fmt.Errorf("%s: unsupported expression %T", e.Pos(), e)
}

func (in *Interpreter) evalBlock(b *ast.Block, parent *Env) (Value, error) {
	env := NewEnv(parent)
	for _, s := range b.Stmts {
		switch ss := s.(type) {
		case *ast.LetStmt:
			v, err := in.eval(ss.Value, env)
			if err != nil {
				return nil, err
			}
			if !bindPattern(ss.Pattern, v, env, in) {
				return nil, fmt.Errorf("%s: let-pattern did not match", ss.Pos())
			}
		case *ast.ExprStmt:
			if _, err := in.eval(ss.X, env); err != nil {
				return nil, err
			}
		case *ast.ItemStmt:
			// nested fn etc.: register locally
			switch d := ss.It.(type) {
			case *ast.FnDecl:
				env.Define(d.Name, &FnV{Decl: d, Closure: env})
			default:
				// best-effort: register globals
				in.Load(&ast.Program{Items: []ast.Item{ss.It}})
			}
		}
	}
	if b.Tail != nil {
		return in.eval(b.Tail, env)
	}
	return &UnitV{}, nil
}

func (in *Interpreter) evalBinary(b *ast.Binary, env *Env) (Value, error) {
	// short-circuit
	if b.Op == "&&" || b.Op == "||" {
		l, err := in.eval(b.L, env)
		if err != nil {
			return nil, err
		}
		lb, ok := l.(*BoolV)
		if !ok {
			return nil, fmt.Errorf("%s: logical op requires bool", b.Pos())
		}
		if b.Op == "&&" && !lb.V {
			return &BoolV{V: false}, nil
		}
		if b.Op == "||" && lb.V {
			return &BoolV{V: true}, nil
		}
		r, err := in.eval(b.R, env)
		if err != nil {
			return nil, err
		}
		rb, ok := r.(*BoolV)
		if !ok {
			return nil, fmt.Errorf("%s: logical op requires bool", b.Pos())
		}
		return &BoolV{V: rb.V}, nil
	}
	l, err := in.eval(b.L, env)
	if err != nil {
		return nil, err
	}
	r, err := in.eval(b.R, env)
	if err != nil {
		return nil, err
	}
	return applyBinary(b.Op, l, r)
}

func (in *Interpreter) evalAssign(a *ast.AssignExpr, env *Env) (Value, error) {
	rhs, err := in.eval(a.R, env)
	if err != nil {
		return nil, err
	}
	// compute new value for compound assignments
	if a.Op != "=" {
		cur, err := in.eval(a.L, env)
		if err != nil {
			return nil, err
		}
		op := strings.TrimSuffix(a.Op, "=")
		rhs, err = applyBinary(op, cur, rhs)
		if err != nil {
			return nil, err
		}
	}
	// place
	switch lhs := a.L.(type) {
	case *ast.Ident:
		if !env.Set(lhs.Name, rhs) {
			return nil, fmt.Errorf("%s: assign to undefined `%s`", a.Pos(), lhs.Name)
		}
		return &UnitV{}, nil
	case *ast.FieldAccess:
		recv, err := in.eval(lhs.X, env)
		if err != nil {
			return nil, err
		}
		if r, ok := recv.(*RefV); ok {
			recv = *r.To
		}
		if sv, ok := recv.(*StructV); ok {
			sv.Fields[lhs.Name] = rhs
			return &UnitV{}, nil
		}
		return nil, fmt.Errorf("%s: cannot assign to field of %T", a.Pos(), recv)
	case *ast.IndexExpr:
		container, err := in.eval(lhs.X, env)
		if err != nil {
			return nil, err
		}
		idxV, err := in.eval(lhs.I, env)
		if err != nil {
			return nil, err
		}
		i, ok := idxV.(*IntV)
		if !ok {
			return nil, fmt.Errorf("%s: index must be int", a.Pos())
		}
		switch c := container.(type) {
		case *ArrayV:
			if i.V < 0 || int(i.V) >= len(c.Elems) {
				return nil, fmt.Errorf("%s: index %d out of bounds", a.Pos(), i.V)
			}
			c.Elems[i.V] = rhs
			return &UnitV{}, nil
		}
		return nil, fmt.Errorf("%s: cannot index-assign into %T", a.Pos(), container)
	case *ast.DerefExpr:
		v, err := in.eval(lhs.X, env)
		if err != nil {
			return nil, err
		}
		r, ok := v.(*RefV)
		if !ok {
			return nil, fmt.Errorf("%s: cannot deref non-reference", a.Pos())
		}
		*r.To = rhs
		return &UnitV{}, nil
	}
	return nil, fmt.Errorf("%s: invalid assignment target", a.Pos())
}

func (in *Interpreter) evalCall(c *ast.Call, env *Env) (Value, error) {
	callee, err := in.eval(c.Callee, env)
	if err != nil {
		return nil, err
	}
	args := make([]Value, len(c.Args))
	for i, a := range c.Args {
		v, err := in.eval(a, env)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	switch f := callee.(type) {
	case *FnV:
		return in.callFn(f, args)
	case *LambdaV:
		return in.callLambda(f, args)
	case *BuiltinV:
		return f.Fn(args)
	}
	return nil, fmt.Errorf("%s: not callable: %s", c.Pos(), Show(callee))
}

func (in *Interpreter) callFn(f *FnV, args []Value) (Value, error) {
	env := NewEnv(f.Closure)
	// bind self if pre-bound by method-call
	pi := 0
	if f.Self != nil && len(f.Decl.Params) > 0 && f.Decl.Params[0].IsSelf {
		name := f.Decl.Params[0].Name
		if name == "" {
			name = "self"
		}
		env.Define(name, f.Self)
		pi = 1
	}
	for i := pi; i < len(f.Decl.Params); i++ {
		ai := i - pi
		if ai >= len(args) {
			return nil, fmt.Errorf("%s: missing argument for `%s`", f.Decl.Pos(), f.Decl.Params[i].Name)
		}
		env.Define(f.Decl.Params[i].Name, args[ai])
	}
	if f.Decl.Body == nil {
		return nil, fmt.Errorf("%s: function `%s` has no body", f.Decl.Pos(), f.Decl.Name)
	}
	v, err := in.evalBlock(f.Decl.Body, env)
	if err != nil {
		if rs, ok := err.(*returnSignal); ok {
			return rs.V, nil
		}
		return nil, err
	}
	return v, nil
}

func (in *Interpreter) callLambda(l *LambdaV, args []Value) (Value, error) {
	env := NewEnv(l.Closure)
	for i, p := range l.Lam.Params {
		if i >= len(args) {
			return nil, fmt.Errorf("missing argument for `%s`", p.Name)
		}
		env.Define(p.Name, args[i])
	}
	v, err := in.eval(l.Lam.Body, env)
	if err != nil {
		if rs, ok := err.(*returnSignal); ok {
			return rs.V, nil
		}
		return nil, err
	}
	return v, nil
}

func (in *Interpreter) evalMethodCall(mc *ast.MethodCall, env *Env) (Value, error) {
	recv, err := in.eval(mc.Recv, env)
	if err != nil {
		return nil, err
	}
	args := make([]Value, len(mc.Args))
	for i, a := range mc.Args {
		v, err := in.eval(a, env)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	// auto-deref &T to T for method resolution
	if r, ok := recv.(*RefV); ok {
		recv = *r.To
	}
	tname := valueTypeName(recv)
	if methods, ok := in.methods[tname]; ok {
		if fn, ok := methods[mc.Method]; ok {
			return in.callFn(&FnV{Decl: fn, Closure: in.globals, Self: recv}, args)
		}
	}
	// builtin methods
	if b, ok := builtinMethod(recv, mc.Method); ok {
		return b(args)
	}
	return nil, fmt.Errorf("%s: no method `%s` on %s", mc.Pos(), mc.Method, tname)
}

func (in *Interpreter) evalFieldAccess(fa *ast.FieldAccess, env *Env) (Value, error) {
	recv, err := in.eval(fa.X, env)
	if err != nil {
		return nil, err
	}
	if r, ok := recv.(*RefV); ok {
		recv = *r.To
	}
	switch x := recv.(type) {
	case *StructV:
		if v, ok := x.Fields[fa.Name]; ok {
			return v, nil
		}
		return nil, fmt.Errorf("%s: no field `%s` on struct `%s`", fa.Pos(), fa.Name, x.Name)
	case *TupleV:
		// numeric index
		var i int
		_, err := fmt.Sscanf(fa.Name, "%d", &i)
		if err == nil && i >= 0 && i < len(x.Elems) {
			return x.Elems[i], nil
		}
		return nil, fmt.Errorf("%s: bad tuple index `%s`", fa.Pos(), fa.Name)
	case *EnumV:
		if v, ok := x.Fields[fa.Name]; ok {
			return v, nil
		}
	}
	return nil, fmt.Errorf("%s: cannot access field `%s` on %T", fa.Pos(), fa.Name, recv)
}

func (in *Interpreter) evalIndex(ix *ast.IndexExpr, env *Env) (Value, error) {
	c, err := in.eval(ix.X, env)
	if err != nil {
		return nil, err
	}
	i, err := in.eval(ix.I, env)
	if err != nil {
		return nil, err
	}
	if r, ok := c.(*RefV); ok {
		c = *r.To
	}
	iv, ok := i.(*IntV)
	if !ok {
		return nil, fmt.Errorf("%s: index must be int", ix.Pos())
	}
	switch x := c.(type) {
	case *ArrayV:
		if iv.V < 0 || int(iv.V) >= len(x.Elems) {
			return nil, fmt.Errorf("%s: index %d out of bounds (len %d)", ix.Pos(), iv.V, len(x.Elems))
		}
		return x.Elems[iv.V], nil
	case *StringV:
		if iv.V < 0 || int(iv.V) >= len(x.V) {
			return nil, fmt.Errorf("%s: string index %d out of bounds", ix.Pos(), iv.V)
		}
		return &CharV{V: rune(x.V[iv.V])}, nil
	}
	return nil, fmt.Errorf("%s: cannot index into %T", ix.Pos(), c)
}

func (in *Interpreter) evalStructLit(sl *ast.StructLit, env *Env) (Value, error) {
	name := strings.Join(sl.Path, "::")
	// enum struct variant?
	if len(sl.Path) == 2 {
		if ed, ok := in.enums[sl.Path[0]]; ok {
			for _, v := range ed.Variants {
				if v.Name == sl.Path[1] {
					ev := &EnumV{Enum: ed.Name, Variant: v.Name, Fields: map[string]Value{}}
					for _, fi := range sl.Fields {
						val, err := in.eval(fi.Value, env)
						if err != nil {
							return nil, err
						}
						ev.Fields[fi.Name] = val
					}
					return ev, nil
				}
			}
		}
	}
	// plain struct
	sd, ok := in.structs[name]
	if !ok {
		// allow unknown struct names (might be a builtin-shaped value)
		sv := &StructV{Name: name, Fields: map[string]Value{}}
		for _, fi := range sl.Fields {
			v, err := in.eval(fi.Value, env)
			if err != nil {
				return nil, err
			}
			sv.Fields[fi.Name] = v
			sv.Order = append(sv.Order, fi.Name)
		}
		return sv, nil
	}
	sv := &StructV{Name: sd.Name, Fields: map[string]Value{}}
	for _, f := range sd.Fields {
		sv.Order = append(sv.Order, f.Name)
	}
	for _, fi := range sl.Fields {
		v, err := in.eval(fi.Value, env)
		if err != nil {
			return nil, err
		}
		sv.Fields[fi.Name] = v
	}
	return sv, nil
}

func (in *Interpreter) evalMatch(m *ast.MatchExpr, env *Env) (Value, error) {
	scrut, err := in.eval(m.Scrut, env)
	if err != nil {
		return nil, err
	}
	for _, arm := range m.Arms {
		armEnv := NewEnv(env)
		if !bindPattern(arm.Pat, scrut, armEnv, in) {
			continue
		}
		if arm.Guard != nil {
			g, err := in.eval(arm.Guard, armEnv)
			if err != nil {
				return nil, err
			}
			gb, ok := g.(*BoolV)
			if !ok || !gb.V {
				continue
			}
		}
		return in.eval(arm.Body, armEnv)
	}
	return nil, fmt.Errorf("%s: non-exhaustive match on %s", m.Pos(), Show(scrut))
}

// --- Iteration ---

func iterate(v Value) ([]Value, error) {
	if r, ok := v.(*RefV); ok {
		v = *r.To
	}
	switch x := v.(type) {
	case *ArrayV:
		return x.Elems, nil
	case *TupleV:
		return x.Elems, nil
	case *StringV:
		out := make([]Value, 0, len(x.V))
		for _, r := range x.V {
			out = append(out, &CharV{V: r})
		}
		return out, nil
	case *StructV:
		// Range: struct Range { start, end }  (created by `a..b`)  -- not yet syntactic
		if start, ok1 := x.Fields["start"].(*IntV); ok1 {
			if end, ok2 := x.Fields["end"].(*IntV); ok2 {
				var out []Value
				for i := start.V; i < end.V; i++ {
					out = append(out, &IntV{V: i})
				}
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("value of type %T is not iterable", v)
}

// --- Pattern matching ---

func bindPattern(p ast.Pattern, v Value, env *Env, in *Interpreter) bool {
	if r, ok := v.(*RefV); ok {
		v = *r.To
	}
	switch pat := p.(type) {
	case *ast.WildcardPat:
		return true
	case *ast.BindPat:
		env.Define(pat.Name, v)
		return true
	case *ast.LitPat:
		lv, err := in.eval(pat.Lit, env)
		if err != nil {
			return false
		}
		return valuesEqual(lv, v)
	case *ast.TuplePat:
		tv, ok := v.(*TupleV)
		if !ok || len(tv.Elems) != len(pat.Elems) {
			return false
		}
		for i, sub := range pat.Elems {
			if !bindPattern(sub, tv.Elems[i], env, in) {
				return false
			}
		}
		return true
	case *ast.EnumPat:
		ev, ok := v.(*EnumV)
		if !ok {
			return false
		}
		// path matches if last seg is the variant; second-to-last (if present) must match enum
		var wantVariant, wantEnum string
		switch len(pat.Path) {
		case 1:
			wantVariant = pat.Path[0]
		default:
			wantEnum = pat.Path[len(pat.Path)-2]
			wantVariant = pat.Path[len(pat.Path)-1]
		}
		if ev.Variant != wantVariant {
			return false
		}
		if wantEnum != "" && ev.Enum != wantEnum {
			return false
		}
		if pat.HasTuple {
			if len(ev.Tuple) != len(pat.Tuple) {
				return false
			}
			for i, sub := range pat.Tuple {
				if !bindPattern(sub, ev.Tuple[i], env, in) {
					return false
				}
			}
		}
		return true
	case *ast.StructPat:
		// could be enum struct variant or struct
		if ev, ok := v.(*EnumV); ok {
			wantVariant := pat.Path[len(pat.Path)-1]
			if ev.Variant != wantVariant {
				return false
			}
			for _, fp := range pat.Fields {
				fv, ok := ev.Fields[fp.Name]
				if !ok {
					return false
				}
				if fp.Pat == nil {
					env.Define(fp.Name, fv)
				} else if !bindPattern(fp.Pat, fv, env, in) {
					return false
				}
			}
			return true
		}
		sv, ok := v.(*StructV)
		if !ok {
			return false
		}
		for _, fp := range pat.Fields {
			fv, ok := sv.Fields[fp.Name]
			if !ok {
				return false
			}
			if fp.Pat == nil {
				env.Define(fp.Name, fv)
			} else if !bindPattern(fp.Pat, fv, env, in) {
				return false
			}
		}
		return true
	case *ast.OrPat:
		for _, alt := range pat.Alts {
			sub := NewEnv(env)
			if bindPattern(alt, v, sub, in) {
				// promote bindings up
				for k, vp := range sub.vars {
					env.Define(k, *vp)
				}
				return true
			}
		}
		return false
	}
	return false
}

// --- Value helpers ---

func valueTypeName(v Value) string {
	switch x := v.(type) {
	case *IntV:
		return "i64"
	case *FloatV:
		return "f64"
	case *BoolV:
		return "bool"
	case *StringV:
		return "String"
	case *CharV:
		return "char"
	case *UnitV:
		return "()"
	case *TupleV:
		return "tuple"
	case *ArrayV:
		return "Vec"
	case *StructV:
		return x.Name
	case *EnumV:
		return x.Enum
	}
	return fmt.Sprintf("%T", v)
}

func valuesEqual(a, b Value) bool {
	if ra, ok := a.(*RefV); ok {
		a = *ra.To
	}
	if rb, ok := b.(*RefV); ok {
		b = *rb.To
	}
	switch x := a.(type) {
	case *IntV:
		y, ok := b.(*IntV)
		return ok && x.V == y.V
	case *FloatV:
		y, ok := b.(*FloatV)
		return ok && x.V == y.V
	case *BoolV:
		y, ok := b.(*BoolV)
		return ok && x.V == y.V
	case *StringV:
		y, ok := b.(*StringV)
		return ok && x.V == y.V
	case *CharV:
		y, ok := b.(*CharV)
		return ok && x.V == y.V
	case *UnitV:
		_, ok := b.(*UnitV)
		return ok
	case *EnumV:
		y, ok := b.(*EnumV)
		if !ok || x.Enum != y.Enum || x.Variant != y.Variant {
			return false
		}
		if len(x.Tuple) != len(y.Tuple) {
			return false
		}
		for i := range x.Tuple {
			if !valuesEqual(x.Tuple[i], y.Tuple[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func applyUnary(op string, v Value) (Value, error) {
	switch op {
	case "-":
		switch x := v.(type) {
		case *IntV:
			return &IntV{V: -x.V}, nil
		case *FloatV:
			return &FloatV{V: -x.V}, nil
		}
	case "!":
		switch x := v.(type) {
		case *BoolV:
			return &BoolV{V: !x.V}, nil
		case *IntV:
			return &IntV{V: ^x.V}, nil
		}
	}
	return nil, fmt.Errorf("unsupported unary %s on %T", op, v)
}

func applyBinary(op string, l, r Value) (Value, error) {
	// auto-deref
	if rv, ok := l.(*RefV); ok {
		l = *rv.To
	}
	if rv, ok := r.(*RefV); ok {
		r = *rv.To
	}
	// promote int + float
	li, liOk := l.(*IntV)
	ri, riOk := r.(*IntV)
	lf, lfOk := l.(*FloatV)
	rf, rfOk := r.(*FloatV)
	ls, lsOk := l.(*StringV)
	rs, rsOk := r.(*StringV)

	// strings
	if op == "+" && lsOk && rsOk {
		return &StringV{V: ls.V + rs.V}, nil
	}
	if (op == "==" || op == "!=") && lsOk && rsOk {
		eq := ls.V == rs.V
		if op == "!=" {
			eq = !eq
		}
		return &BoolV{V: eq}, nil
	}

	// numerics
	if liOk && riOk {
		switch op {
		case "+":
			return &IntV{V: li.V + ri.V}, nil
		case "-":
			return &IntV{V: li.V - ri.V}, nil
		case "*":
			return &IntV{V: li.V * ri.V}, nil
		case "/":
			if ri.V == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return &IntV{V: li.V / ri.V}, nil
		case "%":
			if ri.V == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return &IntV{V: li.V % ri.V}, nil
		case "&":
			return &IntV{V: li.V & ri.V}, nil
		case "|":
			return &IntV{V: li.V | ri.V}, nil
		case "^":
			return &IntV{V: li.V ^ ri.V}, nil
		case "<<":
			return &IntV{V: li.V << ri.V}, nil
		case ">>":
			return &IntV{V: li.V >> ri.V}, nil
		case "==":
			return &BoolV{V: li.V == ri.V}, nil
		case "!=":
			return &BoolV{V: li.V != ri.V}, nil
		case "<":
			return &BoolV{V: li.V < ri.V}, nil
		case "<=":
			return &BoolV{V: li.V <= ri.V}, nil
		case ">":
			return &BoolV{V: li.V > ri.V}, nil
		case ">=":
			return &BoolV{V: li.V >= ri.V}, nil
		}
	}
	// float arithmetic (with int promotion)
	if lfOk || rfOk {
		var a, b float64
		if lfOk {
			a = lf.V
		} else if liOk {
			a = float64(li.V)
		} else {
			return nil, fmt.Errorf("bad lhs for %s", op)
		}
		if rfOk {
			b = rf.V
		} else if riOk {
			b = float64(ri.V)
		} else {
			return nil, fmt.Errorf("bad rhs for %s", op)
		}
		switch op {
		case "+":
			return &FloatV{V: a + b}, nil
		case "-":
			return &FloatV{V: a - b}, nil
		case "*":
			return &FloatV{V: a * b}, nil
		case "/":
			return &FloatV{V: a / b}, nil
		case "==":
			return &BoolV{V: a == b}, nil
		case "!=":
			return &BoolV{V: a != b}, nil
		case "<":
			return &BoolV{V: a < b}, nil
		case "<=":
			return &BoolV{V: a <= b}, nil
		case ">":
			return &BoolV{V: a > b}, nil
		case ">=":
			return &BoolV{V: a >= b}, nil
		}
	}
	// bool ops
	lb, lbOk := l.(*BoolV)
	rb, rbOk := r.(*BoolV)
	if lbOk && rbOk {
		switch op {
		case "==":
			return &BoolV{V: lb.V == rb.V}, nil
		case "!=":
			return &BoolV{V: lb.V != rb.V}, nil
		}
	}
	// generic structural equality
	if op == "==" {
		return &BoolV{V: valuesEqual(l, r)}, nil
	}
	if op == "!=" {
		return &BoolV{V: !valuesEqual(l, r)}, nil
	}
	return nil, fmt.Errorf("unsupported binary `%s` on %T and %T", op, l, r)
}

func castValue(v Value, ty ast.Type) (Value, error) {
	name := typeName(ty)
	switch x := v.(type) {
	case *IntV:
		switch name {
		case "i8", "i16", "i32", "i64", "i128", "isize",
			"u8", "u16", "u32", "u64", "u128", "usize":
			return &IntV{V: x.V}, nil
		case "f32", "f64":
			return &FloatV{V: float64(x.V)}, nil
		case "char":
			return &CharV{V: rune(x.V)}, nil
		}
	case *FloatV:
		switch name {
		case "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize":
			return &IntV{V: int64(x.V)}, nil
		case "f32", "f64":
			return v, nil
		}
	case *CharV:
		switch name {
		case "u32", "i32", "i64", "u64", "usize", "isize":
			return &IntV{V: int64(x.V)}, nil
		}
	}
	return nil, fmt.Errorf("unsupported cast of %T to %s", v, name)
}
