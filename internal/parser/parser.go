// Package parser builds a Lumen AST from a Go-shape token stream.
//
// v0.6 surface:
//
//	package main                          (optional, ignored)
//	func name(p Type, q Type) RetType { ... }
//	func (r RecvType) name(...) Ret { ... }   // method
//	type Name struct { x Type; y Type }
//	type Name enum { Variant; Variant(Type, Type) }
//	x := expr                             // short var decl
//	var x Type = expr                     // typed var decl
//	if cond { ... } else { ... }
//	for cond { ... }                      // (replaces while)
//	for { ... }                           // infinite loop
//	switch scrut { case Pat: stmts; case Pat: stmts; default: stmts }
//
// Statements are terminated by ASI semicolons inserted by the lexer.
// References use Rust-style `&x` / `*p`; receivers may be `(p T)` or `(p *T)`.
package parser

import (
	"fmt"
	"strconv"

	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/lexer"
	"github.com/lumen-lang/lumen/internal/token"
)

type Parser struct {
	toks                []token.Token
	pos                 int
	errs                []string
	file                string
	structLitsForbidden bool
	// methods collected from receiver-form `func (r T) m(...)` declarations.
	// Synthesized into ImplBlocks at end of Parse.
	freeMethods map[string][]*ast.FnDecl
	freeOrder   []string // preserves declaration order of receiver types
}

// Parse parses a whole source file.
func Parse(file, src string) (*ast.Program, []string) {
	lx := lexer.New(file, src)
	toks := lx.Tokenize()
	p := &Parser{toks: toks, file: file, freeMethods: map[string][]*ast.FnDecl{}}
	prog := &ast.Program{File: file}

	p.skipTerms()
	// optional `package IDENT`
	if p.at(token.PACKAGE) {
		p.next()
		p.expect(token.IDENT)
		p.expectTerm()
		p.skipTerms()
	}

	for !p.at(token.EOF) {
		p.skipTerms()
		if p.at(token.EOF) {
			break
		}
		it := p.parseItem()
		if it != nil {
			prog.Items = append(prog.Items, it)
		} else if p.at(token.EOF) {
			break
		}
		p.skipTerms()
	}

	// synthesize an ImplBlock per receiver type
	for _, tname := range p.freeOrder {
		fns := p.freeMethods[tname]
		if len(fns) == 0 {
			continue
		}
		ib := &ast.ImplBlock{
			StartPos: fns[0].NamePos,
			ForType:  &ast.NamedType{NamePos: fns[0].NamePos, Path: []string{tname}},
			Methods:  fns,
		}
		prog.Items = append(prog.Items, ib)
	}

	allErrs := append([]string{}, lx.Errors()...)
	allErrs = append(allErrs, p.errs...)
	return prog, allErrs
}

// --- token helpers ---

func (p *Parser) cur() token.Token {
	if p.pos >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos]
}
func (p *Parser) peek(off int) token.Token {
	i := p.pos + off
	if i >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[i]
}
func (p *Parser) at(k token.Kind) bool { return p.cur().Kind == k }
func (p *Parser) next() token.Token {
	t := p.cur()
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}
func (p *Parser) eat(k token.Kind) bool {
	if p.at(k) {
		p.next()
		return true
	}
	return false
}
func (p *Parser) expect(k token.Kind) token.Token {
	if !p.at(k) {
		p.errorf(p.cur().Pos, "expected %s, got %s (%q)", k, p.cur().Kind, p.cur().Lit)
		return p.cur()
	}
	return p.next()
}
func (p *Parser) errorf(pos token.Pos, format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf("%s: %s", pos, fmt.Sprintf(format, args...)))
}

// skipTerms eats any run of SEMI tokens (statement terminators / ASI noise).
func (p *Parser) skipTerms() {
	for p.at(token.SEMI) {
		p.next()
	}
}

// expectTerm consumes one SEMI (or RBRACE / EOF as soft-end).
func (p *Parser) expectTerm() {
	switch p.cur().Kind {
	case token.SEMI:
		p.next()
	case token.RBRACE, token.EOF, token.RPAREN, token.RBRACK:
		// caller will close the block; no SEMI needed before close
	default:
		p.errorf(p.cur().Pos, "expected end-of-statement, got %s (%q)", p.cur().Kind, p.cur().Lit)
		// try to recover: skip until SEMI or block close
		for !p.at(token.SEMI) && !p.at(token.RBRACE) && !p.at(token.EOF) {
			p.next()
		}
		p.eat(token.SEMI)
	}
}

// --- items ---

func (p *Parser) parseItem() ast.Item {
	switch p.cur().Kind {
	case token.FUNC:
		return p.parseFunc()
	case token.TYPE:
		return p.parseTypeDecl()
	case token.IMPORT:
		// skip: `import "x"` or `import ( ... )`
		p.next()
		if p.eat(token.LPAREN) {
			for !p.at(token.RPAREN) && !p.at(token.EOF) {
				p.next()
			}
			p.expect(token.RPAREN)
		} else if p.at(token.STRING) {
			p.next()
		}
		p.expectTerm()
		return nil
	}
	p.errorf(p.cur().Pos, "expected item (func or type), got %s (%q)", p.cur().Kind, p.cur().Lit)
	// resync
	for !p.at(token.EOF) && !p.at(token.FUNC) && !p.at(token.TYPE) {
		p.next()
	}
	return nil
}

// parseFunc parses either:
//
//	func name(...) Ret { ... }
//	func (recv T)   name(...) Ret { ... }
//	func (recv *T)  name(...) Ret { ... }
func (p *Parser) parseFunc() ast.Item {
	p.expect(token.FUNC)
	// receiver?
	if p.at(token.LPAREN) {
		p.next()
		recvName := p.expect(token.IDENT)
		recvTy := p.parseType()
		p.expect(token.RPAREN)
		nameTok := p.expect(token.IDENT)
		fn := &ast.FnDecl{NamePos: nameTok.Pos, Name: nameTok.Lit}
		// build self param from receiver
		self := ast.Param{NamePos: recvName.Pos, Name: recvName.Lit, IsSelf: true}
		var typeName string
		if rt, ok := recvTy.(*ast.RefType); ok {
			self.SelfRef = true
			// v0.6 wart fixed in v0.7 step 2: start as `&self`; the SelfMut
			// inference pass upgrades to `&mut self` when the body mutates
			// the receiver.
			self.SelfMut = false
			if inner, ok := rt.Inner.(*ast.NamedType); ok && len(inner.Path) == 1 {
				typeName = inner.Path[0]
			}
		} else if nt, ok := recvTy.(*ast.NamedType); ok && len(nt.Path) == 1 {
			typeName = nt.Path[0]
		}
		if typeName == "" {
			p.errorf(recvName.Pos, "invalid method receiver type")
			typeName = "_"
		}
		fn.Params = []ast.Param{self}
		p.parseFuncTail(fn)
		if _, seen := p.freeMethods[typeName]; !seen {
			p.freeOrder = append(p.freeOrder, typeName)
		}
		p.freeMethods[typeName] = append(p.freeMethods[typeName], fn)
		return nil
	}
	// regular function
	nameTok := p.expect(token.IDENT)
	fn := &ast.FnDecl{NamePos: nameTok.Pos, Name: nameTok.Lit}
	p.parseFuncTail(fn)
	return fn
}

// parseFuncTail parses `(params) [RetType] { body }` into fn (params are
// appended after any pre-existing receiver self param).
func (p *Parser) parseFuncTail(fn *ast.FnDecl) {
	p.expect(token.LPAREN)
	if !p.at(token.RPAREN) {
		for {
			fn.Params = append(fn.Params, p.parseParam())
			if !p.eat(token.COMMA) {
				break
			}
			if p.at(token.RPAREN) {
				break
			}
		}
	}
	p.expect(token.RPAREN)
	// optional return type: anything starting a type expression (not `{`)
	if !p.at(token.LBRACE) && p.startsType() {
		fn.Return = p.parseType()
	}
	if p.at(token.LBRACE) {
		fn.Body = p.parseBlock()
	} else {
		p.expectTerm() // declaration without body (extern-like; not standard in v0.6)
	}
}

func (p *Parser) parseParam() ast.Param {
	nameTok := p.expect(token.IDENT)
	ty := p.parseType()
	return ast.Param{NamePos: nameTok.Pos, Name: nameTok.Lit, Ty: ty}
}

// startsType reports whether the current token can begin a Type expression.
func (p *Parser) startsType() bool {
	switch p.cur().Kind {
	case token.IDENT, token.SELF_TYPE, token.STAR, token.AMP,
		token.LPAREN, token.LBRACK, token.FUNC:
		return true
	}
	return false
}

// parseTypeDecl: `type Name struct { fields }` or `type Name enum { variants }`.
func (p *Parser) parseTypeDecl() ast.Item {
	p.expect(token.TYPE)
	nameTok := p.expect(token.IDENT)
	switch p.cur().Kind {
	case token.STRUCT:
		p.next()
		s := &ast.StructDecl{NamePos: nameTok.Pos, Name: nameTok.Lit}
		p.expect(token.LBRACE)
		p.skipTerms()
		for !p.at(token.RBRACE) && !p.at(token.EOF) {
			fnameTok := p.expect(token.IDENT)
			ty := p.parseType()
			s.Fields = append(s.Fields, ast.Field{NamePos: fnameTok.Pos, Name: fnameTok.Lit, Ty: ty, IsPub: true})
			p.expectTerm()
			p.skipTerms()
		}
		p.expect(token.RBRACE)
		return s
	case token.ENUM:
		p.next()
		e := &ast.EnumDecl{NamePos: nameTok.Pos, Name: nameTok.Lit}
		p.expect(token.LBRACE)
		p.skipTerms()
		for !p.at(token.RBRACE) && !p.at(token.EOF) {
			vtok := p.expect(token.IDENT)
			v := ast.Variant{NamePos: vtok.Pos, Name: vtok.Lit}
			if p.eat(token.LPAREN) {
				for !p.at(token.RPAREN) {
					v.Tuple = append(v.Tuple, p.parseType())
					if !p.eat(token.COMMA) {
						break
					}
				}
				p.expect(token.RPAREN)
			} else {
				v.IsUnit = true
			}
			e.Variants = append(e.Variants, v)
			p.expectTerm()
			p.skipTerms()
		}
		p.expect(token.RBRACE)
		return e
	}
	// type alias: `type Name = Type`
	if p.eat(token.EQ) {
		ta := &ast.TypeAlias{NamePos: nameTok.Pos, Name: nameTok.Lit}
		ta.Target = p.parseType()
		p.expectTerm()
		return ta
	}
	p.errorf(p.cur().Pos, "expected `struct`, `enum`, or `=` after `type Name`, got %s", p.cur().Kind)
	return nil
}

// --- types ---

// parseType accepts:
//
//	*T          (mut borrow)
//	&T / &mut T (back-compat refs)
//	(T,T,...)   (tuple)
//	[N]T        (array)
//	func(...) Ret
//	Name[::Name]
func (p *Parser) parseType() ast.Type {
	switch p.cur().Kind {
	case token.STAR:
		t := p.next()
		return &ast.RefType{StartPos: t.Pos, Mut: false, Inner: p.parseType()}
	case token.AMP:
		t := p.next()
		mut := p.eat(token.MUT)
		return &ast.RefType{StartPos: t.Pos, Mut: mut, Inner: p.parseType()}
	case token.LPAREN:
		t := p.next()
		if p.eat(token.RPAREN) {
			return &ast.TupleType{StartPos: t.Pos}
		}
		first := p.parseType()
		if p.eat(token.RPAREN) {
			return first
		}
		tup := &ast.TupleType{StartPos: t.Pos, Elems: []ast.Type{first}}
		for p.eat(token.COMMA) {
			if p.at(token.RPAREN) {
				break
			}
			tup.Elems = append(tup.Elems, p.parseType())
		}
		p.expect(token.RPAREN)
		return tup
	case token.LBRACK:
		// [N]T  (Go-style) or [T;N] (back-compat)
		t := p.next()
		if p.at(token.INT) || p.at(token.IDENT) || p.at(token.RBRACK) {
			var size ast.Expr
			if p.at(token.INT) {
				it := p.next()
				v, _ := strconv.ParseInt(stripSuffix(it.Lit), 10, 64)
				size = &ast.IntLit{StartPos: it.Pos, Value: v}
			}
			p.expect(token.RBRACK)
			elem := p.parseType()
			return &ast.ArrayType{StartPos: t.Pos, Elem: elem, Size: size}
		}
		// fallback to old `[T;N]`
		elem := p.parseType()
		var size ast.Expr
		if p.eat(token.SEMI) {
			size = p.parseExpr()
		}
		p.expect(token.RBRACK)
		return &ast.ArrayType{StartPos: t.Pos, Elem: elem, Size: size}
	case token.FUNC:
		t := p.next()
		p.expect(token.LPAREN)
		ft := &ast.FnType{StartPos: t.Pos}
		for !p.at(token.RPAREN) {
			ft.Params = append(ft.Params, p.parseType())
			if !p.eat(token.COMMA) {
				break
			}
		}
		p.expect(token.RPAREN)
		if p.startsType() && !p.at(token.LBRACE) {
			ft.Return = p.parseType()
		}
		return ft
	case token.IDENT, token.SELF_TYPE:
		startPos := p.cur().Pos
		var segs []string
		segs = append(segs, p.next().Lit)
		for p.at(token.COLONCOLON) {
			p.next()
			segs = append(segs, p.expect(token.IDENT).Lit)
		}
		nt := &ast.NamedType{NamePos: startPos, Path: segs}
		// Generic type args: Name<T1, T2, ...>
		// Only parse when the current token is `<` to avoid ambiguity with
		// comparison operators. This is safe because we're already in type
		// position (parseType is only called from type contexts).
		if p.at(token.LT) {
			p.next() // consume `<`
			for !p.at(token.GT) && !p.at(token.EOF) {
				nt.Args = append(nt.Args, p.parseType())
				if !p.eat(token.COMMA) {
					break
				}
			}
			p.expect(token.GT)
		}
		return nt
	}
	p.errorf(p.cur().Pos, "expected type, got %s", p.cur().Kind)
	tok := p.next()
	return &ast.NamedType{NamePos: tok.Pos, Path: []string{"<error>"}}
}

// --- blocks & statements ---

func (p *Parser) parseBlock() *ast.Block {
	tok := p.expect(token.LBRACE)
	blk := &ast.Block{StartPos: tok.Pos}
	p.skipTerms()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.atStmtBoundary() {
			// nested item-style: not allowed in v0.6 (no inner func/type)
			p.errorf(p.cur().Pos, "inner items not supported")
			p.next()
			continue
		}
		// var declaration
		if p.at(token.VAR) {
			blk.Stmts = append(blk.Stmts, p.parseVar())
			p.expectTerm()
			p.skipTerms()
			continue
		}
		// expression-or-shortdecl-or-assignment-or-block-tail
		e := p.parseExpr()
		// short var decl: IDENT :=  or  IDENT, IDENT, ... := (only single supported)
		if id, ok := e.(*ast.Ident); ok && p.at(token.COLONEQ) {
			p.next()
			rhs := p.parseExpr()
			blk.Stmts = append(blk.Stmts, &ast.LetStmt{
				StartPos: id.NamePos,
				Pattern:  &ast.BindPat{NamePos: id.NamePos, Name: id.Name},
				Value:    rhs,
				Mut:      true, // v0.6: all `:=` bindings are reassignable
			})
			p.expectTerm()
			p.skipTerms()
			continue
		}
		// statement vs tail
		if p.at(token.SEMI) {
			p.next()
			p.skipTerms()
			// Promote the trailing expression to be the block's tail when
			// followed immediately by `}`. ASI always inserts SEMI before
			// `}`, so this is how we recover the "last expression is the
			// block's value" form for every expression shape (literals,
			// calls, binaries, struct lits, if/match/switch/for/loop/...).
			if p.at(token.RBRACE) {
				blk.Tail = e
				break
			}
			blk.Stmts = append(blk.Stmts, &ast.ExprStmt{X: e})
			continue
		}
		if p.at(token.RBRACE) {
			blk.Tail = e
			break
		}
		if isBlockExpr(e) {
			blk.Stmts = append(blk.Stmts, &ast.ExprStmt{X: e})
			p.skipTerms()
			continue
		}
		p.errorf(p.cur().Pos, "expected end-of-statement, got %s", p.cur().Kind)
		blk.Stmts = append(blk.Stmts, &ast.ExprStmt{X: e})
		p.next()
	}
	p.expect(token.RBRACE)
	return blk
}

func (p *Parser) atStmtBoundary() bool {
	switch p.cur().Kind {
	case token.FUNC, token.TYPE:
		return true
	}
	return false
}

func isBlockExpr(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Block, *ast.IfExpr, *ast.WhileExpr, *ast.ForExpr, *ast.LoopExpr, *ast.MatchExpr, *ast.ReturnExpr:
		return true
	}
	return false
}

// parseVar: `var x Type = expr` | `var x Type` | `var x = expr`
func (p *Parser) parseVar() *ast.LetStmt {
	tok := p.expect(token.VAR)
	nameTok := p.expect(token.IDENT)
	stmt := &ast.LetStmt{
		StartPos: tok.Pos,
		Pattern:  &ast.BindPat{NamePos: nameTok.Pos, Name: nameTok.Lit},
		Mut:      true,
	}
	if p.startsType() && !p.at(token.EQ) {
		stmt.Ty = p.parseType()
	}
	if p.eat(token.EQ) {
		stmt.Value = p.parseExpr()
	} else {
		// uninitialized: synthesize unit (or zero-value placeholder)
		stmt.Value = &ast.UnitLit{StartPos: nameTok.Pos}
	}
	return stmt
}

// --- patterns (Rust-like, reused from v0.5; bare-variant inference deferred) ---

func (p *Parser) parsePattern() ast.Pattern {
	first := p.parsePatternAtom()
	if p.at(token.PIPE) {
		alts := []ast.Pattern{first}
		for p.eat(token.PIPE) {
			alts = append(alts, p.parsePatternAtom())
		}
		return &ast.OrPat{StartPos: first.Pos(), Alts: alts}
	}
	return first
}

func (p *Parser) parsePatternAtom() ast.Pattern {
	switch p.cur().Kind {
	case token.UNDERSCORE:
		t := p.next()
		return &ast.WildcardPat{StartPos: t.Pos}
	case token.INT, token.FLOAT, token.STRING, token.CHAR, token.TRUE, token.FALSE:
		startPos := p.cur().Pos
		lit := p.parsePrimary()
		return &ast.LitPat{StartPos: startPos, Lit: lit}
	case token.MINUS:
		startPos := p.cur().Pos
		lit := p.parsePrefix()
		return &ast.LitPat{StartPos: startPos, Lit: lit}
	case token.LPAREN:
		t := p.next()
		if p.eat(token.RPAREN) {
			return &ast.TuplePat{StartPos: t.Pos}
		}
		elems := []ast.Pattern{p.parsePattern()}
		for p.eat(token.COMMA) {
			if p.at(token.RPAREN) {
				break
			}
			elems = append(elems, p.parsePattern())
		}
		p.expect(token.RPAREN)
		if len(elems) == 1 {
			return elems[0]
		}
		return &ast.TuplePat{StartPos: t.Pos, Elems: elems}
	case token.IDENT, token.SELF_TYPE:
		startPos := p.cur().Pos
		segs := []string{p.next().Lit}
		for p.at(token.COLONCOLON) {
			p.next()
			segs = append(segs, p.expect(token.IDENT).Lit)
		}
		if p.eat(token.LPAREN) {
			ep := &ast.EnumPat{StartPos: startPos, Path: segs, HasTuple: true}
			for !p.at(token.RPAREN) {
				ep.Tuple = append(ep.Tuple, p.parsePattern())
				if !p.eat(token.COMMA) {
					break
				}
			}
			p.expect(token.RPAREN)
			return ep
		}
		if p.eat(token.LBRACE) {
			sp := &ast.StructPat{StartPos: startPos, Path: segs}
			for !p.at(token.RBRACE) {
				if p.eat(token.DOTDOT) {
					sp.Rest = true
					break
				}
				fn := p.expect(token.IDENT)
				fp := ast.FieldPat{Name: fn.Lit}
				if p.eat(token.COLON) {
					fp.Pat = p.parsePattern()
				}
				sp.Fields = append(sp.Fields, fp)
				if !p.eat(token.COMMA) {
					break
				}
			}
			p.expect(token.RBRACE)
			return sp
		}
		if len(segs) > 1 {
			return &ast.EnumPat{StartPos: startPos, Path: segs}
		}
		return &ast.BindPat{NamePos: startPos, Name: segs[0]}
	}
	p.errorf(p.cur().Pos, "expected pattern, got %s", p.cur().Kind)
	t := p.next()
	return &ast.WildcardPat{StartPos: t.Pos}
}

// --- expressions (Pratt) ---

const (
	precLowest   = 0
	precAssign   = 1
	precLogicOr  = 2
	precLogicAnd = 3
	precCompare  = 4
	precBitOr    = 5
	precBitXor   = 6
	precBitAnd   = 7
	precShift    = 8
	precAdd      = 9
	precMul      = 10
	precCast     = 11
	precUnary    = 12
	precPostfix  = 13
)

func (p *Parser) parseExpr() ast.Expr { return p.parseExprPrec(precLowest) }

func (p *Parser) parseExprPrec(minPrec int) ast.Expr {
	lhs := p.parsePrefix()
	for {
		opPrec, opName, rightAssoc := p.peekBinop()
		if opPrec <= precLowest || opPrec < minPrec {
			break
		}
		if opName == "=" || isAssignOp(opName) {
			opTok := p.next()
			rhs := p.parseExprPrec(opPrec)
			lhs = &ast.AssignExpr{OpPos: opTok.Pos, Op: opName, L: lhs, R: rhs}
			continue
		}
		if opName == "as" {
			opTok := p.next()
			ty := p.parseType()
			lhs = &ast.CastExpr{StartPos: opTok.Pos, X: lhs, Ty: ty}
			continue
		}
		opTok := p.next()
		nextMin := opPrec + 1
		if rightAssoc {
			nextMin = opPrec
		}
		rhs := p.parseExprPrec(nextMin)
		lhs = &ast.Binary{OpPos: opTok.Pos, Op: opName, L: lhs, R: rhs}
	}
	return lhs
}

func (p *Parser) peekBinop() (prec int, name string, rightAssoc bool) {
	switch p.cur().Kind {
	case token.EQ:
		return precAssign, "=", true
	case token.PLUSEQ:
		return precAssign, "+=", true
	case token.MINUSEQ:
		return precAssign, "-=", true
	case token.STAREQ:
		return precAssign, "*=", true
	case token.SLASHEQ:
		return precAssign, "/=", true
	case token.PERCENTEQ:
		return precAssign, "%=", true
	case token.AMPEQ:
		return precAssign, "&=", true
	case token.PIPEEQ:
		return precAssign, "|=", true
	case token.CARETEQ:
		return precAssign, "^=", true
	case token.SHLEQ:
		return precAssign, "<<=", true
	case token.SHREQ:
		return precAssign, ">>=", true
	case token.OR:
		return precLogicOr, "||", false
	case token.AND:
		return precLogicAnd, "&&", false
	case token.EQEQ:
		return precCompare, "==", false
	case token.NEQ:
		return precCompare, "!=", false
	case token.LT:
		return precCompare, "<", false
	case token.LTE:
		return precCompare, "<=", false
	case token.GT:
		return precCompare, ">", false
	case token.GTE:
		return precCompare, ">=", false
	case token.PIPE:
		return precBitOr, "|", false
	case token.CARET:
		return precBitXor, "^", false
	case token.AMP:
		return precBitAnd, "&", false
	case token.SHL:
		return precShift, "<<", false
	case token.SHR:
		return precShift, ">>", false
	case token.PLUS:
		return precAdd, "+", false
	case token.MINUS:
		return precAdd, "-", false
	case token.STAR:
		return precMul, "*", false
	case token.SLASH:
		return precMul, "/", false
	case token.PERCENT:
		return precMul, "%", false
	case token.AS:
		return precCast, "as", false
	}
	return precLowest, "", false
}

func isAssignOp(s string) bool {
	switch s {
	case "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=":
		return true
	}
	return false
}

func (p *Parser) parsePrefix() ast.Expr {
	switch p.cur().Kind {
	case token.MINUS, token.NOT:
		t := p.next()
		x := p.parsePrefix()
		return &ast.Unary{OpPos: t.Pos, Op: t.Lit, X: x}
	case token.STAR:
		t := p.next()
		x := p.parsePrefix()
		return &ast.DerefExpr{StartPos: t.Pos, X: x}
	case token.AMP:
		t := p.next()
		mut := p.eat(token.MUT)
		x := p.parsePrefix()
		return &ast.RefExpr{StartPos: t.Pos, Mut: mut, X: x}
	}
	return p.parsePostfix(p.parsePrimary())
}

func (p *Parser) parsePostfix(lhs ast.Expr) ast.Expr {
	for {
		switch p.cur().Kind {
		case token.LPAREN:
			tok := p.next()
			var args []ast.Expr
			for !p.at(token.RPAREN) {
				args = append(args, p.parseExpr())
				if !p.eat(token.COMMA) {
					break
				}
			}
			p.expect(token.RPAREN)
			lhs = &ast.Call{StartPos: tok.Pos, Callee: lhs, Args: args}
		case token.DOT:
			tok := p.next()
			if p.at(token.INT) {
				idxTok := p.next()
				lhs = &ast.FieldAccess{StartPos: tok.Pos, X: lhs, Name: idxTok.Lit}
				continue
			}
			nameTok := p.expect(token.IDENT)
			if p.at(token.LPAREN) {
				p.next()
				var args []ast.Expr
				for !p.at(token.RPAREN) {
					args = append(args, p.parseExpr())
					if !p.eat(token.COMMA) {
						break
					}
				}
				p.expect(token.RPAREN)
				lhs = &ast.MethodCall{StartPos: tok.Pos, Recv: lhs, Method: nameTok.Lit, Args: args}
			} else {
				lhs = &ast.FieldAccess{StartPos: tok.Pos, X: lhs, Name: nameTok.Lit}
			}
		case token.LBRACK:
			tok := p.next()
			idx := p.parseExpr()
			p.expect(token.RBRACK)
			lhs = &ast.IndexExpr{StartPos: tok.Pos, X: lhs, I: idx}
		case token.QUESTION:
			tok := p.next()
			lhs = &ast.TryExpr{StartPos: tok.Pos, X: lhs}
		default:
			return lhs
		}
	}
}

func (p *Parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.INT:
		p.next()
		v, _ := parseIntLit(t.Lit)
		return &ast.IntLit{StartPos: t.Pos, Value: v, Suffix: extractSuffix(t.Lit)}
	case token.FLOAT:
		p.next()
		v, _ := strconv.ParseFloat(stripSuffix(t.Lit), 64)
		return &ast.FloatLit{StartPos: t.Pos, Value: v, Suffix: extractSuffix(t.Lit)}
	case token.STRING:
		p.next()
		return &ast.StringLit{StartPos: t.Pos, Value: t.Lit}
	case token.CHAR:
		p.next()
		var r rune
		for _, c := range t.Lit {
			r = c
			break
		}
		return &ast.CharLit{StartPos: t.Pos, Value: r}
	case token.TRUE:
		p.next()
		return &ast.BoolLit{StartPos: t.Pos, Value: true}
	case token.FALSE:
		p.next()
		return &ast.BoolLit{StartPos: t.Pos, Value: false}
	case token.LPAREN:
		p.next()
		if p.eat(token.RPAREN) {
			return &ast.UnitLit{StartPos: t.Pos}
		}
		first := p.parseExpr()
		if p.eat(token.RPAREN) {
			return first
		}
		elems := []ast.Expr{first}
		for p.eat(token.COMMA) {
			if p.at(token.RPAREN) {
				break
			}
			elems = append(elems, p.parseExpr())
		}
		p.expect(token.RPAREN)
		return &ast.TupleExpr{StartPos: t.Pos, Elems: elems}
	case token.LBRACK:
		p.next()
		var elems []ast.Expr
		for !p.at(token.RBRACK) {
			elems = append(elems, p.parseExpr())
			if !p.eat(token.COMMA) {
				break
			}
		}
		p.expect(token.RBRACK)
		return &ast.ArrayLit{StartPos: t.Pos, Elems: elems}
	case token.LBRACE:
		return p.parseBlock()
	case token.IF:
		return p.parseIf()
	case token.FOR:
		return p.parseFor()
	case token.SWITCH:
		return p.parseSwitch()
	case token.FN, token.FUNC:
		return p.parseLambda()
	case token.RETURN:
		p.next()
		r := &ast.ReturnExpr{StartPos: t.Pos}
		if !p.endsExpr() {
			r.X = p.parseExpr()
		}
		return r
	case token.BREAK:
		p.next()
		b := &ast.BreakExpr{StartPos: t.Pos}
		if !p.endsExpr() {
			b.X = p.parseExpr()
		}
		return b
	case token.CONTINUE:
		p.next()
		return &ast.ContinueExpr{StartPos: t.Pos}
	case token.IDENT, token.SELF_TYPE, token.SELF_VAL:
		return p.parsePathOrStructLit()
	}
	p.errorf(t.Pos, "expected expression, got %s (%q)", t.Kind, t.Lit)
	p.next()
	return &ast.UnitLit{StartPos: t.Pos}
}

// parseLambda parses inline function literals:
//   fn(x i64, y i64) i64 { ... }
//   func(x i64) { ... }
func (p *Parser) parseLambda() ast.Expr {
	tok := p.next() // FN or FUNC
	lam := &ast.Lambda{StartPos: tok.Pos}
	p.expect(token.LPAREN)
	if !p.at(token.RPAREN) {
		for {
			lam.Params = append(lam.Params, p.parseParam())
			if !p.eat(token.COMMA) {
				break
			}
			if p.at(token.RPAREN) {
				break
			}
		}
	}
	p.expect(token.RPAREN)
	if !p.at(token.LBRACE) && p.startsType() {
		lam.Return = p.parseType()
	}
	if p.at(token.LBRACE) {
		lam.Body = p.parseBlock()
	} else {
		p.errorf(p.cur().Pos, "lambda literal requires a block body")
		lam.Body = &ast.UnitLit{StartPos: tok.Pos}
	}
	return lam
}

func (p *Parser) endsExpr() bool {
	switch p.cur().Kind {
	case token.SEMI, token.RBRACE, token.RPAREN, token.RBRACK, token.COMMA, token.EOF:
		return true
	}
	return false
}

func (p *Parser) parseExprNoStruct() ast.Expr {
	save := p.structLitsForbidden
	p.structLitsForbidden = true
	defer func() { p.structLitsForbidden = save }()
	return p.parseExpr()
}

func (p *Parser) parsePathOrStructLit() ast.Expr {
	startPos := p.cur().Pos
	segs := []string{p.next().Lit}
	for p.at(token.COLONCOLON) {
		p.next()
		seg := p.expect(token.IDENT).Lit
		segs = append(segs, seg)
	}
	if !p.structLitsForbidden && p.at(token.LBRACE) && looksLikeStructLit(p) {
		p.next()
		sl := &ast.StructLit{StartPos: startPos, Path: segs}
		p.skipTerms()
		for !p.at(token.RBRACE) {
			fn := p.expect(token.IDENT)
			fi := ast.FieldInit{NamePos: fn.Pos, Name: fn.Lit}
			if p.eat(token.COLON) {
				fi.Value = p.parseExpr()
			} else {
				fi.Value = &ast.Ident{NamePos: fn.Pos, Name: fn.Lit}
			}
			sl.Fields = append(sl.Fields, fi)
			if !p.eat(token.COMMA) {
				p.skipTerms()
				break
			}
			p.skipTerms()
		}
		p.expect(token.RBRACE)
		return sl
	}
	if len(segs) == 1 {
		return &ast.Ident{NamePos: startPos, Name: segs[0]}
	}
	return &ast.Path{StartPos: startPos, Segments: segs}
}

func looksLikeStructLit(p *Parser) bool {
	// after LBRACE: RBRACE | IDENT (COLON|COMMA|RBRACE|SEMI)
	if p.peek(1).Kind == token.RBRACE {
		return true
	}
	if p.peek(1).Kind == token.IDENT {
		k := p.peek(2).Kind
		return k == token.COLON || k == token.COMMA || k == token.RBRACE || k == token.SEMI
	}
	if p.peek(1).Kind == token.SEMI {
		// `{ <newline> field: ... }` style
		i := 1
		for p.peek(i).Kind == token.SEMI {
			i++
		}
		if p.peek(i).Kind == token.IDENT {
			k := p.peek(i + 1).Kind
			return k == token.COLON || k == token.COMMA || k == token.RBRACE || k == token.SEMI
		}
	}
	return false
}

// parseIf: `if cond { ... } [ else if cond {...} ]* [ else {...} ]`
func (p *Parser) parseIf() ast.Expr {
	tok := p.expect(token.IF)
	cond := p.parseExprNoStruct()
	then := p.parseBlock()
	ie := &ast.IfExpr{StartPos: tok.Pos, Cond: cond, Then: then}
	// ASI may have inserted SEMI before `else` because `}` is ASI-eligible.
	// Tolerate it here.
	save := p.pos
	p.skipTerms()
	if p.eat(token.ELSE) {
		if p.at(token.IF) {
			ie.Else = p.parseIf()
		} else {
			ie.Else = p.parseBlock()
		}
	} else {
		p.pos = save
	}
	return ie
}

// parseFor: `for { ... }`  or  `for cond { ... }`  or  `for pat in expr { ... }`
func (p *Parser) parseFor() ast.Expr {
	tok := p.expect(token.FOR)
	if p.at(token.LBRACE) {
		return &ast.LoopExpr{StartPos: tok.Pos, Body: p.parseBlock()}
	}
	// Detect `for ident in expr { }` by peeking for `in` after the first token.
	// We parse the pattern first only when the second token is `in`.
	if p.at(token.IDENT) && p.peek(1).Kind == token.IN {
		pat := p.parsePattern()
		p.expect(token.IN)
		iter := p.parseExprNoStruct()
		body := p.parseBlock()
		return &ast.ForExpr{StartPos: tok.Pos, Pat: pat, Iter: iter, Body: body}
	}
	cond := p.parseExprNoStruct()
	body := p.parseBlock()
	return &ast.WhileExpr{StartPos: tok.Pos, Cond: cond, Body: body}
}

// parseSwitch: `switch scrut { case Pat: stmts...; case Pat: stmts...; default: stmts... }`
//
// Each arm body runs until next `case` / `default` / `}`. Arms desugar into
// MatchExpr arms; multi-statement bodies become a Block; the block's value is
// its tail expression (or unit if there's no expression-valued tail).
func (p *Parser) parseSwitch() ast.Expr {
	tok := p.expect(token.SWITCH)
	scrut := p.parseExprNoStruct()
	p.expect(token.LBRACE)
	me := &ast.MatchExpr{StartPos: tok.Pos, Scrut: scrut}
	p.skipTerms()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		arm := ast.MatchArm{}
		armPos := p.cur().Pos
		switch p.cur().Kind {
		case token.CASE:
			p.next()
			arm.Pat = p.parsePattern()
		case token.DEFAULT:
			p.next()
			arm.Pat = &ast.WildcardPat{StartPos: armPos}
		default:
			p.errorf(p.cur().Pos, "expected `case` or `default`, got %s", p.cur().Kind)
			p.next()
			continue
		}
		p.expect(token.COLON)
		// arm body: a sequence of stmts until next case/default/RBRACE
		body := p.parseSwitchArmBody(armPos)
		arm.Body = body
		me.Arms = append(me.Arms, arm)
		p.skipTerms()
	}
	p.expect(token.RBRACE)
	return me
}

func (p *Parser) parseSwitchArmBody(pos token.Pos) ast.Expr {
	p.skipTerms()
	blk := &ast.Block{StartPos: pos}
	for !p.at(token.CASE) && !p.at(token.DEFAULT) && !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.at(token.VAR) {
			blk.Stmts = append(blk.Stmts, p.parseVar())
			p.expectTerm()
			p.skipTerms()
			continue
		}
		e := p.parseExpr()
		if id, ok := e.(*ast.Ident); ok && p.at(token.COLONEQ) {
			p.next()
			rhs := p.parseExpr()
			blk.Stmts = append(blk.Stmts, &ast.LetStmt{
				StartPos: id.NamePos,
				Pattern:  &ast.BindPat{NamePos: id.NamePos, Name: id.Name},
				Value:    rhs,
				Mut:      true,
			})
			p.expectTerm()
			p.skipTerms()
			continue
		}
		// if this is the last expression before arm end, treat as tail value.
		if p.at(token.SEMI) {
			p.next()
			p.skipTerms()
			if p.at(token.CASE) || p.at(token.DEFAULT) || p.at(token.RBRACE) {
				// last stmt of arm body acts as tail
				blk.Tail = e
				break
			}
			blk.Stmts = append(blk.Stmts, &ast.ExprStmt{X: e})
			continue
		}
		// reached arm boundary directly (e.g. block expression w/o semi)
		blk.Tail = e
		break
	}
	if len(blk.Stmts) == 0 && blk.Tail != nil {
		return blk.Tail
	}
	return blk
}

// --- numeric literal helpers ---

func stripSuffix(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c == 'i' || c == 'u' || c == 'f') && i > 0 {
			prev := s[i-1]
			if prev >= '0' && prev <= '9' || prev == '.' {
				return s[:i]
			}
		}
	}
	return s
}

func extractSuffix(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c == 'i' || c == 'u' || c == 'f') && i > 0 {
			prev := s[i-1]
			if prev >= '0' && prev <= '9' || prev == '.' {
				return s[i:]
			}
		}
	}
	return ""
}

func parseIntLit(s string) (int64, error) {
	s = stripSuffix(s)
	if len(s) > 2 {
		switch s[:2] {
		case "0x", "0X":
			return strconv.ParseInt(s[2:], 16, 64)
		case "0b", "0B":
			return strconv.ParseInt(s[2:], 2, 64)
		case "0o", "0O":
			return strconv.ParseInt(s[2:], 8, 64)
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
