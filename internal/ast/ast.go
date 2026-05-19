// Package ast defines the Lumen abstract syntax tree.
package ast

import "github.com/lumen-lang/lumen/internal/token"

// Node is implemented by every AST node.
type Node interface{ Pos() token.Pos }

// --- Top level ---

type Program struct {
	File  string
	Items []Item
}

// Item is a top-level declaration.
type Item interface {
	Node
	itemNode()
}

type FnDecl struct {
	NamePos  token.Pos
	Name     string
	Generics []GenericParam
	Params   []Param
	Return   Type // nil = unit
	Body     *Block
	IsPub    bool
	IsExtern bool
	IsCompt  bool
}

type StructDecl struct {
	NamePos  token.Pos
	Name     string
	Generics []GenericParam
	Fields   []Field // for tuple struct, name is "0","1",...
	IsTuple  bool
}

type EnumDecl struct {
	NamePos  token.Pos
	Name     string
	Generics []GenericParam
	Variants []Variant
}

type TraitDecl struct {
	NamePos  token.Pos
	Name     string
	Generics []GenericParam
	Methods  []*FnDecl
}

type ImplBlock struct {
	StartPos token.Pos
	Generics []GenericParam
	Trait    Type // optional
	ForType  Type
	Methods  []*FnDecl
}

type TypeAlias struct {
	NamePos  token.Pos
	Name     string
	Generics []GenericParam
	Target   Type
}

type UseDecl struct {
	StartPos token.Pos
	Path     []string
}

type ConstDecl struct {
	NamePos token.Pos
	Name    string
	Ty      Type
	Value   Expr
}

func (n *FnDecl) Pos() token.Pos     { return n.NamePos }
func (n *StructDecl) Pos() token.Pos { return n.NamePos }
func (n *EnumDecl) Pos() token.Pos   { return n.NamePos }
func (n *TraitDecl) Pos() token.Pos  { return n.NamePos }
func (n *ImplBlock) Pos() token.Pos  { return n.StartPos }
func (n *TypeAlias) Pos() token.Pos  { return n.NamePos }
func (n *UseDecl) Pos() token.Pos    { return n.StartPos }
func (n *ConstDecl) Pos() token.Pos  { return n.NamePos }

func (*FnDecl) itemNode()     {}
func (*StructDecl) itemNode() {}
func (*EnumDecl) itemNode()   {}
func (*TraitDecl) itemNode()  {}
func (*ImplBlock) itemNode()  {}
func (*TypeAlias) itemNode()  {}
func (*UseDecl) itemNode()    {}
func (*ConstDecl) itemNode()  {}

// --- Helpers ---

type GenericParam struct {
	Name   string
	Bounds []Type
}

type Param struct {
	NamePos token.Pos
	Name    string
	Ty      Type
	IsSelf  bool
	SelfRef bool // &self
	SelfMut bool // &mut self
}

type Field struct {
	NamePos token.Pos
	Name    string
	Ty      Type
	IsPub   bool
}

type Variant struct {
	NamePos token.Pos
	Name    string
	Fields  []Field // named
	Tuple   []Type  // tuple variants
	IsUnit  bool
}

// --- Types ---

type Type interface {
	Node
	typeNode()
}

type NamedType struct {
	NamePos token.Pos
	Path    []string
	Args    []Type
}

type RefType struct {
	StartPos token.Pos
	Mut      bool
	Inner    Type
}

type TupleType struct {
	StartPos token.Pos
	Elems    []Type
}

type ArrayType struct {
	StartPos token.Pos
	Elem     Type
	Size     Expr // nil = slice
}

type FnType struct {
	StartPos token.Pos
	Params   []Type
	Return   Type
}

func (t *NamedType) Pos() token.Pos { return t.NamePos }
func (t *RefType) Pos() token.Pos   { return t.StartPos }
func (t *TupleType) Pos() token.Pos { return t.StartPos }
func (t *ArrayType) Pos() token.Pos { return t.StartPos }
func (t *FnType) Pos() token.Pos    { return t.StartPos }

func (*NamedType) typeNode() {}
func (*RefType) typeNode()   {}
func (*TupleType) typeNode() {}
func (*ArrayType) typeNode() {}
func (*FnType) typeNode()    {}

// --- Statements & Expressions ---

type Stmt interface {
	Node
	stmtNode()
}

type LetStmt struct {
	StartPos token.Pos
	Pattern  Pattern
	Ty       Type // optional
	Value    Expr
	Mut      bool
}

type ExprStmt struct {
	X Expr
}

type ItemStmt struct{ It Item }

func (s *LetStmt) Pos() token.Pos  { return s.StartPos }
func (s *ExprStmt) Pos() token.Pos { return s.X.Pos() }
func (s *ItemStmt) Pos() token.Pos { return s.It.Pos() }

func (*LetStmt) stmtNode()  {}
func (*ExprStmt) stmtNode() {}
func (*ItemStmt) stmtNode() {}

type Expr interface {
	Node
	exprNode()
}

type IntLit struct {
	StartPos token.Pos
	Value    int64
	Suffix   string
}
type FloatLit struct {
	StartPos token.Pos
	Value    float64
	Suffix   string
}
type StringLit struct {
	StartPos token.Pos
	Value    string
}
type CharLit struct {
	StartPos token.Pos
	Value    rune
}
type BoolLit struct {
	StartPos token.Pos
	Value    bool
}
type UnitLit struct{ StartPos token.Pos }

type Ident struct {
	NamePos token.Pos
	Name    string
}

type Path struct {
	StartPos token.Pos
	Segments []string
}

type Binary struct {
	OpPos token.Pos
	Op    string
	L, R  Expr
}

type Unary struct {
	OpPos token.Pos
	Op    string
	X     Expr
}

type Call struct {
	StartPos token.Pos
	Callee   Expr
	Args     []Expr
}

type MethodCall struct {
	StartPos token.Pos
	Recv     Expr
	Method   string
	Args     []Expr
}

type FieldAccess struct {
	StartPos token.Pos
	X        Expr
	Name     string
}

type IndexExpr struct {
	StartPos token.Pos
	X, I     Expr
}

type AssignExpr struct {
	OpPos token.Pos
	Op    string // "=", "+=", ...
	L, R  Expr
}

type IfExpr struct {
	StartPos token.Pos
	Cond     Expr
	Then     *Block
	Else     Expr // *Block or *IfExpr or nil
}

type WhileExpr struct {
	StartPos token.Pos
	Cond     Expr
	Body     *Block
}

type ForExpr struct {
	StartPos token.Pos
	Pat      Pattern
	Iter     Expr
	Body     *Block
}

type LoopExpr struct {
	StartPos token.Pos
	Body     *Block
}

type Block struct {
	StartPos token.Pos
	Stmts    []Stmt
	Tail     Expr // optional implicit-return expression
}

type ReturnExpr struct {
	StartPos token.Pos
	X        Expr
}

type BreakExpr struct {
	StartPos token.Pos
	X        Expr
}

type ContinueExpr struct{ StartPos token.Pos }

type MatchExpr struct {
	StartPos token.Pos
	Scrut    Expr
	Arms     []MatchArm
}

type MatchArm struct {
	Pat   Pattern
	Guard Expr // optional
	Body  Expr
}

type Lambda struct {
	StartPos token.Pos
	Params   []Param
	Return   Type // optional explicit return type
	Body     Expr
	Move     bool
}

type StructLit struct {
	StartPos token.Pos
	Path     []string
	Fields   []FieldInit
}

type FieldInit struct {
	NamePos token.Pos
	Name    string
	Value   Expr
}

type TupleExpr struct {
	StartPos token.Pos
	Elems    []Expr
}

type ArrayLit struct {
	StartPos token.Pos
	Elems    []Expr
}

type RefExpr struct {
	StartPos token.Pos
	Mut      bool
	X        Expr
}

type DerefExpr struct {
	StartPos token.Pos
	X        Expr
}

type CastExpr struct {
	StartPos token.Pos
	X        Expr
	Ty       Type
}

type TryExpr struct {
	StartPos token.Pos
	X        Expr
}

type SpawnExpr struct {
	StartPos token.Pos
	Body     *Block
}

func (e *Block) Pos() token.Pos { return e.StartPos }

func (*IntLit) exprNode()       {}
func (*FloatLit) exprNode()     {}
func (*StringLit) exprNode()    {}
func (*CharLit) exprNode()      {}
func (*BoolLit) exprNode()      {}
func (*UnitLit) exprNode()      {}
func (*Ident) exprNode()        {}
func (*Path) exprNode()         {}
func (*Binary) exprNode()       {}
func (*Unary) exprNode()        {}
func (*Call) exprNode()         {}
func (*MethodCall) exprNode()   {}
func (*FieldAccess) exprNode()  {}
func (*IndexExpr) exprNode()    {}
func (*AssignExpr) exprNode()   {}
func (*IfExpr) exprNode()       {}
func (*WhileExpr) exprNode()    {}
func (*ForExpr) exprNode()      {}
func (*LoopExpr) exprNode()     {}
func (*Block) exprNode()        {}
func (*ReturnExpr) exprNode()   {}
func (*BreakExpr) exprNode()    {}
func (*ContinueExpr) exprNode() {}
func (*MatchExpr) exprNode()    {}
func (*Lambda) exprNode()       {}
func (*StructLit) exprNode()    {}
func (*TupleExpr) exprNode()    {}
func (*ArrayLit) exprNode()     {}
func (*RefExpr) exprNode()      {}
func (*DerefExpr) exprNode()    {}
func (*CastExpr) exprNode()     {}
func (*TryExpr) exprNode()      {}
func (*SpawnExpr) exprNode()    {}

func (e *IntLit) Pos() token.Pos       { return e.StartPos }
func (e *FloatLit) Pos() token.Pos     { return e.StartPos }
func (e *StringLit) Pos() token.Pos    { return e.StartPos }
func (e *CharLit) Pos() token.Pos      { return e.StartPos }
func (e *BoolLit) Pos() token.Pos      { return e.StartPos }
func (e *UnitLit) Pos() token.Pos      { return e.StartPos }
func (e *Ident) Pos() token.Pos        { return e.NamePos }
func (e *Path) Pos() token.Pos         { return e.StartPos }
func (e *Binary) Pos() token.Pos       { return e.OpPos }
func (e *Unary) Pos() token.Pos        { return e.OpPos }
func (e *Call) Pos() token.Pos         { return e.StartPos }
func (e *MethodCall) Pos() token.Pos   { return e.StartPos }
func (e *FieldAccess) Pos() token.Pos  { return e.StartPos }
func (e *IndexExpr) Pos() token.Pos    { return e.StartPos }
func (e *AssignExpr) Pos() token.Pos   { return e.OpPos }
func (e *IfExpr) Pos() token.Pos       { return e.StartPos }
func (e *WhileExpr) Pos() token.Pos    { return e.StartPos }
func (e *ForExpr) Pos() token.Pos      { return e.StartPos }
func (e *LoopExpr) Pos() token.Pos     { return e.StartPos }
func (e *ReturnExpr) Pos() token.Pos   { return e.StartPos }
func (e *BreakExpr) Pos() token.Pos    { return e.StartPos }
func (e *ContinueExpr) Pos() token.Pos { return e.StartPos }
func (e *MatchExpr) Pos() token.Pos    { return e.StartPos }
func (e *Lambda) Pos() token.Pos       { return e.StartPos }
func (e *StructLit) Pos() token.Pos    { return e.StartPos }
func (e *TupleExpr) Pos() token.Pos    { return e.StartPos }
func (e *ArrayLit) Pos() token.Pos     { return e.StartPos }
func (e *RefExpr) Pos() token.Pos      { return e.StartPos }
func (e *DerefExpr) Pos() token.Pos    { return e.StartPos }
func (e *CastExpr) Pos() token.Pos     { return e.StartPos }
func (e *TryExpr) Pos() token.Pos      { return e.StartPos }
func (e *SpawnExpr) Pos() token.Pos    { return e.StartPos }

// --- Patterns ---

type Pattern interface {
	Node
	patNode()
}

type WildcardPat struct{ StartPos token.Pos }
type BindPat struct {
	NamePos token.Pos
	Name    string
	Mut     bool
}
type LitPat struct {
	StartPos token.Pos
	Lit      Expr
}
type TuplePat struct {
	StartPos token.Pos
	Elems    []Pattern
}
type StructPat struct {
	StartPos token.Pos
	Path     []string
	Fields   []FieldPat
	Rest     bool
}
type EnumPat struct {
	StartPos token.Pos
	Path     []string
	Tuple    []Pattern // nil if unit
	HasTuple bool
}
type OrPat struct {
	StartPos token.Pos
	Alts     []Pattern
}

type FieldPat struct {
	Name string
	Pat  Pattern // if nil, binds field to its own name
}

func (p *WildcardPat) Pos() token.Pos { return p.StartPos }
func (p *BindPat) Pos() token.Pos     { return p.NamePos }
func (p *LitPat) Pos() token.Pos      { return p.StartPos }
func (p *TuplePat) Pos() token.Pos    { return p.StartPos }
func (p *StructPat) Pos() token.Pos   { return p.StartPos }
func (p *EnumPat) Pos() token.Pos     { return p.StartPos }
func (p *OrPat) Pos() token.Pos       { return p.StartPos }

func (*WildcardPat) patNode() {}
func (*BindPat) patNode()     {}
func (*LitPat) patNode()      {}
func (*TuplePat) patNode()    {}
func (*StructPat) patNode()   {}
func (*EnumPat) patNode()     {}
func (*OrPat) patNode()       {}
