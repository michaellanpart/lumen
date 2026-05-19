// Package types implements the v0.3 Lumen type checker.
//
// v0.3 extends the v0.2 typed core with:
//
//	types     : i64, f64, bool, String, unit, user structs, &T, &mut T
//	items     : fn, struct, impl (no generics, no traits yet)
//	stmts     : let (binding pattern only), expression statements
//	exprs     : literals, ident, binary/unary/call, if/else, while,
//	            return, block, assignment (=), struct literal,
//	            field access, method call, &expr
//
// Anything outside this core produces a clear error so the CLI can fall
// back to the interpreter target.
package types

import (
	"fmt"
	"sort"

	"github.com/lumen-lang/lumen/internal/ast"
)

// Kind tags the categories of types known to v0.3.
type Kind int

const (
	KUnknown Kind = iota
	KI64
	KF64
	KBool
	KString
	KUnit
	KStruct
	KRef
	KArray
	KEnum
	KFn
	KVec
	KOption
	KResult
	KMap
)

// Type is a value-typed handle. Primitive types use only Kind; KStruct
// carries a non-nil *StructTy; KEnum carries a non-nil *EnumTy; KRef
// carries a non-nil *Type plus Mut. KVec carries a non-nil *VecTy.
// KMap carries a non-nil *MapTy.
//
// Type is intentionally cheap to copy and compare. Two Types are equal if
// their Kind matches and the relevant payload matches (pointer equality for
// structs/enums, recursive equality for refs).
type Type struct {
	Kind   Kind
	Struct *StructTy
	Array  *ArrayTy
	Vec    *VecTy
	Map    *MapTy
	Option *OptionTy
	Result *ResultTy
	Enum   *EnumTy
	Fn     *FnTy
	Inner  *Type
	Mut    bool
}

// VecTy represents the type Vec<Elem> — a heap-growable dynamic array.
type VecTy struct {
	Elem Type
}

// MapTy represents the type HashMap<Key, Val>.
type MapTy struct {
	Key Type
	Val Type
}

// OptionTy represents the type Option<Elem>.
type OptionTy struct {
	Elem Type
}

// ResultTy represents the type Result<Ok, Err>.
type ResultTy struct {
	Ok  Type
	Err Type
}

// ArrayTy represents either a fixed-size array (`[N]T`) or an unsized
// array/slice-shaped type (`[]T`) used in typed-core declarations.
type ArrayTy struct {
	Elem    Type
	Len     int64
	HasLen  bool
}

// FnTy is the type-erased signature of a first-class function value. Only
// free functions are first-class today (no method values). Receiver and
// borrow-inference flags are intentionally absent — a `*FnTy` carries just
// what a caller (or a runtime builtin like http_serve_fn) needs to call
// the function through a C function pointer.
type FnTy struct {
	Params []Type
	Return Type
}

// MkFn returns the type `fn(params...) -> ret`.
func MkFn(params []Type, ret Type) Type {
	return Type{Kind: KFn, Fn: &FnTy{Params: params, Return: ret}}
}

var (
	TI64  = Type{Kind: KI64}
	TF64  = Type{Kind: KF64}
	TBool = Type{Kind: KBool}
	TStr  = Type{Kind: KString}
	TUnit = Type{Kind: KUnit}
)

// MkRef returns the type `&inner` (or `&mut inner` if mut).
func MkRef(inner Type, mut bool) Type {
	in := inner
	return Type{Kind: KRef, Inner: &in, Mut: mut}
}

// MkStruct returns the named-struct type for s.
func MkStruct(s *StructTy) Type { return Type{Kind: KStruct, Struct: s} }

// MkArray returns the array type for element type elem. If hasLen is false,
// Len is ignored and the type models an unsized array/slice shape.
func MkArray(elem Type, len int64, hasLen bool) Type {
	return Type{Kind: KArray, Array: &ArrayTy{Elem: elem, Len: len, HasLen: hasLen}}
}

// MkEnum returns the named-enum type for e.
func MkEnum(e *EnumTy) Type { return Type{Kind: KEnum, Enum: e} }

// MkVec returns the type Vec<elem>.
func MkVec(elem Type) Type { return Type{Kind: KVec, Vec: &VecTy{Elem: elem}} }

// MkMap returns the type HashMap<key, val>.
func MkMap(key, val Type) Type { return Type{Kind: KMap, Map: &MapTy{Key: key, Val: val}} }

// MkOption returns the type Option<elem>.
func MkOption(elem Type) Type { return Type{Kind: KOption, Option: &OptionTy{Elem: elem}} }

// MkResult returns the type Result<ok, err>.
func MkResult(ok, err Type) Type { return Type{Kind: KResult, Result: &ResultTy{Ok: ok, Err: err}} }

func (t Type) String() string {
	switch t.Kind {
	case KI64:
		return "i64"
	case KF64:
		return "f64"
	case KBool:
		return "bool"
	case KString:
		return "String"
	case KUnit:
		return "()"
	case KStruct:
		if t.Struct != nil {
			return t.Struct.Name
		}
		return "<struct?>"
	case KArray:
		if t.Array == nil {
			return "[?]"
		}
		if t.Array.HasLen {
			return fmt.Sprintf("[%d]%s", t.Array.Len, t.Array.Elem.String())
		}
		return "[]" + t.Array.Elem.String()
	case KEnum:
		if t.Enum != nil {
			return t.Enum.Name
		}
		return "<enum?>"
	case KRef:
		prefix := "&"
		if t.Mut {
			prefix = "&mut "
		}
		if t.Inner != nil {
			return prefix + t.Inner.String()
		}
		return prefix + "?"
	case KFn:
		if t.Fn == nil {
			return "fn(?)"
		}
		s := "fn("
		for i, p := range t.Fn.Params {
			if i > 0 {
				s += ", "
			}
			s += p.String()
		}
		s += ")"
		if t.Fn.Return.Kind != KUnit {
			s += " " + t.Fn.Return.String()
		}
		return s
	case KVec:
		if t.Vec == nil {
			return "Vec<?>"
		}
		return "Vec<" + t.Vec.Elem.String() + ">"
	case KOption:
		if t.Option == nil {
			return "Option<?>"
		}
		return "Option<" + t.Option.Elem.String() + ">"
	case KResult:
		if t.Result == nil {
			return "Result<?, ?>"
		}
		return "Result<" + t.Result.Ok.String() + ", " + t.Result.Err.String() + ">"
	case KMap:
		if t.Map == nil {
			return "HashMap<?, ?>"
		}
		return "HashMap<" + t.Map.Key.String() + ", " + t.Map.Val.String() + ">"
	}
	return "?"
}

// IsNumeric reports whether t admits arithmetic operators.
func (t Type) IsNumeric() bool { return t.Kind == KI64 || t.Kind == KF64 }

// Equal reports structural equality of two types.
func (t Type) Equal(o Type) bool {
	if t.Kind != o.Kind {
		return false
	}
	switch t.Kind {
	case KStruct:
		return t.Struct == o.Struct
	case KArray:
		if t.Array == nil || o.Array == nil {
			return t.Array == o.Array
		}
		if t.Array.HasLen != o.Array.HasLen {
			return false
		}
		if t.Array.HasLen && t.Array.Len != o.Array.Len {
			return false
		}
		return t.Array.Elem.Equal(o.Array.Elem)
	case KEnum:
		return t.Enum == o.Enum
	case KRef:
		if t.Mut != o.Mut {
			return false
		}
		if t.Inner == nil || o.Inner == nil {
			return t.Inner == o.Inner
		}
		return t.Inner.Equal(*o.Inner)
	case KFn:
		if t.Fn == nil || o.Fn == nil {
			return t.Fn == o.Fn
		}
		if len(t.Fn.Params) != len(o.Fn.Params) {
			return false
		}
		for i := range t.Fn.Params {
			if !t.Fn.Params[i].Equal(o.Fn.Params[i]) {
				return false
			}
		}
		return t.Fn.Return.Equal(o.Fn.Return)
	case KVec:
		if t.Vec == nil || o.Vec == nil {
			return t.Vec == o.Vec
		}
		return t.Vec.Elem.Equal(o.Vec.Elem)
	case KOption:
		if t.Option == nil || o.Option == nil {
			return t.Option == o.Option
		}
		return t.Option.Elem.Equal(o.Option.Elem)
	case KResult:
		if t.Result == nil || o.Result == nil {
			return t.Result == o.Result
		}
		return t.Result.Ok.Equal(o.Result.Ok) && t.Result.Err.Equal(o.Result.Err)
	case KMap:
		if t.Map == nil || o.Map == nil {
			return t.Map == o.Map
		}
		return t.Map.Key.Equal(o.Map.Key) && t.Map.Val.Equal(o.Map.Val)
	}
	return true
}

// FieldTy is one field of a struct, resolved.
type FieldTy struct {
	Name string
	Ty   Type
}

// StructTy is the resolved type of a `struct` declaration.
type StructTy struct {
	Name    string
	Fields  []FieldTy
	Methods map[string]*FnSig
	Decl    *ast.StructDecl
}

// Field returns the field with the given name and its index, or (nil, -1).
func (s *StructTy) Field(name string) (*FieldTy, int) {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return &s.Fields[i], i
		}
	}
	return nil, -1
}

// VariantTy is one variant of an enum.
type VariantTy struct {
	Name   string
	Tag    int
	IsUnit bool
	Fields []Type // tuple-variant element types (empty if IsUnit)
}

// EnumTy is the resolved type of an `enum` declaration.
type EnumTy struct {
	Name     string
	Variants []VariantTy
	Decl     *ast.EnumDecl
}

// Variant returns the variant with the given name and its index, or (nil, -1).
func (e *EnumTy) Variant(name string) (*VariantTy, int) {
	for i := range e.Variants {
		if e.Variants[i].Name == name {
			return &e.Variants[i], i
		}
	}
	return nil, -1
}

// SelfKind classifies the receiver of a method.
type SelfKind int

const (
	SelfNone   SelfKind = iota // free function
	SelfValue                  // `self`
	SelfRef                    // `&self`
	SelfRefMut                 // `&mut self`
)

// FnSig is the resolved signature of a function or method.
type FnSig struct {
	Name     string // bare method name for impls; full name for free fns
	Owner    string // "" for free fns; struct name for methods
	Self     SelfKind
	SelfName string     // receiver binding name ("self" if not set)
	Params   []ParamSig // receiver excluded
	Return   Type
	Decl     *ast.FnDecl
	// SelfBorrow is set by infer.ParamBorrow when Self == SelfValue and
	// the body never mutates the receiver. It downgrades the value receiver
	// to a "by-value but read-only" mode, letting borrowck skip the move
	// when callers invoke the method. Cbackend lowering is unchanged: the
	// receiver is still passed by value in C.
	SelfBorrow bool
}

// CName returns the symbol name used in generated C.
func (s *FnSig) CName() string {
	if s.Owner == "" {
		return "Lm_" + s.Name
	}
	return "Lm_" + s.Owner + "_" + s.Name
}

type ParamSig struct {
	Name string
	Ty   Type
	// Borrow is set by infer.ParamBorrow for non-Copy value parameters
	// (`p T` where T is a struct/enum) whose body never mutates them.
	// Borrowck consults this flag: when set, passing a value into the
	// parameter is NOT treated as a move, so callers may freely pass the
	// same variable to multiple read-only callees. Cbackend lowering is
	// unchanged: the parameter is still emitted by value in C (the
	// caller-side copy is identical to a normal pass-by-value).
	Borrow bool
}

// Capture records a single variable captured by a lambda.
type Capture struct {
	Name string
	Ty   Type
}

// Info is the result of type-checking a program.
type Info struct {
	ExprTypes map[ast.Expr]Type
	Fns       map[string]*FnSig
	Order     []*FnSig // free-fn declaration order (codegen order)
	Structs   map[string]*StructTy
	StructOrd []*StructTy // struct declaration order (codegen order)
	Enums     map[string]*EnumTy
	EnumOrd   []*EnumTy // enum declaration order (codegen order)
	Methods   []*FnSig  // method declaration order (codegen order)
	// AutoBorrow records call-site argument expressions that were
	// implicitly borrowed: the param expects `&T` (any mut) but the user
	// passed an addressable value of type `T`. Codegen wraps these in `&`.
	AutoBorrow map[ast.Expr]bool
	// LambdaCaptures maps each lambda that closes over outer variables to
	// its sorted list of captured bindings.
	LambdaCaptures map[*ast.Lambda][]Capture
}

// Check type-checks prog under the v0.3 typed-core rules. Any unsupported
// construct yields an error rather than a successful check, so callers can
// fall back to the interpreter target with confidence.
func Check(prog *ast.Program) (*Info, []error) {
	c := &checker{
		info: &Info{
			ExprTypes:      map[ast.Expr]Type{},
			Fns:            map[string]*FnSig{},
			Structs:        map[string]*StructTy{},
			Enums:          map[string]*EnumTy{},
			AutoBorrow:     map[ast.Expr]bool{},
			LambdaCaptures: map[*ast.Lambda][]Capture{},
		},
	}
	// Register builtin HTTP types: Request and Response.
	// These are pre-declared so programs using http_serve_req don't need to
	// redeclare them; their C definitions come from lumen.h. They are
	// intentionally NOT added to StructOrd so the C backend does not emit
	// duplicate struct definitions.
	reqSt := &StructTy{Name: "Request", Methods: map[string]*FnSig{}, Fields: []FieldTy{
		{Name: "method", Ty: TStr},
		{Name: "path", Ty: TStr},
		{Name: "query", Ty: TStr},
		{Name: "body", Ty: TStr},
	}}
	respSt := &StructTy{Name: "Response", Methods: map[string]*FnSig{}, Fields: []FieldTy{
		{Name: "status", Ty: TI64},
		{Name: "body", Ty: TStr},
		{Name: "content_type", Ty: TStr},
	}}
	respTy := MkStruct(respSt)
	// Response::ok(body: String) -> Response
	respSt.Methods["ok"] = &FnSig{
		Name: "ok", Owner: "Response", Self: SelfNone,
		Params: []ParamSig{{Name: "body", Ty: TStr}},
		Return: respTy,
	}
	// Response::with_status(status: i64, body: String) -> Response
	respSt.Methods["with_status"] = &FnSig{
		Name: "with_status", Owner: "Response", Self: SelfNone,
		Params: []ParamSig{{Name: "status", Ty: TI64}, {Name: "body", Ty: TStr}},
		Return: respTy,
	}
	// Response::json(body: String) -> Response  (200, application/json)
	respSt.Methods["json"] = &FnSig{
		Name: "json", Owner: "Response", Self: SelfNone,
		Params: []ParamSig{{Name: "body", Ty: TStr}},
		Return: respTy,
	}
	// Response::json_status(status: i64, body: String) -> Response
	respSt.Methods["json_status"] = &FnSig{
		Name: "json_status", Owner: "Response", Self: SelfNone,
		Params: []ParamSig{{Name: "status", Ty: TI64}, {Name: "body", Ty: TStr}},
		Return: respTy,
	}
	c.info.Structs["Request"] = reqSt
	c.info.Structs["Response"] = respSt

	// Pass 1a: register struct names (so field/method types can refer to
	// any struct, in any order).
	for _, it := range prog.Items {
		if s, ok := it.(*ast.StructDecl); ok {
			if _, dup := c.info.Structs[s.Name]; dup {
				c.errf(s.NamePos, "duplicate struct %q", s.Name)
				continue
			}
			if len(s.Generics) > 0 {
				c.errf(s.NamePos, "generic structs not supported in v0.3")
				continue
			}
			if s.IsTuple {
				c.errf(s.NamePos, "tuple structs not supported in v0.3")
				continue
			}
			st := &StructTy{Name: s.Name, Decl: s, Methods: map[string]*FnSig{}}
			c.info.Structs[s.Name] = st
			c.info.StructOrd = append(c.info.StructOrd, st)
		}
		if e, ok := it.(*ast.EnumDecl); ok {
			if _, dup := c.info.Enums[e.Name]; dup {
				c.errf(e.NamePos, "duplicate enum %q", e.Name)
				continue
			}
			if _, dup := c.info.Structs[e.Name]; dup {
				c.errf(e.NamePos, "name %q already used by a struct", e.Name)
				continue
			}
			if len(e.Generics) > 0 {
				c.errf(e.NamePos, "generic enums not supported in v0.5")
				continue
			}
			et := &EnumTy{Name: e.Name, Decl: e}
			c.info.Enums[e.Name] = et
			c.info.EnumOrd = append(c.info.EnumOrd, et)
		}
	}

	// Pass 1b: resolve struct field types.
	for _, st := range c.info.StructOrd {
		seen := map[string]bool{}
		for _, f := range st.Decl.Fields {
			if seen[f.Name] {
				c.errf(f.NamePos, "duplicate field %q in struct %q", f.Name, st.Name)
				continue
			}
			seen[f.Name] = true
			ft, err := c.resolveType(f.Ty)
			if err != nil {
				c.errs = append(c.errs, err)
				continue
			}
			st.Fields = append(st.Fields, FieldTy{Name: f.Name, Ty: ft})
		}
	}

	// Pass 1b': resolve enum variants. Tuple variants get their element
	// types resolved here; named-field variants are not supported yet.
	for _, et := range c.info.EnumOrd {
		seen := map[string]bool{}
		for i, v := range et.Decl.Variants {
			if seen[v.Name] {
				c.errf(v.NamePos, "duplicate variant %q in enum %q", v.Name, et.Name)
				continue
			}
			seen[v.Name] = true
			vt := VariantTy{Name: v.Name, Tag: i, IsUnit: v.IsUnit}
			if len(v.Fields) > 0 {
				c.errf(v.NamePos, "named-field enum variants not supported in v0.5")
				continue
			}
			for _, ty := range v.Tuple {
				ft, err := c.resolveType(ty)
				if err != nil {
					c.errs = append(c.errs, err)
					continue
				}
				vt.Fields = append(vt.Fields, ft)
			}
			et.Variants = append(et.Variants, vt)
		}
	}

	// Pass 1c: collect free fn signatures and impl method signatures.
	for _, it := range prog.Items {
		switch n := it.(type) {
		case *ast.StructDecl:
			// already handled
		case *ast.EnumDecl:
			// already handled
		case *ast.FnDecl:
			sig, err := c.declSig(n, nil)
			if err != nil {
				c.errs = append(c.errs, err)
				continue
			}
			if _, dup := c.info.Fns[sig.Name]; dup {
				c.errf(n.NamePos, "duplicate function %q", sig.Name)
				continue
			}
			c.info.Fns[sig.Name] = sig
			c.info.Order = append(c.info.Order, sig)
		case *ast.ImplBlock:
			if err := c.declImpl(n); err != nil {
				c.errs = append(c.errs, err)
			}
		default:
			c.errf(it.Pos(), "v0.3 typed core does not support %T", it)
		}
	}
	if len(c.errs) > 0 {
		return c.info, c.errs
	}

	// Pass 1.5: infer omitted return types from the function bodies so
	// callees encountered during Pass 2 have a concrete return type
	// available.
	c.inferReturns(prog)

	// Pass 2: check all bodies (free fns + methods, in the order they
	// were declared so error messages are stable).
	for _, sig := range c.info.Order {
		c.checkFn(sig)
	}
	for _, sig := range c.info.Methods {
		c.checkFn(sig)
	}
	return c.info, c.errs
}

// --- checker state ---

type checker struct {
	info         *Info
	errs         []error
	scope        []map[string]Type
	cur          *FnSig
	selfType     *StructTy // set while resolving types inside an impl block
	captureDepth int                // > 0 when inside a capturing lambda body
	captureSet   map[string]Type    // records variables captured from outer scope
}

func (c *checker) errf(pos interface{ String() string }, f string, a ...any) {
	c.errs = append(c.errs, fmt.Errorf("%s: "+f, append([]any{pos}, a...)...))
}

// declSig resolves d's signature. When owner is non-nil, d is being
// declared inside `impl owner { ... }` so `Self` resolves to owner and
// the first parameter may be a `self` receiver.
func (c *checker) declSig(d *ast.FnDecl, owner *StructTy) (*FnSig, error) {
	if len(d.Generics) > 0 {
		return nil, fmt.Errorf("%s: generics not supported in v0.3", d.NamePos)
	}
	if d.IsExtern || d.IsCompt {
		return nil, fmt.Errorf("%s: extern/comptime not supported in v0.3", d.NamePos)
	}
	prevSelf := c.selfType
	c.selfType = owner
	defer func() { c.selfType = prevSelf }()

	// Start with KUnknown so inferReturns (Pass 1.5) can detect that
	// the declaration omitted the return type. After Pass 1.5 every
	// signature has a concrete Return (defaulting to TUnit).
	sig := &FnSig{Name: d.Name, Decl: d}
	if owner != nil {
		sig.Owner = owner.Name
	}
	for i, p := range d.Params {
		if p.IsSelf {
			if owner == nil {
				return nil, fmt.Errorf("%s: `self` is only valid inside an impl block", p.NamePos)
			}
			if i != 0 {
				return nil, fmt.Errorf("%s: `self` must be the first parameter", p.NamePos)
			}
			switch {
			case p.SelfRef && p.SelfMut:
				sig.Self = SelfRefMut
			case p.SelfRef:
				sig.Self = SelfRef
			default:
				sig.Self = SelfValue
			}
			if p.Name != "" {
				sig.SelfName = p.Name
			} else {
				sig.SelfName = "self"
			}
			continue
		}
		if p.Ty == nil {
			return nil, fmt.Errorf("%s: parameter %q needs a type annotation", p.NamePos, p.Name)
		}
		t, err := c.resolveType(p.Ty)
		if err != nil {
			return nil, err
		}
		sig.Params = append(sig.Params, ParamSig{Name: p.Name, Ty: t})
	}
	if d.Return != nil {
		t, err := c.resolveType(d.Return)
		if err != nil {
			return nil, err
		}
		sig.Return = t
	}
	return sig, nil
}

func (c *checker) declImpl(b *ast.ImplBlock) error {
	if len(b.Generics) > 0 {
		return fmt.Errorf("%s: generic impls not supported in v0.3", b.StartPos)
	}
	if b.Trait != nil {
		return fmt.Errorf("%s: trait impls not supported in v0.3", b.StartPos)
	}
	forTy, err := c.resolveType(b.ForType)
	if err != nil {
		return err
	}
	if forTy.Kind != KStruct {
		return fmt.Errorf("%s: impl target must be a user struct (got %s)", b.StartPos, forTy)
	}
	owner := forTy.Struct
	for _, m := range b.Methods {
		sig, err := c.declSig(m, owner)
		if err != nil {
			c.errs = append(c.errs, err)
			continue
		}
		if _, dup := owner.Methods[sig.Name]; dup {
			c.errf(m.NamePos, "duplicate method %q on %s", sig.Name, owner.Name)
			continue
		}
		owner.Methods[sig.Name] = sig
		c.info.Methods = append(c.info.Methods, sig)
	}
	return nil
}

func (c *checker) resolveType(t ast.Type) (Type, error) {
	switch t := t.(type) {
	case *ast.NamedType:
		if len(t.Args) > 0 {
			// Supported generic types in v0.3: Vec<T>, Option<T>, Result<T, E>.
			if len(t.Path) == 1 {
				switch t.Path[0] {
				case "Vec":
					if len(t.Args) != 1 {
						return Type{}, fmt.Errorf("%s: Vec requires exactly 1 type argument", t.Pos())
					}
					elem, err := c.resolveType(t.Args[0])
					if err != nil {
						return Type{}, err
					}
					return MkVec(elem), nil
				case "Option":
					if len(t.Args) != 1 {
						return Type{}, fmt.Errorf("%s: Option requires exactly 1 type argument", t.Pos())
					}
					elem, err := c.resolveType(t.Args[0])
					if err != nil {
						return Type{}, err
					}
					return MkOption(elem), nil
				case "Result":
					if len(t.Args) != 2 {
						return Type{}, fmt.Errorf("%s: Result requires exactly 2 type arguments", t.Pos())
					}
					ok, err := c.resolveType(t.Args[0])
					if err != nil {
						return Type{}, err
					}
					errT, err2 := c.resolveType(t.Args[1])
					if err2 != nil {
						return Type{}, err2
					}
					return MkResult(ok, errT), nil
				case "HashMap":
					if len(t.Args) != 2 {
						return Type{}, fmt.Errorf("%s: HashMap requires exactly 2 type arguments", t.Pos())
					}
					key, err := c.resolveType(t.Args[0])
					if err != nil {
						return Type{}, err
					}
					val, err2 := c.resolveType(t.Args[1])
					if err2 != nil {
						return Type{}, err2
					}
					return MkMap(key, val), nil
				}
			}
			return Type{}, fmt.Errorf("%s: generic type arguments not supported in v0.3 (only Vec<T>, Option<T>, Result<T,E>, HashMap<K,V> are allowed)", t.Pos())
		}
		if len(t.Path) != 1 {
			return Type{}, fmt.Errorf("%s: path types not supported in v0.3", t.Pos())
		}
		name := t.Path[0]
		switch name {
		case "i64", "int64", "int":
			return TI64, nil
		case "f64", "float64":
			return TF64, nil
		case "bool":
			return TBool, nil
		case "String", "str", "string":
			return TStr, nil
		case "unit", "()":
			return TUnit, nil
		case "Self":
			if c.selfType == nil {
				return Type{}, fmt.Errorf("%s: `Self` is only valid inside an impl block", t.Pos())
			}
			return MkStruct(c.selfType), nil
		}
		if s, ok := c.info.Structs[name]; ok {
			return MkStruct(s), nil
		}
		if e, ok := c.info.Enums[name]; ok {
			return MkEnum(e), nil
		}
		return Type{}, fmt.Errorf("%s: unknown type %q", t.Pos(), name)
	case *ast.FnType:
		params := make([]Type, len(t.Params))
		for i, p := range t.Params {
			pt, err := c.resolveType(p)
			if err != nil {
				return Type{}, err
			}
			params[i] = pt
		}
		ret := TUnit
		if t.Return != nil {
			rt, err := c.resolveType(t.Return)
			if err != nil {
				return Type{}, err
			}
			ret = rt
		}
		return MkFn(params, ret), nil
	case *ast.RefType:
		inner, err := c.resolveType(t.Inner)
		if err != nil {
			return Type{}, err
		}
		return MkRef(inner, t.Mut), nil
	case *ast.ArrayType:
		elem, err := c.resolveType(t.Elem)
		if err != nil {
			return Type{}, err
		}
		if t.Size == nil {
			return MkArray(elem, 0, false), nil
		}
		il, ok := t.Size.(*ast.IntLit)
		if !ok {
			return Type{}, fmt.Errorf("%s: array size must be an integer literal in v0.3", t.Pos())
		}
		if il.Value < 0 {
			return Type{}, fmt.Errorf("%s: array size must be >= 0", t.Pos())
		}
		return MkArray(elem, il.Value, true), nil
	}
	return Type{}, fmt.Errorf("%s: unsupported type expression %T in v0.3", t.Pos(), t)
}

// --- scope helpers ---

func (c *checker) push()                    { c.scope = append(c.scope, map[string]Type{}) }
func (c *checker) pop()                     { c.scope = c.scope[:len(c.scope)-1] }
func (c *checker) bind(name string, t Type) { c.scope[len(c.scope)-1][name] = t }
func (c *checker) lookup(name string) (Type, bool) {
	for i := len(c.scope) - 1; i >= 0; i-- {
		if t, ok := c.scope[i][name]; ok {
			// If we're inside a lambda body and this binding comes from
			// the outer scope (below the capture depth mark), record it.
			if c.captureSet != nil && i < c.captureDepth {
				c.captureSet[name] = t
			}
			return t, true
		}
	}
	return Type{}, false
}

// --- checking ---

func (c *checker) checkFn(sig *FnSig) {
	c.cur = sig
	if sig.Owner != "" {
		c.selfType = c.info.Structs[sig.Owner]
	} else {
		c.selfType = nil
	}
	c.scope = nil
	c.push()
	if sig.Self != SelfNone {
		owner := c.info.Structs[sig.Owner]
		var selfTy Type
		switch sig.Self {
		case SelfValue:
			selfTy = MkStruct(owner)
		case SelfRef:
			selfTy = MkRef(MkStruct(owner), false)
		case SelfRefMut:
			selfTy = MkRef(MkStruct(owner), true)
		}
		c.bind(sig.SelfName, selfTy)
	}
	for _, p := range sig.Params {
		c.bind(p.Name, p.Ty)
	}
	bodyTy := c.checkBlock(sig.Decl.Body)
	c.pop()
	if !assignable(bodyTy, sig.Return) {
		c.errf(sig.Decl.NamePos,
			"fn %q declared to return %s but body has type %s",
			sig.Name, sig.Return, bodyTy)
	}
}

func (c *checker) checkBlock(b *ast.Block) Type {
	c.push()
	defer c.pop()
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	if b.Tail != nil {
		return c.checkExpr(b.Tail)
	}
	return TUnit
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.LetStmt:
		bp, ok := s.Pattern.(*ast.BindPat)
		if !ok {
			c.errf(s.StartPos, "v0.3 typed core only supports identifier patterns in `let`")
			return
		}
		var declared Type
		if s.Ty != nil {
			t, err := c.resolveType(s.Ty)
			if err != nil {
				c.errs = append(c.errs, err)
				return
			}
			declared = t
		}
		got := c.checkExpr(s.Value)
		// Vec::new() returns Vec<unit> as a sentinel when no explicit type arg
		// was provided. If the binding has a declared Vec<T> annotation, unify:
		// overwrite the expression's recorded type so the C backend sees the
		// concrete element type.
		if got.Kind == KVec && got.Vec != nil && got.Vec.Elem.Kind == KUnit &&
			declared.Kind == KVec && declared.Vec != nil && declared.Vec.Elem.Kind != KUnit {
			c.info.ExprTypes[s.Value] = declared
			got = declared
		}
		// Option::None returns Option<unit> as a sentinel. Unify with declared Option<T>.
		if got.Kind == KOption && got.Option != nil && got.Option.Elem.Kind == KUnit &&
			declared.Kind == KOption && declared.Option != nil && declared.Option.Elem.Kind != KUnit {
			c.info.ExprTypes[s.Value] = declared
			got = declared
		}
		// Result sentinels: Ok(x) → Result<T, unit>, Err(e) → Result<unit, E>.
		// Fill in the unit placeholders from the declared annotation.
		if got.Kind == KResult && declared.Kind == KResult &&
			got.Result != nil && declared.Result != nil {
			newOk := got.Result.Ok
			newErr := got.Result.Err
			changed := false
			if newOk.Kind == KUnit && declared.Result.Ok.Kind != KUnit {
				newOk = declared.Result.Ok
				changed = true
			}
			if newErr.Kind == KUnit && declared.Result.Err.Kind != KUnit {
				newErr = declared.Result.Err
				changed = true
			}
			if changed {
				unified := MkResult(newOk, newErr)
				c.info.ExprTypes[s.Value] = unified
				got = unified
			}
		}
		// HashMap::new() returns HashMap<unit,unit> as a sentinel. Unify with declared HashMap<K,V>.
		if got.Kind == KMap && got.Map != nil &&
			got.Map.Key.Kind == KUnit && got.Map.Val.Kind == KUnit &&
			declared.Kind == KMap && declared.Map != nil &&
			(declared.Map.Key.Kind != KUnit || declared.Map.Val.Kind != KUnit) {
			c.info.ExprTypes[s.Value] = declared
			got = declared
		}
		if declared.Kind != KUnknown && !assignable(got, declared) {
			c.errf(s.StartPos, "let %s: %s = expr of type %s", bp.Name, declared, got)
		}
		final := declared
		if final.Kind == KUnknown {
			final = got
		}
		c.bind(bp.Name, final)
	case *ast.ExprStmt:
		c.checkExpr(s.X)
	default:
		c.errf(s.Pos(), "v0.3 typed core does not support %T", s)
	}
}

func (c *checker) checkExpr(e ast.Expr) Type {
	t := c.synthExpr(e)
	c.info.ExprTypes[e] = t
	return t
}

func (c *checker) synthExpr(e ast.Expr) Type {
	switch e := e.(type) {
	case *ast.IntLit:
		return TI64
	case *ast.FloatLit:
		return TF64
	case *ast.BoolLit:
		return TBool
	case *ast.StringLit:
		return TStr
	case *ast.UnitLit:
		return TUnit
	case *ast.Path:
		return c.checkPathExpr(e)
	case *ast.Ident:
		if t, ok := c.lookup(e.Name); ok {
			return t
		}
		if sig, ok := c.info.Fns[e.Name]; ok {
			// First-class function value: only free functions are
			// first-class today. Method values (`p.distance_sq` without
			// the call parens) remain unsupported.
			if sig.Owner != "" {
				c.errf(e.NamePos, "method values not supported (write a wrapper free fn)")
				return TUnit
			}
			params := make([]Type, len(sig.Params))
			for i, p := range sig.Params {
				params[i] = p.Ty
			}
			return MkFn(params, sig.Return)
		}
		c.errf(e.NamePos, "unresolved identifier %q", e.Name)
		return TUnit

	case *ast.Lambda:
		params := make([]Type, len(e.Params))
		for i, p := range e.Params {
			pt, err := c.resolveType(p.Ty)
			if err != nil {
				c.errs = append(c.errs, err)
				pt = TUnit
			}
			params[i] = pt
		}

		// Check body with outer scope visible so captures are detected,
		// but mark the boundary so lookup can record them.
		savedFn := c.cur
		savedSelf := c.selfType
		savedDepth := c.captureDepth
		savedSet := c.captureSet

		c.captureDepth = len(c.scope)
		c.captureSet = map[string]Type{}

		declaredRet := Type{Kind: KUnknown}
		hasDeclaredRet := false
		if e.Return != nil {
			rt, err := c.resolveType(e.Return)
			if err != nil {
				c.errs = append(c.errs, err)
				rt = TUnit
			}
			declaredRet = rt
			hasDeclaredRet = true
			c.cur = &FnSig{Return: rt}
		} else {
			c.cur = nil
		}
		c.selfType = nil

		c.push()
		for i, p := range e.Params {
			c.bind(p.Name, params[i])
		}
		bodyTy := c.checkExpr(e.Body)
		c.pop()

		// Collect captures in deterministic order.
		captures := make([]Capture, 0, len(c.captureSet))
		for name, ty := range c.captureSet {
			captures = append(captures, Capture{Name: name, Ty: ty})
		}
		sort.Slice(captures, func(i, j int) bool { return captures[i].Name < captures[j].Name })
		if len(captures) > 0 {
			c.info.LambdaCaptures[e] = captures
		}

		c.cur = savedFn
		c.selfType = savedSelf
		c.captureDepth = savedDepth
		c.captureSet = savedSet

		ret := bodyTy
		if hasDeclaredRet {
			// Block-bodied lambdas may rely on explicit `return` statements,
			// in which case the block value is unit while returns are checked
			// against c.cur above.
			if _, isBlock := e.Body.(*ast.Block); !(isBlock && bodyTy.Kind == KUnit) {
				if !assignable(bodyTy, declaredRet) {
					c.errf(e.StartPos, "lambda body has type %s but declared return type is %s", bodyTy, declaredRet)
				}
			}
			ret = declaredRet
		}
		return MkFn(params, ret)

	case *ast.Binary:
		lt := c.checkExpr(e.L)
		rt := c.checkExpr(e.R)
		switch e.Op {
		case "+", "-", "*", "/", "%":
			// String + anything: auto-coerce rhs to string, return String.
			if lt.Kind == KString {
				switch rt.Kind {
				case KString, KI64, KF64, KBool:
					return TStr
				default:
					c.errf(e.OpPos, "cannot concatenate String with %s", rt)
					return TStr
				}
			}
			if e.Op == "+" && rt.Kind == KString {
				c.errf(e.OpPos, "use String + ... not ... + String for concatenation")
				return TStr
			}
			if !lt.Equal(rt) || !lt.IsNumeric() {
				c.errf(e.OpPos, "operator %q wants numeric operands of the same type (got %s, %s)", e.Op, lt, rt)
				return TI64
			}
			return lt
		case "==", "!=":
			if !lt.Equal(rt) {
				c.errf(e.OpPos, "operator %q wants equal types (got %s, %s)", e.Op, lt, rt)
			}
			return TBool
		case "<", "<=", ">", ">=":
			if !lt.Equal(rt) || !lt.IsNumeric() {
				c.errf(e.OpPos, "operator %q wants numeric operands of the same type (got %s, %s)", e.Op, lt, rt)
			}
			return TBool
		case "&&", "||":
			if lt.Kind != KBool || rt.Kind != KBool {
				c.errf(e.OpPos, "operator %q wants bool operands (got %s, %s)", e.Op, lt, rt)
			}
			return TBool
		}
		c.errf(e.OpPos, "operator %q not supported in v0.3", e.Op)
		return TUnit

	case *ast.Unary:
		xt := c.checkExpr(e.X)
		switch e.Op {
		case "-":
			if !xt.IsNumeric() {
				c.errf(e.OpPos, "unary - wants a numeric operand (got %s)", xt)
			}
			return xt
		case "!":
			if xt.Kind != KBool {
				c.errf(e.OpPos, "unary ! wants a bool operand (got %s)", xt)
			}
			return TBool
		}
		c.errf(e.OpPos, "unary operator %q not supported in v0.3", e.Op)
		return TUnit

	case *ast.Call:
		return c.checkCall(e)

	case *ast.MethodCall:
		return c.checkMethodCall(e)

	case *ast.FieldAccess:
		recv := c.checkExpr(e.X)
		st, ok := structOf(recv)
		if !ok {
			c.errf(e.StartPos, "cannot access field %q on non-struct type %s", e.Name, recv)
			return TUnit
		}
		f, _ := st.Field(e.Name)
		if f == nil {
			c.errf(e.StartPos, "struct %s has no field %q", st.Name, e.Name)
			return TUnit
		}
		return f.Ty

	case *ast.ArrayLit:
		if len(e.Elems) == 0 {
			c.errf(e.StartPos, "empty array literal requires an explicit array type annotation")
			return MkArray(TUnit, 0, true)
		}
		elemTy := c.checkExpr(e.Elems[0])
		for i := 1; i < len(e.Elems); i++ {
			t := c.checkExpr(e.Elems[i])
			if !assignable(t, elemTy) {
				c.errf(e.StartPos, "array literal element %d: expected %s, got %s", i, elemTy, t)
			}
		}
		return MkArray(elemTy, int64(len(e.Elems)), true)

	case *ast.IndexExpr:
		x := c.checkExpr(e.X)
		it := c.checkExpr(e.I)
		if !assignable(it, TI64) {
			c.errf(e.StartPos, "array/vec index must be i64 (got %s)", it)
		}
		// Vec<T>[i] → T
		if x.Kind == KVec {
			if x.Vec == nil {
				c.errf(e.StartPos, "cannot index untyped Vec")
				return TUnit
			}
			return x.Vec.Elem
		}
		arr, ok := arrayOf(x)
		if !ok {
			c.errf(e.StartPos, "cannot index non-array/non-vec type %s", x)
			return TUnit
		}
		return arr.Elem

	case *ast.StructLit:
		return c.checkStructLit(e)

	case *ast.RefExpr:
		inner := c.checkExpr(e.X)
		return MkRef(inner, e.Mut)

	case *ast.IfExpr:
		ct := c.checkExpr(e.Cond)
		if ct.Kind != KBool {
			c.errf(e.StartPos, "if condition must be bool (got %s)", ct)
		}
		tt := c.checkBlock(e.Then)
		if e.Else == nil {
			return TUnit
		}
		var et Type
		switch x := e.Else.(type) {
		case *ast.Block:
			et = c.checkBlock(x)
		case *ast.IfExpr:
			et = c.checkExpr(x)
		default:
			c.errf(e.StartPos, "unsupported else branch %T", e.Else)
			return TUnit
		}
		if !tt.Equal(et) {
			if tt.Kind == KUnit {
				return et
			}
			if et.Kind == KUnit {
				return tt
			}
			c.errf(e.StartPos, "if branches have incompatible types (%s vs %s)", tt, et)
		}
		return tt

	case *ast.WhileExpr:
		ct := c.checkExpr(e.Cond)
		if ct.Kind != KBool {
			c.errf(e.StartPos, "while condition must be bool (got %s)", ct)
		}
		c.checkBlock(e.Body)
		return TUnit

	case *ast.ForExpr:
		iterTy := c.checkExpr(e.Iter)
		var elemTy Type
		switch iterTy.Kind {
		case KVec:
			if iterTy.Vec != nil {
				elemTy = iterTy.Vec.Elem
			} else {
				elemTy = TUnit
			}
		case KArray:
			if iterTy.Array != nil {
				elemTy = iterTy.Array.Elem
			} else {
				elemTy = TUnit
			}
		default:
			c.errf(e.StartPos, "for-in requires Vec<T> or array, got %s", iterTy)
			elemTy = TUnit
		}
		// Bind the pattern variable(s) in the body scope.
		c.push()
		bp, ok := e.Pat.(*ast.BindPat)
		if ok {
			c.bind(bp.Name, elemTy)
		}
		c.checkBlock(e.Body)
		c.pop()
		return TUnit

	case *ast.Block:
		return c.checkBlock(e)

	case *ast.MatchExpr:
		return c.checkMatch(e)

	case *ast.ReturnExpr:
		var got Type = TUnit
		if e.X != nil {
			got = c.checkExpr(e.X)
		}
		if c.cur != nil && !assignable(got, c.cur.Return) {
			c.errf(e.StartPos, "return %s from fn returning %s", got, c.cur.Return)
		}
		// `return` is divergent; satisfy any expected type by reporting the
		// enclosing function's return type as our value.
		if c.cur != nil {
			return c.cur.Return
		}
		return TUnit

	case *ast.TryExpr:
		xTy := c.checkExpr(e.X)
		if c.cur == nil {
			c.errf(e.StartPos, "`?` used outside a function body")
			return TUnit
		}
		switch xTy.Kind {
		case KOption:
			if xTy.Option == nil {
				c.errf(e.StartPos, "`?` on malformed Option type")
				return TUnit
			}
			if c.cur.Return.Kind != KOption {
				c.errf(e.StartPos, "`?` on Option requires enclosing function to return Option<...> (got %s)", c.cur.Return)
				return xTy.Option.Elem
			}
			return xTy.Option.Elem
		case KResult:
			if xTy.Result == nil {
				c.errf(e.StartPos, "`?` on malformed Result type")
				return TUnit
			}
			if c.cur.Return.Kind != KResult || c.cur.Return.Result == nil {
				c.errf(e.StartPos, "`?` on Result requires enclosing function to return Result<..., ...> (got %s)", c.cur.Return)
				return xTy.Result.Ok
			}
			if !assignable(xTy.Result.Err, c.cur.Return.Result.Err) {
				c.errf(e.StartPos, "`?` error type mismatch: %s is not assignable to function Result error %s",
					xTy.Result.Err, c.cur.Return.Result.Err)
			}
			return xTy.Result.Ok
		default:
			c.errf(e.StartPos, "`?` requires Option<T> or Result<T,E>, got %s", xTy)
			return TUnit
		}

	case *ast.AssignExpr:
		if e.Op != "=" {
			c.errf(e.OpPos, "compound assignment %q not supported in v0.3", e.Op)
			return TUnit
		}
		// Compute the lvalue's type. v0.3 accepts:
		//   - local variable: `x = ...`
		//   - field write:    `place.field = ...`  (place may be a struct value or &mut ref)
		//   - index write:    `arr[i] = ...`
		var lt Type
		switch l := e.L.(type) {
		case *ast.Ident:
			t, ok := c.lookup(l.Name)
			if !ok {
				c.errf(e.OpPos, "unknown variable %q", l.Name)
				return TUnit
			}
			lt = t
		case *ast.FieldAccess:
			lt = c.checkExpr(l) // this records the field's type and validates the receiver
		case *ast.IndexExpr:
			lt = c.checkExpr(l)
		default:
			c.errf(e.OpPos, "v0.3 only supports assignment to a variable, field, or index")
			return TUnit
		}
		rt := c.checkExpr(e.R)
		if !assignable(rt, lt) {
			c.errf(e.OpPos, "cannot assign %s to %s", rt, lt)
		}
		return TUnit
	}

	c.errf(e.Pos(), "v0.3 typed core does not support %T", e)
	return TUnit
}

func (c *checker) checkCall(e *ast.Call) Type {
	// Path-based call: `Type::assoc_fn(args)` resolves to a struct's
	// associated function (impl method with no self receiver). It may
	// also be an enum tuple-variant constructor: `E::V(a, b)`.
	if p, ok := e.Callee.(*ast.Path); ok {
		if len(p.Segments) == 2 {
			if et, ok := c.info.Enums[p.Segments[0]]; ok {
				return c.checkVariantCtor(e, et, p.Segments[1])
			}
		}
		return c.checkAssocCall(e, p)
	}
	id, ok := e.Callee.(*ast.Ident)
	if ok {
		switch id.Name {
	case "println", "print":
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if !isPrintable(at) {
				c.errf(e.StartPos, "%s cannot print value of type %s", id.Name, at)
			}
		}
		return TUnit
	case "http_serve":
		// Builtin: http_serve(host: str, port: i64, body: str) -> unit.
		// Lowered to Lm_http_serve in the runtime header.
		if len(e.Args) != 3 {
			c.errf(e.StartPos, "http_serve expects (host, port, body) — got %d args", len(e.Args))
		}
		wants := []Type{TStr, TI64, TStr}
		for i, a := range e.Args {
			at := c.checkExpr(a)
			if i < len(wants) && !assignable(at, wants[i]) {
				c.errf(e.StartPos, "http_serve arg %d: expected %s, got %s", i, wants[i], at)
			}
		}
		return TUnit
	case "io_setbuf":
		// Builtin: io_setbuf(size: i64) -> unit. Switches stdout to a
		// fully-buffered allocation of `size` bytes. Lowered to Lm_io_setbuf.
		if len(e.Args) != 1 {
			c.errf(e.StartPos, "io_setbuf expects (size_bytes) — got %d args", len(e.Args))
		}
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if !assignable(at, TI64) {
				c.errf(e.StartPos, "io_setbuf arg: expected i64, got %s", at)
			}
		}
		return TUnit
	case "parse_int":
		// Builtin: parse_int(s: String) -> Option<i64>. Tries to parse s as a
		// decimal integer; returns Option::None on failure. Lowered to Lm_parse_int.
		if len(e.Args) != 1 {
			c.errf(e.StartPos, "parse_int expects (s: String) — got %d args", len(e.Args))
		}
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if !assignable(at, TStr) {
				c.errf(e.StartPos, "parse_int arg: expected String, got %s", at)
			}
		}
		return MkOption(TI64)
	case "fmt":
		// Builtin: fmt(template: String, args...) -> String.
		// Template uses {} as a placeholder (one per arg). Each arg may be
		// String, i64, f64, or bool. Lowered to Lm_fmt in the runtime.
		if len(e.Args) == 0 {
			c.errf(e.StartPos, "fmt expects at least one argument (the template string)")
			return TStr
		}
		tmplT := c.checkExpr(e.Args[0])
		if !assignable(tmplT, TStr) {
			c.errf(e.StartPos, "fmt arg 0 (template): expected String, got %s", tmplT)
		}
		for i, a := range e.Args[1:] {
			at := c.checkExpr(a)
			switch at.Kind {
			case KString, KI64, KF64, KBool:
				// ok
			default:
				c.errf(e.StartPos, "fmt arg %d: cannot format value of type %s", i+1, at)
			}
		}
		return TStr
	case "len":
		if len(e.Args) != 1 {
			c.errf(e.StartPos, "len expects exactly 1 argument (got %d)", len(e.Args))
		}
		if len(e.Args) == 0 {
			return TI64
		}
		at := c.checkExpr(e.Args[0])
		switch at.Kind {
		case KString:
			return TI64
		case KArray:
			return TI64
		default:
			c.errf(e.StartPos, "len expects String or array (got %s)", at)
			return TI64
		}
	case "http_serve_req":
		// Builtin: http_serve_req(host, port, handler) -> unit.
		// handler is fn(Request) -> Response; the runtime parses each HTTP
		// request into a Request struct, calls handler, and formats the
		// Response (status, body, content_type) into a proper HTTP reply.
		if len(e.Args) != 3 {
			c.errf(e.StartPos, "http_serve_req expects (host, port, handler) — got %d args", len(e.Args))
			for _, a := range e.Args {
				c.checkExpr(a)
			}
			return TUnit
		}
		hostT := c.checkExpr(e.Args[0])
		if !assignable(hostT, TStr) {
			c.errf(e.StartPos, "http_serve_req arg 0 (host): expected String, got %s", hostT)
		}
		portT := c.checkExpr(e.Args[1])
		if !assignable(portT, TI64) {
			c.errf(e.StartPos, "http_serve_req arg 1 (port): expected i64, got %s", portT)
		}
		handlerT := c.checkExpr(e.Args[2])
		if handlerT.Kind != KFn || handlerT.Fn == nil {
			c.errf(e.StartPos, "http_serve_req arg 2 (handler): expected fn(Request) -> Response, got %s", handlerT)
			return TUnit
		}
		if len(handlerT.Fn.Params) != 1 {
			c.errf(e.StartPos, "http_serve_req handler must take exactly one parameter (Request); got %d", len(handlerT.Fn.Params))
			return TUnit
		}
		reqSt2, hasReq := c.info.Structs["Request"]
		respSt2, hasResp := c.info.Structs["Response"]
		if !hasReq || !hasResp {
			c.errf(e.StartPos, "http_serve_req requires Request and Response types to be in scope")
			return TUnit
		}
		if !assignable(handlerT.Fn.Params[0], MkStruct(reqSt2)) {
			c.errf(e.StartPos, "http_serve_req handler param must be Request, got %s", handlerT.Fn.Params[0])
		}
		if !assignable(handlerT.Fn.Return, MkStruct(respSt2)) {
			c.errf(e.StartPos, "http_serve_req handler must return Response, got %s", handlerT.Fn.Return)
		}
		return TUnit
	case "http_serve_fn":
		// Builtin: http_serve_fn(host, port, handler, &svc) -> unit.
		// `handler` is a free function `fn(&Service) string` and `&svc`
		// is an immutable borrow of the same Service type. Handler is
		// invoked per request; runtime builds the HTTP response from the
		// returned body string with a hard-coded 200 OK status.
		if len(e.Args) != 4 {
			c.errf(e.StartPos, "http_serve_fn expects (host, port, handler, &svc) — got %d args", len(e.Args))
			for _, a := range e.Args {
				c.checkExpr(a)
			}
			return TUnit
		}
		hostT := c.checkExpr(e.Args[0])
		if !assignable(hostT, TStr) {
			c.errf(e.StartPos, "http_serve_fn arg 0 (host): expected String, got %s", hostT)
		}
		portT := c.checkExpr(e.Args[1])
		if !assignable(portT, TI64) {
			c.errf(e.StartPos, "http_serve_fn arg 1 (port): expected i64, got %s", portT)
		}
		handlerT := c.checkExpr(e.Args[2])
		svcT := c.checkExpr(e.Args[3])
		// Auto-borrow plain Service value -> &Service if needed.
		if svcT.Kind != KRef {
			if c.argAssignable(e.Args[3], svcT, MkRef(svcT, false)) {
				svcT = MkRef(svcT, false)
			}
		}
		if handlerT.Kind != KFn || handlerT.Fn == nil {
			c.errf(e.StartPos, "http_serve_fn arg 2 (handler): expected fn(&Service) String, got %s", handlerT)
			return TUnit
		}
		if len(handlerT.Fn.Params) != 1 {
			c.errf(e.StartPos, "http_serve_fn handler must take exactly one parameter (&Service); got %d", len(handlerT.Fn.Params))
			return TUnit
		}
		if !assignable(handlerT.Fn.Return, TStr) {
			c.errf(e.StartPos, "http_serve_fn handler must return String; got %s", handlerT.Fn.Return)
		}
		// Handler param must agree with the svc arg type (both &Service).
		if !assignable(svcT, handlerT.Fn.Params[0]) {
			c.errf(e.StartPos, "http_serve_fn: handler param %s does not match svc arg %s",
				handlerT.Fn.Params[0], svcT)
		}
		return TUnit
		}
		if sig, ok := c.info.Fns[id.Name]; ok {
			if len(e.Args) != len(sig.Params) {
				c.errf(e.StartPos, "fn %q expects %d args, got %d", id.Name, len(sig.Params), len(e.Args))
			}
			for i, a := range e.Args {
				at := c.checkExpr(a)
				if i < len(sig.Params) && !c.argAssignable(a, at, sig.Params[i].Ty) {
					c.errf(e.StartPos, "arg %d of %q: expected %s, got %s", i, id.Name, sig.Params[i].Ty, at)
				}
			}
			return sig.Return
		}
		if ft, ok := c.lookup(id.Name); ok && ft.Kind == KFn && ft.Fn != nil {
			c.info.ExprTypes[e.Callee] = ft
			if len(e.Args) != len(ft.Fn.Params) {
				c.errf(e.StartPos, "fn value %q expects %d args, got %d", id.Name, len(ft.Fn.Params), len(e.Args))
			}
			for i, a := range e.Args {
				at := c.checkExpr(a)
				if i < len(ft.Fn.Params) && !c.argAssignable(a, at, ft.Fn.Params[i]) {
					c.errf(e.StartPos, "arg %d of %q: expected %s, got %s", i, id.Name, ft.Fn.Params[i], at)
				}
			}
			return ft.Fn.Return
		}
		c.errf(e.StartPos, "unknown function %q", id.Name)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return TUnit
	}

	calleeTy := c.checkExpr(e.Callee)
	if calleeTy.Kind != KFn || calleeTy.Fn == nil {
		c.errf(e.StartPos, "cannot call non-function value of type %s", calleeTy)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return TUnit
	}
	if len(e.Args) != len(calleeTy.Fn.Params) {
		c.errf(e.StartPos, "fn value call expects %d args, got %d", len(calleeTy.Fn.Params), len(e.Args))
	}
	for i, a := range e.Args {
		at := c.checkExpr(a)
		if i < len(calleeTy.Fn.Params) && !c.argAssignable(a, at, calleeTy.Fn.Params[i]) {
			c.errf(e.StartPos, "arg %d of fn value call: expected %s, got %s", i, calleeTy.Fn.Params[i], at)
		}
	}
	return calleeTy.Fn.Return
}

func (c *checker) checkMethodCall(e *ast.MethodCall) Type {
	recv := c.checkExpr(e.Recv)

	// Built-in String methods.
	if recv.Kind == KString {
		switch e.Method {
		case "len":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "String::len expects no arguments")
			}
			return TI64
		case "contains":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "String::contains expects 1 argument (sub: String), got %d", len(e.Args))
				return TBool
			}
			at := c.checkExpr(e.Args[0])
			if !assignable(at, TStr) {
				c.errf(e.StartPos, "String::contains: expected String, got %s", at)
			}
			return TBool
		case "starts_with":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "String::starts_with expects 1 argument (prefix: String), got %d", len(e.Args))
				return TBool
			}
			at := c.checkExpr(e.Args[0])
			if !assignable(at, TStr) {
				c.errf(e.StartPos, "String::starts_with: expected String, got %s", at)
			}
			return TBool
		case "ends_with":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "String::ends_with expects 1 argument (suffix: String), got %d", len(e.Args))
				return TBool
			}
			at := c.checkExpr(e.Args[0])
			if !assignable(at, TStr) {
				c.errf(e.StartPos, "String::ends_with: expected String, got %s", at)
			}
			return TBool
		case "trim":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "String::trim expects no arguments")
			}
			return TStr
		case "to_upper":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "String::to_upper expects no arguments")
			}
			return TStr
		case "to_lower":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "String::to_lower expects no arguments")
			}
			return TStr
		case "slice":
			if len(e.Args) != 2 {
				c.errf(e.StartPos, "String::slice expects 2 arguments (start: i64, end: i64), got %d", len(e.Args))
				return TStr
			}
			for _, a := range e.Args {
				at := c.checkExpr(a)
				if !assignable(at, TI64) {
					c.errf(e.StartPos, "String::slice: expected i64 index, got %s", at)
				}
			}
			return TStr
		case "replace":
			if len(e.Args) != 2 {
				c.errf(e.StartPos, "String::replace expects 2 arguments (from: String, to: String), got %d", len(e.Args))
				return TStr
			}
			for i, a := range e.Args {
				at := c.checkExpr(a)
				if !assignable(at, TStr) {
					c.errf(e.StartPos, "String::replace arg %d: expected String, got %s", i, at)
				}
			}
			return TStr
		case "index_of":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "String::index_of expects 1 argument (sub: String), got %d", len(e.Args))
				return MkOption(TI64)
			}
			at := c.checkExpr(e.Args[0])
			if !assignable(at, TStr) {
				c.errf(e.StartPos, "String::index_of: expected String, got %s", at)
			}
			return MkOption(TI64)
		case "split":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "String::split expects 1 argument (sep: String), got %d", len(e.Args))
				return MkVec(TStr)
			}
			at := c.checkExpr(e.Args[0])
			if !assignable(at, TStr) {
				c.errf(e.StartPos, "String::split: expected String separator, got %s", at)
			}
			return MkVec(TStr)
		default:
			c.errf(e.StartPos, "String has no method %q", e.Method)
			return TUnit
		}
	}

	// Built-in Vec<T> methods.
	if recv.Kind == KVec && recv.Vec != nil {
		elem := recv.Vec.Elem
		switch e.Method {
		case "push":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Vec::push expects 1 argument, got %d", len(e.Args))
				return TUnit
			}
			at := c.checkExpr(e.Args[0])
			if !assignable(at, elem) {
				c.errf(e.StartPos, "Vec::push: expected %s, got %s", elem, at)
			}
			return TUnit
		case "len":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Vec::len expects no arguments")
			}
			return TI64
		case "pop":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Vec::pop expects no arguments")
			}
			return elem
		case "get":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Vec::get expects 1 argument (index: i64), got %d", len(e.Args))
				return elem
			}
			it := c.checkExpr(e.Args[0])
			if !assignable(it, TI64) {
				c.errf(e.StartPos, "Vec::get: index must be i64, got %s", it)
			}
			return elem
		default:
			c.errf(e.StartPos, "Vec<%s> has no method %q", elem, e.Method)
			return TUnit
		}
	}

	// Built-in Option<T> methods.
	if recv.Kind == KOption && recv.Option != nil {
		elem := recv.Option.Elem
		switch e.Method {
		case "is_some":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Option::is_some expects no arguments")
			}
			return TBool
		case "is_none":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Option::is_none expects no arguments")
			}
			return TBool
		case "unwrap":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Option::unwrap expects no arguments")
			}
			return elem
		case "unwrap_or":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Option::unwrap_or expects 1 argument (default value), got %d", len(e.Args))
				return elem
			}
			dt := c.checkExpr(e.Args[0])
			if !assignable(dt, elem) {
				c.errf(e.StartPos, "Option::unwrap_or: default must be %s, got %s", elem, dt)
			}
			return elem
		default:
			c.errf(e.StartPos, "Option<%s> has no method %q", elem, e.Method)
			return TUnit
		}
	}

	// Built-in Result<T, E> methods.
	if recv.Kind == KResult && recv.Result != nil {
		okT := recv.Result.Ok
		errT := recv.Result.Err
		switch e.Method {
		case "is_ok":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Result::is_ok expects no arguments")
			}
			return TBool
		case "is_err":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Result::is_err expects no arguments")
			}
			return TBool
		case "unwrap":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Result::unwrap expects no arguments")
			}
			return okT
		case "unwrap_err":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Result::unwrap_err expects no arguments")
			}
			return errT
		case "unwrap_or":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Result::unwrap_or expects 1 argument (default value), got %d", len(e.Args))
				return okT
			}
			dt := c.checkExpr(e.Args[0])
			if !assignable(dt, okT) {
				c.errf(e.StartPos, "Result::unwrap_or: default must be %s, got %s", okT, dt)
			}
			return okT
		default:
			c.errf(e.StartPos, "Result<%s, %s> has no method %q", okT, errT, e.Method)
			return TUnit
		}
	}

	// Built-in HashMap<K, V> methods.
	if recv.Kind == KMap && recv.Map != nil {
		keyT := recv.Map.Key
		valT := recv.Map.Val
		switch e.Method {
		case "insert":
			if len(e.Args) != 2 {
				c.errf(e.StartPos, "HashMap::insert expects 2 arguments (key, value), got %d", len(e.Args))
				return TUnit
			}
			kt := c.checkExpr(e.Args[0])
			vt := c.checkExpr(e.Args[1])
			if !assignable(kt, keyT) {
				c.errf(e.StartPos, "HashMap::insert: key type mismatch: expected %s, got %s", keyT, kt)
			}
			if !assignable(vt, valT) {
				c.errf(e.StartPos, "HashMap::insert: value type mismatch: expected %s, got %s", valT, vt)
			}
			return TUnit
		case "get":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "HashMap::get expects 1 argument (key), got %d", len(e.Args))
				return MkOption(valT)
			}
			kt := c.checkExpr(e.Args[0])
			if !assignable(kt, keyT) {
				c.errf(e.StartPos, "HashMap::get: key type mismatch: expected %s, got %s", keyT, kt)
			}
			return MkOption(valT)
		case "contains_key":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "HashMap::contains_key expects 1 argument (key), got %d", len(e.Args))
				return TBool
			}
			kt := c.checkExpr(e.Args[0])
			if !assignable(kt, keyT) {
				c.errf(e.StartPos, "HashMap::contains_key: key type mismatch: expected %s, got %s", keyT, kt)
			}
			return TBool
		case "remove":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "HashMap::remove expects 1 argument (key), got %d", len(e.Args))
				return MkOption(valT)
			}
			kt := c.checkExpr(e.Args[0])
			if !assignable(kt, keyT) {
				c.errf(e.StartPos, "HashMap::remove: key type mismatch: expected %s, got %s", keyT, kt)
			}
			return MkOption(valT)
		case "len":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "HashMap::len expects no arguments")
			}
			return TI64
		default:
			c.errf(e.StartPos, "HashMap<%s, %s> has no method %q", keyT, valT, e.Method)
			return TUnit
		}
	}

	st, ok := structOf(recv)
	if !ok {
		c.errf(e.StartPos, "cannot call method %q on non-struct type %s", e.Method, recv)
		return TUnit
	}
	m, ok := st.Methods[e.Method]
	if !ok {
		c.errf(e.StartPos, "struct %s has no method %q", st.Name, e.Method)
		return TUnit
	}
	if m.Self == SelfNone {
		c.errf(e.StartPos, "%s::%s is an associated function, not a method", st.Name, e.Method)
		return TUnit
	}
	// Receiver shape compatibility:
	//   self      → recv must be the value type
	//   &self     → recv may be value (auto-borrow) or shared ref
	//   &mut self → recv must already be a mutable place; auto-&mut deferred
	switch m.Self {
	case SelfValue:
		if recv.Kind == KRef {
			c.errf(e.StartPos, "method %s::%s takes self by value but receiver is a reference", st.Name, e.Method)
		}
	case SelfRef:
		// always fine: value -> auto-borrow, &T -> as-is
	case SelfRefMut:
		if recv.Kind == KRef && !recv.Mut {
			c.errf(e.StartPos, "method %s::%s takes &mut self but receiver is a shared reference", st.Name, e.Method)
		}
	}
	if len(e.Args) != len(m.Params) {
		c.errf(e.StartPos, "method %s::%s expects %d args, got %d", st.Name, e.Method, len(m.Params), len(e.Args))
	}
	for i, a := range e.Args {
		at := c.checkExpr(a)
		if i < len(m.Params) && !c.argAssignable(a, at, m.Params[i].Ty) {
			c.errf(e.StartPos, "arg %d of %s::%s: expected %s, got %s", i, st.Name, e.Method, m.Params[i].Ty, at)
		}
	}
	return m.Return
}

func (c *checker) checkAssocCall(e *ast.Call, p *ast.Path) Type {
	if len(p.Segments) != 2 {
		c.errf(e.StartPos, "v0.3 only supports `Type::name` style associated calls")
		return TUnit
	}
	typeName, fnName := p.Segments[0], p.Segments[1]

	// Vec::<method> — handled before struct lookup since Vec is a builtin generic.
	if typeName == "Vec" {
		switch fnName {
		case "new":
			// Vec::new() — the element type comes from the binding's declared
			// annotation (resolved in checkStmt). Return the sentinel Vec<unit>
			// which the LetStmt unifier will replace with the annotation type.
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Vec::new() takes no arguments")
			}
			return MkVec(TUnit)
		default:
			c.errf(e.StartPos, "Vec has no associated function %q (did you mean Vec::new?)", fnName)
			return TUnit
		}
	}

	// HashMap::<method> — builtin generic hash map.
	if typeName == "HashMap" {
		switch fnName {
		case "new":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "HashMap::new() takes no arguments")
			}
			// Sentinel: key/val type resolved by LetStmt unifier.
			return MkMap(TUnit, TUnit)
		default:
			c.errf(e.StartPos, "HashMap has no associated function %q (use HashMap::new())", fnName)
			return TUnit
		}
	}

	// Option::<ctor> — builtin generic option type.
	if typeName == "Option" {
		switch fnName {
		case "None":
			if len(e.Args) != 0 {
				c.errf(e.StartPos, "Option::None takes no arguments")
			}
			// Sentinel: element type resolved by LetStmt unifier or inferred as unit.
			return MkOption(TUnit)
		case "Some":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Option::Some takes exactly 1 argument")
				return MkOption(TUnit)
			}
			elem := c.checkExpr(e.Args[0])
			return MkOption(elem)
		default:
			c.errf(e.StartPos, "Option has no associated function %q (use Option::Some or Option::None)", fnName)
			return TUnit
		}
	}

	// Result::<ctor> — builtin generic result type.
	if typeName == "Result" {
		switch fnName {
		case "Ok":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Result::Ok takes exactly 1 argument")
				return MkResult(TUnit, TUnit)
			}
			okT := c.checkExpr(e.Args[0])
			// Err type is unit sentinel, resolved by LetStmt unifier.
			return MkResult(okT, TUnit)
		case "Err":
			if len(e.Args) != 1 {
				c.errf(e.StartPos, "Result::Err takes exactly 1 argument")
				return MkResult(TUnit, TUnit)
			}
			errT := c.checkExpr(e.Args[0])
			// Ok type is unit sentinel, resolved by LetStmt unifier.
			return MkResult(TUnit, errT)
		default:
			c.errf(e.StartPos, "Result has no associated function %q (use Result::Ok or Result::Err)", fnName)
			return TUnit
		}
	}

	if typeName == "Self" {
		if c.selfType == nil {
			c.errf(e.StartPos, "`Self` used outside an impl block")
			return TUnit
		}
		typeName = c.selfType.Name
		// Rewrite the AST so later passes (codegen) see the concrete name.
		p.Segments[0] = typeName
	}
	st, ok := c.info.Structs[typeName]
	if !ok {
		c.errf(e.StartPos, "unknown type %q", typeName)
		return TUnit
	}
	m, ok := st.Methods[fnName]
	if !ok {
		c.errf(e.StartPos, "type %s has no associated function %q", typeName, fnName)
		return TUnit
	}
	if m.Self != SelfNone {
		c.errf(e.StartPos, "%s::%s is a method (takes self); call it as `recv.%s(...)`", typeName, fnName, fnName)
		return TUnit
	}
	if len(e.Args) != len(m.Params) {
		c.errf(e.StartPos, "%s::%s expects %d args, got %d", typeName, fnName, len(m.Params), len(e.Args))
	}
	for i, a := range e.Args {
		at := c.checkExpr(a)
		if i < len(m.Params) && !c.argAssignable(a, at, m.Params[i].Ty) {
			c.errf(e.StartPos, "arg %d of %s::%s: expected %s, got %s", i, typeName, fnName, m.Params[i].Ty, at)
		}
	}
	return m.Return
}

func (c *checker) checkStructLit(e *ast.StructLit) Type {
	if len(e.Path) != 1 {
		c.errf(e.StartPos, "v0.3 does not support qualified struct paths")
		return TUnit
	}
	name := e.Path[0]
	if name == "Self" {
		if c.selfType == nil {
			c.errf(e.StartPos, "`Self` used outside an impl block")
			return TUnit
		}
		name = c.selfType.Name
	}
	st, ok := c.info.Structs[name]
	if !ok {
		c.errf(e.StartPos, "unknown struct %q", name)
		return TUnit
	}
	provided := map[string]bool{}
	for _, fi := range e.Fields {
		f, _ := st.Field(fi.Name)
		if f == nil {
			c.errf(fi.NamePos, "struct %s has no field %q", st.Name, fi.Name)
			continue
		}
		if provided[fi.Name] {
			c.errf(fi.NamePos, "duplicate field %q in literal", fi.Name)
			continue
		}
		provided[fi.Name] = true
		vt := c.checkExpr(fi.Value)
		if !assignable(vt, f.Ty) {
			c.errf(fi.NamePos, "field %q: expected %s, got %s", fi.Name, f.Ty, vt)
		}
	}
	for _, f := range st.Fields {
		if !provided[f.Name] {
			c.errf(e.StartPos, "struct literal for %s missing field %q", st.Name, f.Name)
		}
	}
	return MkStruct(st)
}

// structOf returns the struct type pointed to by t (directly or through one
// level of reference).
func structOf(t Type) (*StructTy, bool) {
	switch t.Kind {
	case KStruct:
		return t.Struct, true
	case KRef:
		if t.Inner != nil && t.Inner.Kind == KStruct {
			return t.Inner.Struct, true
		}
	}
	return nil, false
}

// arrayOf returns the array type wrapped by t (directly or through one
// level of reference).
func arrayOf(t Type) (*ArrayTy, bool) {
	switch t.Kind {
	case KArray:
		return t.Array, t.Array != nil
	case KRef:
		if t.Inner != nil && t.Inner.Kind == KArray {
			return t.Inner.Array, t.Inner.Array != nil
		}
	}
	return nil, false
}

func isPrintable(t Type) bool {
	switch t.Kind {
	case KI64, KF64, KBool, KString:
		return true
	}
	return false
}

func assignable(got, want Type) bool {
	if want.Kind == KUnknown {
		return true
	}
	if got.Equal(want) {
		return true
	}
	if got.Kind == KArray && want.Kind == KArray && got.Array != nil && want.Array != nil {
		if !got.Array.Elem.Equal(want.Array.Elem) {
			return false
		}
		if !want.Array.HasLen {
			return true
		}
		if got.Array.HasLen && got.Array.Len == want.Array.Len {
			return true
		}
	}
	// Vec<unit> (sentinel from Vec::new()) is assignable to any Vec<T>.
	if got.Kind == KVec && want.Kind == KVec &&
		got.Vec != nil && want.Vec != nil && got.Vec.Elem.Kind == KUnit {
		return true
	}
	// Option<unit> (sentinel from Option::None) is assignable to any Option<T>.
	if got.Kind == KOption && want.Kind == KOption &&
		got.Option != nil && want.Option != nil && got.Option.Elem.Kind == KUnit {
		return true
	}
	// Result<T, unit> (Ok sentinel) or Result<unit, E> (Err sentinel) or
	// Result<unit, unit> is assignable to any Result<T, E>.
	if got.Kind == KResult && want.Kind == KResult &&
		got.Result != nil && want.Result != nil {
		okUnit := got.Result.Ok.Kind == KUnit
		errUnit := got.Result.Err.Kind == KUnit
		if okUnit || errUnit {
			return true
		}
	}
	// HashMap<unit,unit> (sentinel from HashMap::new()) is assignable to any HashMap<K,V>.
	if got.Kind == KMap && want.Kind == KMap &&
		got.Map != nil && want.Map != nil &&
		got.Map.Key.Kind == KUnit && got.Map.Val.Kind == KUnit {
		return true
	}
	// Subtyping: &mut T is assignable to &T (mut → shared is sound).
	if want.Kind == KRef && got.Kind == KRef && !want.Mut && got.Mut {
		if want.Inner != nil && got.Inner != nil && got.Inner.Equal(*want.Inner) {
			return true
		}
	}
	return false
}

// argAssignable extends `assignable` with v0.7 call-site auto-borrow: when
// the param expects `&T` (any mut) and the arg is an addressable value of
// type `T`, record the implicit borrow in info.AutoBorrow and accept.
func (c *checker) argAssignable(arg ast.Expr, got, want Type) bool {
	if assignable(got, want) {
		return true
	}
	if want.Kind != KRef || want.Inner == nil {
		return false
	}
	if !got.Equal(*want.Inner) {
		return false
	}
	if !isAddressable(arg) {
		return false
	}
	c.info.AutoBorrow[arg] = true
	return true
}

func isAddressable(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.FieldAccess, *ast.IndexExpr, *ast.DerefExpr:
		return true
	}
	return false
}

// --- enum paths, variant constructors, and match ---

// checkPathExpr resolves a multi-segment path used in expression position.
// In v0.5 the only supported shape is `Enum::UnitVariant`, which yields a
// value of that enum type.
func (c *checker) checkPathExpr(p *ast.Path) Type {
	if len(p.Segments) != 2 {
		c.errf(p.StartPos, "unsupported path expression %v", p.Segments)
		return TUnit
	}
	et, ok := c.info.Enums[p.Segments[0]]
	if !ok {
		c.errf(p.StartPos, "unknown enum %q", p.Segments[0])
		return TUnit
	}
	v, _ := et.Variant(p.Segments[1])
	if v == nil {
		c.errf(p.StartPos, "enum %s has no variant %q", et.Name, p.Segments[1])
		return TUnit
	}
	if !v.IsUnit {
		c.errf(p.StartPos, "variant %s::%s takes arguments; call it like %s::%s(...)",
			et.Name, v.Name, et.Name, v.Name)
		return TUnit
	}
	return MkEnum(et)
}

// checkVariantCtor type-checks a tuple-variant constructor call like
// `Option::Some(42)`.
func (c *checker) checkVariantCtor(e *ast.Call, et *EnumTy, vname string) Type {
	v, _ := et.Variant(vname)
	if v == nil {
		c.errf(e.StartPos, "enum %s has no variant %q", et.Name, vname)
		return TUnit
	}
	if v.IsUnit {
		c.errf(e.StartPos, "variant %s::%s is a unit variant; write it without parentheses",
			et.Name, vname)
		return MkEnum(et)
	}
	if len(e.Args) != len(v.Fields) {
		c.errf(e.StartPos, "variant %s::%s expects %d args, got %d",
			et.Name, vname, len(v.Fields), len(e.Args))
	}
	for i, a := range e.Args {
		at := c.checkExpr(a)
		if i < len(v.Fields) && !assignable(at, v.Fields[i]) {
			c.errf(e.StartPos, "arg %d of %s::%s: expected %s, got %s",
				i, et.Name, vname, v.Fields[i], at)
		}
	}
	return MkEnum(et)
}

// checkMatch type-checks a match expression. All arm bodies must produce
// the same type; for enum scrutinees we also require exhaustiveness
// (either every variant is named, or a wildcard/bind catches the rest).
func (c *checker) checkMatch(e *ast.MatchExpr) Type {
	scrut := c.checkExpr(e.Scrut)
	if len(e.Arms) == 0 {
		c.errf(e.StartPos, "match expression has no arms")
		return TUnit
	}

	covered := map[string]bool{}
	hasWildcard := false

	var result Type
	for i, arm := range e.Arms {
		c.push()
		if name, isVariant := variantNameOf(arm.Pat); isVariant {
			covered[name] = true
		}
		if isCatchAll(arm.Pat) {
			hasWildcard = true
		}
		c.checkPat(arm.Pat, scrut)
		if arm.Guard != nil {
			gt := c.checkExpr(arm.Guard)
			if gt.Kind != KBool {
				c.errf(arm.Pat.Pos(), "match guard must be bool (got %s)", gt)
			}
		}
		bt := c.checkExpr(arm.Body)
		c.pop()
		if i == 0 {
			result = bt
			continue
		}
		if !result.Equal(bt) {
			if result.Kind == KUnit {
				result = bt
			} else if bt.Kind != KUnit {
				c.errf(arm.Pat.Pos(), "match arms have incompatible types (%s vs %s)", result, bt)
			}
		}
	}

	// Exhaustiveness check (only for enum/Option/Result scrutinees).
	if !hasWildcard {
		switch scrut.Kind {
		case KEnum:
			for _, v := range scrut.Enum.Variants {
				if !covered[v.Name] {
					c.errf(e.StartPos, "non-exhaustive match: variant %s::%s not covered",
						scrut.Enum.Name, v.Name)
					break
				}
			}
		case KOption:
			for _, v := range []string{"Some", "None"} {
				if !covered[v] {
					c.errf(e.StartPos, "non-exhaustive match on Option: arm %q not covered", v)
					break
				}
			}
		case KResult:
			for _, v := range []string{"Ok", "Err"} {
				if !covered[v] {
					c.errf(e.StartPos, "non-exhaustive match on Result: arm %q not covered", v)
					break
				}
			}
		}
	}
	return result
}

// variantNameOf returns the variant name of a pattern of the form
// `Enum::Variant(...)` or `Enum::Variant`, including Option/Result patterns.
// The bool is false otherwise.
func variantNameOf(p ast.Pattern) (string, bool) {
	if ep, ok := p.(*ast.EnumPat); ok && len(ep.Path) == 2 {
		return ep.Path[1], true
	}
	return "", false
}

// isCatchAll reports whether p matches every value of its scrutinee type.
func isCatchAll(p ast.Pattern) bool {
	switch p.(type) {
	case *ast.WildcardPat, *ast.BindPat:
		return true
	}
	return false
}

// checkPat verifies that pat is well-typed against the scrutinee type,
// and binds any names the pattern introduces in the current scope.
func (c *checker) checkPat(pat ast.Pattern, scrut Type) {
	switch p := pat.(type) {
	case *ast.WildcardPat:
		return
	case *ast.BindPat:
		c.bind(p.Name, scrut)
	case *ast.LitPat:
		lt := c.checkExpr(p.Lit)
		if !lt.Equal(scrut) {
			c.errf(p.StartPos, "pattern literal of type %s doesn't match scrutinee of type %s", lt, scrut)
		}
	case *ast.EnumPat:
		if len(p.Path) != 2 {
			c.errf(p.StartPos, "unsupported enum pattern path %v", p.Path)
			return
		}
		// Option<T> patterns: Option::Some(x) and Option::None
		if p.Path[0] == "Option" {
			if scrut.Kind != KOption {
				c.errf(p.StartPos, "Option pattern used against non-Option scrutinee %s", scrut)
				return
			}
			switch p.Path[1] {
			case "Some":
				if len(p.Tuple) != 1 {
					c.errf(p.StartPos, "Option::Some requires exactly 1 binding")
					return
				}
				elemTy := TUnit
				if scrut.Option != nil {
					elemTy = scrut.Option.Elem
				}
				c.checkPat(p.Tuple[0], elemTy)
			case "None":
				if len(p.Tuple) != 0 {
					c.errf(p.StartPos, "Option::None takes no bindings")
				}
			default:
				c.errf(p.StartPos, "Option has no variant %q (use Some or None)", p.Path[1])
			}
			return
		}
		// Result<T,E> patterns: Result::Ok(x) and Result::Err(e)
		if p.Path[0] == "Result" {
			if scrut.Kind != KResult {
				c.errf(p.StartPos, "Result pattern used against non-Result scrutinee %s", scrut)
				return
			}
			switch p.Path[1] {
			case "Ok":
				if len(p.Tuple) != 1 {
					c.errf(p.StartPos, "Result::Ok requires exactly 1 binding")
					return
				}
				okTy := TUnit
				if scrut.Result != nil {
					okTy = scrut.Result.Ok
				}
				c.checkPat(p.Tuple[0], okTy)
			case "Err":
				if len(p.Tuple) != 1 {
					c.errf(p.StartPos, "Result::Err requires exactly 1 binding")
					return
				}
				errTy := TUnit
				if scrut.Result != nil {
					errTy = scrut.Result.Err
				}
				c.checkPat(p.Tuple[0], errTy)
			default:
				c.errf(p.StartPos, "Result has no variant %q (use Ok or Err)", p.Path[1])
			}
			return
		}
		if scrut.Kind != KEnum {
			c.errf(p.StartPos, "enum pattern used against non-enum scrutinee %s", scrut)
			return
		}
		et := scrut.Enum
		if et.Name != p.Path[0] {
			c.errf(p.StartPos, "variant %s::%s doesn't belong to enum %s", p.Path[0], p.Path[1], et.Name)
			return
		}
		v, _ := et.Variant(p.Path[1])
		if v == nil {
			c.errf(p.StartPos, "enum %s has no variant %q", et.Name, p.Path[1])
			return
		}
		if !p.HasTuple {
			if !v.IsUnit {
				c.errf(p.StartPos, "variant %s::%s carries data; bind it with `(...)`", et.Name, v.Name)
			}
			return
		}
		if v.IsUnit {
			c.errf(p.StartPos, "variant %s::%s is a unit variant; drop the `(...)`", et.Name, v.Name)
			return
		}
		if len(p.Tuple) != len(v.Fields) {
			c.errf(p.StartPos, "variant %s::%s has %d fields, pattern provides %d",
				et.Name, v.Name, len(v.Fields), len(p.Tuple))
			return
		}
		for i, sub := range p.Tuple {
			c.checkPat(sub, v.Fields[i])
		}
	default:
		c.errf(pat.Pos(), "v0.5 does not support pattern %T", pat)
	}
}
