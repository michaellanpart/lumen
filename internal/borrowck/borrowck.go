// Package borrowck implements Lumen's move/borrow checker.
//
// v0.4 was a *whole-value* move checker: structs were treated as atomic
// values and any field-level move escalated to "the whole struct is
// moved." v0.4.1 introduces **partial moves**: each binding has a tree of
// move states, so `let y = p.inner` only consumes `p.inner` and leaves
// `p.other` usable. The whole struct may not be used (or moved) again
// until reassigned, but other still-live fields can be read.
//
// Borrow tracking (&T / &mut T aliasing rules) is still deferred to a
// future revision.
package borrowck

import (
	"fmt"
	"strings"

	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/token"
	"github.com/lumen-lang/lumen/internal/types"
)

// Check runs the move checker against an already type-checked program.
// It returns a (possibly empty) slice of diagnostic errors.
func Check(prog *ast.Program, info *types.Info) []error {
	c := &checker{info: info}
	for _, sig := range info.Order {
		c.checkFn(sig)
	}
	for _, sig := range info.Methods {
		c.checkFn(sig)
	}
	return c.errs
}

// --- internals ---

// moveState tracks the move state of a single binding (or sub-field).
//
//   - `whole` is true when the place was moved as a unit. In that state,
//     `parts` is meaningless (and cleared).
//   - Otherwise, `parts` maps field names to per-field move states. A
//     missing key means "fully live." A present key means "this field has
//     some sub-move" (read recursively).
type moveState struct {
	whole   bool
	wholeAt token.Pos
	parts   map[string]*moveState
}

// usableAt reports whether the place rooted here at the given field path
// is fully live (no move at or below). It returns the position of the
// offending move when not usable.
func (s *moveState) usableAt(path []string) (bool, token.Pos) {
	if s == nil {
		return true, token.Pos{}
	}
	if s.whole {
		return false, s.wholeAt
	}
	if len(path) == 0 {
		// Need every descendant to be live.
		for _, c := range s.parts {
			if ok, at := c.usableAt(nil); !ok {
				return false, at
			}
		}
		return true, token.Pos{}
	}
	return s.parts[path[0]].usableAt(path[1:])
}

// anyPartMoved reports whether *any* descendant under this node has been
// moved (used for diagnostic phrasing — "partially moved" vs "moved").
func (s *moveState) anyPartMoved() bool {
	if s == nil {
		return false
	}
	for _, c := range s.parts {
		if c.whole || c.anyPartMoved() {
			return true
		}
	}
	return false
}

// markMoved marks the place at `path` as wholly moved.
func (s *moveState) markMoved(path []string, at token.Pos) {
	if len(path) == 0 {
		s.whole = true
		s.wholeAt = at
		s.parts = nil
		return
	}
	if s.parts == nil {
		s.parts = map[string]*moveState{}
	}
	child, ok := s.parts[path[0]]
	if !ok {
		child = &moveState{}
		s.parts[path[0]] = child
	}
	child.markMoved(path[1:], at)
}

// revive clears the move at `path` (and any descendants). A no-op when
// an ancestor is whole-moved, since we have no way to express "field
// alive, rest dead" without a containing aggregate.
func (s *moveState) revive(path []string) {
	if len(path) == 0 {
		s.whole = false
		s.wholeAt = token.Pos{}
		s.parts = nil
		return
	}
	if s.whole {
		return
	}
	if s.parts == nil {
		return
	}
	if child, ok := s.parts[path[0]]; ok {
		child.revive(path[1:])
		if !child.whole && len(child.parts) == 0 {
			delete(s.parts, path[0])
		}
	}
}

// clone deep-copies the state.
func (s *moveState) clone() *moveState {
	if s == nil {
		return nil
	}
	out := &moveState{whole: s.whole, wholeAt: s.wholeAt}
	if len(s.parts) > 0 {
		out.parts = make(map[string]*moveState, len(s.parts))
		for k, c := range s.parts {
			out.parts[k] = c.clone()
		}
	}
	return out
}

// mergeFrom union-merges another state into this one: a place is moved
// after the merge if either side had it moved.
func (s *moveState) mergeFrom(b *moveState) {
	if b == nil {
		return
	}
	if s.whole {
		return
	}
	if b.whole {
		s.whole = true
		s.wholeAt = b.wholeAt
		s.parts = nil
		return
	}
	for k, bc := range b.parts {
		sc := s.parts[k]
		if sc == nil {
			sc = &moveState{}
			if s.parts == nil {
				s.parts = map[string]*moveState{}
			}
			s.parts[k] = sc
		}
		sc.mergeFrom(bc)
	}
}

// equals reports deep equality (used for the "loop didn't move anything"
// check).
func (s *moveState) equals(b *moveState) bool {
	if s == nil && b == nil {
		return true
	}
	if s == nil || b == nil {
		// A nil and an empty are equivalent.
		return s.isLive() && b.isLive()
	}
	if s.whole != b.whole {
		return false
	}
	if len(s.parts) != len(b.parts) {
		return false
	}
	for k, sc := range s.parts {
		if !sc.equals(b.parts[k]) {
			return false
		}
	}
	return true
}

func (s *moveState) isLive() bool {
	if s == nil {
		return true
	}
	if s.whole {
		return false
	}
	for _, c := range s.parts {
		if !c.isLive() {
			return false
		}
	}
	return true
}

// frame maps local-variable names to their current move state. The outer
// slice is a stack of lexical scopes; lookups walk top-down so inner
// shadowings win.
type frame []map[string]*moveState

func (f frame) lookup(name string) (*moveState, bool) {
	for i := len(f) - 1; i >= 0; i-- {
		if s, ok := f[i][name]; ok {
			return s, true
		}
	}
	return nil, false
}

// snapshot returns a deep copy of the frame so branches can be merged.
func (f frame) snapshot() frame {
	out := make(frame, len(f))
	for i, scope := range f {
		cp := make(map[string]*moveState, len(scope))
		for k, v := range scope {
			cp[k] = v.clone()
		}
		out[i] = cp
	}
	return out
}

// mergeInto unions the moves from b into a. After this call, a local is
// considered moved if either branch moved it.
func (f frame) mergeInto(b frame) {
	for i := range f {
		if i >= len(b) {
			break
		}
		for name, s := range f[i] {
			if bs, ok := b[i][name]; ok {
				s.mergeFrom(bs)
			}
		}
	}
}

// equalMoves reports whether two frames agree on which places are moved.
// Used to detect loops that consume their own inputs.
func (f frame) equalMoves(b frame) bool {
	if len(f) != len(b) {
		return false
	}
	for i := range f {
		if len(f[i]) != len(b[i]) {
			return false
		}
		for name, s := range f[i] {
			bs, ok := b[i][name]
			if !ok || !s.equals(bs) {
				return false
			}
		}
	}
	return true
}

type checker struct {
	info    *types.Info
	errs    []error
	borrows [][]*borrow // parallel stack to frame: borrows live in the
	// scope-layer that created them, dropped on pop.
	loopDepth int // when >0, suppresses NLL last-use shortening because
	// source-position ordering does not reflect runtime execution order
	// across iterations.
}

// borrow records an active &/&mut borrow against the place (root, path).
// `owner` is the local-binding name that keeps the borrow alive; it is
// used purely so that frame snapshots/branches don't need to track these:
// borrows die when their owner's lexical scope ends.
//
// `lastUse` enables non-lexical lifetimes (NLL): it is the latest source
// position at which `owner` is referenced in its enclosing block (or 0
// if not yet computed, meaning fall back to full-lexical scoping).
// Conflicts whose position is strictly past `lastUse` are ignored — the
// borrow is considered dead by then.
type borrow struct {
	owner   string
	root    string
	path    []string
	mut     bool
	at      token.Pos
	lastUse token.Pos
}

// overlapPaths reports whether two field paths under the same root
// conflict — i.e. one is a prefix of the other (so a borrow of one
// observes the other).
func overlapPaths(a, b []string) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pushScope pushes a new lexical scope for both moves and borrows.
func (c *checker) pushScope(f *frame) {
	*f = append(*f, map[string]*moveState{})
	c.borrows = append(c.borrows, nil)
}

// popScope pops one lexical scope from both stacks. Borrows registered
// in the scope are discarded (their owner went out of scope).
func (c *checker) popScope(f *frame) {
	*f = (*f)[:len(*f)-1]
	c.borrows = c.borrows[:len(c.borrows)-1]
}

// activeBorrows iterates over every currently-live borrow.
func (c *checker) activeBorrows() []*borrow {
	var out []*borrow
	for _, layer := range c.borrows {
		out = append(out, layer...)
	}
	return out
}

// registerBorrow records a let-bound &/&mut of (root, path) owned by `name`.
func (c *checker) registerBorrow(owner, root string, path []string, mut bool, at token.Pos) {
	if len(c.borrows) == 0 {
		return
	}
	top := len(c.borrows) - 1
	c.borrows[top] = append(c.borrows[top], &borrow{
		owner: owner, root: root, path: path, mut: mut, at: at,
	})
}

// borrowConflict reports the first existing borrow that conflicts with
// an attempted action on the place (root, path):
//
//   - kindRef:      taking another & on this place — conflicts with any
//     existing &mut on an overlapping path.
//   - kindRefMut:   taking a &mut — conflicts with ANY existing borrow on
//     an overlapping path.
//   - kindWrite:    move / assignment / consume — conflicts with ANY
//     existing borrow on an overlapping path.
//   - kindRead:     read access — conflicts with an existing &mut on an
//     overlapping path.
type conflictKind int

const (
	kindRef conflictKind = iota
	kindRefMut
	kindWrite
	kindRead
)

// posBefore reports whether a strictly precedes b in source order.
// Both positions must come from the same file; cross-file comparisons
// fall back to false (treating them as incomparable).
func posBefore(a, b token.Pos) bool {
	if a.File != b.File {
		return false
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// posValid reports whether p is a non-zero source position.
func posValid(p token.Pos) bool {
	return p.Line != 0
}

func (c *checker) borrowConflict(root string, path []string, kind conflictKind, at token.Pos) *borrow {
	for _, b := range c.activeBorrows() {
		if b.root != root || !overlapPaths(b.path, path) {
			continue
		}
		// NLL: when not inside a loop body, a borrow whose lastUse is set
		// and is strictly before the current position is already dead.
		if c.loopDepth == 0 && posValid(b.lastUse) && posBefore(b.lastUse, at) {
			continue
		}
		switch kind {
		case kindRef:
			if b.mut {
				return b
			}
		case kindRefMut, kindWrite:
			return b
		case kindRead:
			if b.mut {
				return b
			}
		}
	}
	return nil
}

// lastUseOfNameInBlock scans the remaining statements (and tail) of a
// block, returning the latest source position at which `name` appears as
// an identifier. Nested lambdas/spawns are skipped — a closure that
// captures `name` keeps it alive beyond simple lexical analysis, so we
// fall back to lexical scoping in that case by returning 0.
func lastUseOfNameInBlock(stmts []ast.Stmt, tail ast.Expr, name string) token.Pos {
	var latest token.Pos
	var capturedByClosure bool
	var scan func(n ast.Node)
	scan = func(n ast.Node) {
		if n == nil || capturedByClosure {
			return
		}
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == name && (!posValid(latest) || posBefore(latest, x.NamePos)) {
				latest = x.NamePos
			}
		case *ast.Unary:
			scan(x.X)
		case *ast.Binary:
			scan(x.L)
			scan(x.R)
		case *ast.Call:
			scan(x.Callee)
			for _, a := range x.Args {
				scan(a)
			}
		case *ast.MethodCall:
			scan(x.Recv)
			for _, a := range x.Args {
				scan(a)
			}
		case *ast.FieldAccess:
			scan(x.X)
		case *ast.IndexExpr:
			scan(x.X)
			scan(x.I)
		case *ast.AssignExpr:
			scan(x.L)
			scan(x.R)
		case *ast.IfExpr:
			scan(x.Cond)
			scan(x.Then)
			scan(x.Else)
		case *ast.WhileExpr:
			scan(x.Cond)
			scan(x.Body)
		case *ast.ForExpr:
			scan(x.Iter)
			scan(x.Body)
		case *ast.LoopExpr:
			scan(x.Body)
		case *ast.Block:
			for _, s := range x.Stmts {
				scan(s)
			}
			if x.Tail != nil {
				scan(x.Tail)
			}
		case *ast.ReturnExpr:
			scan(x.X)
		case *ast.BreakExpr:
			scan(x.X)
		case *ast.MatchExpr:
			scan(x.Scrut)
			for _, arm := range x.Arms {
				scan(arm.Guard)
				scan(arm.Body)
			}
		case *ast.StructLit:
			for _, fi := range x.Fields {
				scan(fi.Value)
			}
		case *ast.TupleExpr:
			for _, el := range x.Elems {
				scan(el)
			}
		case *ast.ArrayLit:
			for _, el := range x.Elems {
				scan(el)
			}
		case *ast.RefExpr:
			scan(x.X)
		case *ast.DerefExpr:
			scan(x.X)
		case *ast.CastExpr:
			scan(x.X)
		case *ast.TryExpr:
			scan(x.X)
		case *ast.Lambda, *ast.SpawnExpr:
			// A lambda/spawn body may capture `name`, extending its lifetime
			// in ways position-based NLL cannot model. Fall back to lexical.
			capturedByClosure = true
		case *ast.LetStmt:
			scan(x.Value)
		case *ast.ExprStmt:
			scan(x.X)
		}
	}
	for _, s := range stmts {
		scan(s)
	}
	if tail != nil {
		scan(tail)
	}
	if capturedByClosure {
		return token.Pos{}
	}
	return latest
}

func (c *checker) errf(p token.Pos, format string, a ...any) {
	c.errs = append(c.errs, fmt.Errorf("move error: %s: %s", p, fmt.Sprintf(format, a...)))
}

func (c *checker) borrowErrf(p token.Pos, format string, a ...any) {
	c.errs = append(c.errs, fmt.Errorf("borrow error: %s: %s", p, fmt.Sprintf(format, a...)))
}

func (c *checker) checkFn(sig *types.FnSig) {
	f := frame{map[string]*moveState{}}
	c.borrows = [][]*borrow{nil}
	// Parameters start live.
	if sig.Self != types.SelfNone {
		name := sig.SelfName
		if name == "" {
			name = "self"
		}
		f[0][name] = &moveState{}
	}
	for _, p := range sig.Params {
		f[0][p.Name] = &moveState{}
	}
	c.checkBlock(sig.Decl.Body, &f, sig.Return)
}

// checkBlock dataflows through a block. The block introduces its own
// lexical scope: bindings (and borrows owned by them) are dropped on exit.
func (c *checker) checkBlock(b *ast.Block, f *frame, ret types.Type) {
	c.pushScope(f)
	top := len(c.borrows) - 1
	for i, s := range b.Stmts {
		before := len(c.borrows[top])
		c.checkStmt(s, f)
		// Any borrows registered by this statement get their NLL `lastUse`
		// computed against the remaining statements (and tail) of this
		// block. If `owner` is never referenced again, lastUse equals the
		// borrow's own `at` — i.e. the borrow dies immediately.
		if len(c.borrows[top]) > before {
			rem := b.Stmts[i+1:]
			for _, br := range c.borrows[top][before:] {
				if br.owner == "<arg>" {
					continue // transient call-arg borrows; never need NLL
				}
				lu := lastUseOfNameInBlock(rem, b.Tail, br.owner)
				if !posValid(lu) {
					lu = br.at
				}
				br.lastUse = lu
			}
		}
	}
	if b.Tail != nil {
		c.checkExpr(b.Tail, f, false)
	}
	c.popScope(f)
}

func (c *checker) checkStmt(s ast.Stmt, f *frame) {
	switch s := s.(type) {
	case *ast.LetStmt:
		valTy := c.info.ExprTypes[s.Value]
		c.checkExpr(s.Value, f, !isCopy(valTy))
		bp, ok := s.Pattern.(*ast.BindPat)
		if !ok {
			return
		}
		(*f)[len(*f)-1][bp.Name] = &moveState{}
		// If the RHS is a direct &place or &mut place, the binding owns a
		// live borrow for the rest of this scope.
		if rx, ok := s.Value.(*ast.RefExpr); ok {
			if root, path, ok := extractPlace(rx.X); ok {
				c.registerBorrow(bp.Name, root, path, rx.Mut, rx.StartPos)
			}
		}
	case *ast.ExprStmt:
		c.checkExpr(s.X, f, false)
	case *ast.ItemStmt:
		// Nested items don't see the surrounding fn's locals; nothing to do.
	}
}

// extractPlace tries to interpret e as a (root local, field path). Returns
// ok=false when e is something other than an Ident or a chain of
// FieldAccesses ultimately rooted at an Ident.
func extractPlace(e ast.Expr) (root string, path []string, ok bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name, nil, true
	case *ast.FieldAccess:
		r, p, ok := extractPlace(x.X)
		if !ok {
			return "", nil, false
		}
		return r, append(p, x.Name), true
	}
	return "", nil, false
}

// formatPlace renders a (root, path) place for diagnostics.
func formatPlace(root string, path []string) string {
	if len(path) == 0 {
		return root
	}
	return root + "." + strings.Join(path, ".")
}

// usePlace records a read (and optional move) of a place. When the place
// can't be resolved to a local (e.g. method call on a global), the call
// is a no-op.
func (c *checker) usePlace(e ast.Expr, f *frame, consume bool, pos token.Pos) {
	root, path, ok := extractPlace(e)
	if !ok {
		return
	}
	st, ok := f.lookup(root)
	if !ok {
		return
	}
	if okUse, at := st.usableAt(path); !okUse {
		label := "moved"
		// If the offending move is *below* the requested path, the value is
		// "partially moved" — phrase the diagnostic accordingly.
		if hasMovedBelow(st, path) {
			label = "partially moved"
		}
		c.errf(pos, "use of %s value %q (moved at %s)", label, formatPlace(root, path), at)
		return
	}
	// Borrow-checker rules:
	//   - any read of a place that overlaps an active &mut is forbidden;
	//   - a move/consume requires no active borrow on overlapping paths.
	if consume && !isCopy(c.info.ExprTypes[e]) {
		if b := c.borrowConflict(root, path, kindWrite, pos); b != nil {
			c.borrowErrf(pos, "cannot move out of %q: it is borrowed (at %s)",
				formatPlace(root, path), b.at)
			return
		}
		st.markMoved(path, pos)
		return
	}
	if b := c.borrowConflict(root, path, kindRead, pos); b != nil {
		c.borrowErrf(pos, "cannot use %q: it is mutably borrowed (at %s)",
			formatPlace(root, path), b.at)
	}
}

// hasMovedBelow reports whether a sub-field strictly under `path` is moved.
func hasMovedBelow(s *moveState, path []string) bool {
	cur := s
	for _, seg := range path {
		if cur == nil || cur.whole {
			return false
		}
		cur = cur.parts[seg]
	}
	return cur != nil && (cur.anyPartMoved() || (!cur.whole && len(cur.parts) > 0))
}

// checkExpr walks an expression and updates the frame. The `consume` flag
// is true when the *result* of this expression is being moved into a
// binding/argument/return slot.
func (c *checker) checkExpr(e ast.Expr, f *frame, consume bool) {
	switch e := e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.CharLit,
		*ast.BoolLit, *ast.UnitLit, *ast.Path:
		return

	case *ast.Ident:
		c.usePlace(e, f, consume, e.NamePos)

	case *ast.Unary:
		c.checkExpr(e.X, f, false)

	case *ast.Binary:
		c.checkExpr(e.L, f, false)
		c.checkExpr(e.R, f, false)

	case *ast.RefExpr:
		// Taking a reference is a borrow, not a move. The borrowed place
		// must currently be usable AND not conflict with an existing borrow.
		// (We don't go through usePlace here because it would also report a
		// read-conflict against an active &mut — but the borrow-conflict
		// diagnostic below is more specific to "taking a borrow.")
		if root, path, ok := extractPlace(e.X); ok {
			if st, ok := f.lookup(root); ok {
				if okUse, at := st.usableAt(path); !okUse {
					label := "moved"
					if hasMovedBelow(st, path) {
						label = "partially moved"
					}
					c.errf(e.StartPos, "use of %s value %q (moved at %s)",
						label, formatPlace(root, path), at)
					return
				}
			}
			kind := kindRef
			if e.Mut {
				kind = kindRefMut
			}
			if b := c.borrowConflict(root, path, kind, e.StartPos); b != nil {
				what := "immutably"
				if e.Mut {
					what = "mutably"
				}
				had := "immutably"
				if b.mut {
					had = "mutably"
				}
				c.borrowErrf(e.StartPos,
					"cannot borrow %q %s: already borrowed %s (at %s)",
					formatPlace(root, path), what, had, b.at)
			}
		} else {
			c.checkExpr(e.X, f, false)
		}

	case *ast.FieldAccess:
		if _, _, ok := extractPlace(e); ok {
			c.usePlace(e, f, consume, e.StartPos)
		} else {
			// Field access on a non-place (e.g., call result): just walk.
			c.checkExpr(e.X, f, false)
		}

	case *ast.Call:
		c.checkExpr(e.Callee, f, false)
		c.checkCallArgs(e.Callee, e.Args, f)

	case *ast.MethodCall:
		consumeRecv := false
		selfRefMut := false
		if st := receiverStruct(c.info.ExprTypes[e.Recv]); st != nil {
			if m, ok := st.Methods[e.Method]; ok {
				switch {
				case m.Self == types.SelfValue && !m.SelfBorrow:
					consumeRecv = true
				case m.Self == types.SelfRefMut:
					selfRefMut = true
				}
			}
		}
		// Receiver is normally a place (a local or a field chain); route
		// through usePlace so partial moves are honored. For `&mut self`
		// methods, the call also acts as a transient `&mut` borrow of the
		// receiver and must conflict with any active borrow over the same
		// path (a kindRead check, which usePlace does for non-consuming
		// reads, only catches active `&mut` borrows — not active `&`).
		if root, path, ok := extractPlace(e.Recv); ok {
			if selfRefMut {
				if b := c.borrowConflict(root, path, kindRefMut, e.Recv.Pos()); b != nil {
					c.borrowErrf(e.Recv.Pos(),
						"cannot call &mut method on %q: it is borrowed (at %s)",
						formatPlace(root, path), b.at)
					return
				}
			}
			c.usePlace(e.Recv, f, consumeRecv, e.Recv.Pos())
		} else {
			c.checkExpr(e.Recv, f, consumeRecv)
		}
		c.checkMethodArgs(e, f)

	case *ast.StructLit:
		for _, fi := range e.Fields {
			ft := c.info.ExprTypes[fi.Value]
			c.checkExpr(fi.Value, f, !isCopy(ft))
		}

	case *ast.AssignExpr:
		rt := c.info.ExprTypes[e.R]
		c.checkExpr(e.R, f, !isCopy(rt))
		// LHS: assignment to a place revives it, but only if no live borrow
		// observes it.
		if root, path, ok := extractPlace(e.L); ok {
			if st, ok := f.lookup(root); ok {
				if b := c.borrowConflict(root, path, kindWrite, e.OpPos); b != nil {
					c.borrowErrf(e.OpPos,
						"cannot assign to %q: it is borrowed (at %s)",
						formatPlace(root, path), b.at)
					return
				}
				st.revive(path)
				return
			}
		}
		c.checkExpr(e.L, f, false)

	case *ast.IfExpr:
		c.checkExpr(e.Cond, f, false)
		before := f.snapshot()
		c.checkBlock(e.Then, f, types.Type{})
		thenEnd := f.snapshot()
		if e.Else != nil {
			*f = before
			switch el := e.Else.(type) {
			case *ast.Block:
				c.checkBlock(el, f, types.Type{})
			case *ast.IfExpr:
				c.checkExpr(el, f, false)
			}
			f.mergeInto(thenEnd)
		} else {
			*f = before
			f.mergeInto(thenEnd)
		}

	case *ast.WhileExpr:
		c.checkExpr(e.Cond, f, false)
		before := f.snapshot()
		c.loopDepth++
		c.checkBlock(e.Body, f, types.Type{})
		c.loopDepth--
		if !before.equalMoves(*f) {
			c.errf(e.StartPos, "while-loop body moves a value used on the next iteration")
		}

	case *ast.Block:
		c.checkBlock(e, f, types.Type{})

	case *ast.MatchExpr:
		c.checkMatchExpr(e, f)

	case *ast.ReturnExpr:
		if e.X != nil {
			rt := c.info.ExprTypes[e.X]
			c.checkExpr(e.X, f, !isCopy(rt))
		}
	}
}

func (c *checker) checkCallArgs(callee ast.Expr, args []ast.Expr, f *frame) {
	var sig *types.FnSig
	var variantFields []types.Type
	isPrint := false
	switch cl := callee.(type) {
	case *ast.Ident:
		if cl.Name == "println" || cl.Name == "print" || cl.Name == "http_serve" || cl.Name == "http_serve_fn" || cl.Name == "io_setbuf" || cl.Name == "fmt" || cl.Name == "parse_int" {
			isPrint = true
		} else {
			sig = c.info.Fns[cl.Name]
		}
	case *ast.Path:
		if len(cl.Segments) == 2 {
			if st, ok := c.info.Structs[cl.Segments[0]]; ok {
				sig = st.Methods[cl.Segments[1]]
			}
			if et, ok := c.info.Enums[cl.Segments[0]]; ok {
				if v, _ := et.Variant(cl.Segments[1]); v != nil {
					variantFields = v.Fields
				}
			}
		}
	}
	c.checkArgsWithTransientBorrows(args, f, func(i int) (consume, asMutBorrow bool) {
		if isPrint {
			return false, false
		}
		if variantFields != nil {
			if i < len(variantFields) {
				return !isCopy(variantFields[i]), false
			}
			return false, false
		}
		if sig != nil && i < len(sig.Params) {
			p := sig.Params[i]
			consume = !isCopy(p.Ty) && !p.Borrow
			// Auto-borrow registers as &mut if the param's declared type
			// is `&mut T`.
			if p.Ty.Kind == types.KRef && p.Ty.Mut {
				asMutBorrow = true
			}
			return consume, asMutBorrow
		}
		return false, false
	})
}

func (c *checker) checkMethodArgs(e *ast.MethodCall, f *frame) {
	var sig *types.FnSig
	if st := receiverStruct(c.info.ExprTypes[e.Recv]); st != nil {
		sig = st.Methods[e.Method]
	}
	c.checkArgsWithTransientBorrows(e.Args, f, func(i int) (consume, asMutBorrow bool) {
		if sig != nil && i < len(sig.Params) {
			p := sig.Params[i]
			consume = !isCopy(p.Ty) && !p.Borrow
			if p.Ty.Kind == types.KRef && p.Ty.Mut {
				asMutBorrow = true
			}
			return consume, asMutBorrow
		}
		return false, false
	})
}

// checkArgsWithTransientBorrows checks each argument in turn and, for any
// argument that is a `&place` / `&mut place` or an auto-borrowed place
// (info.AutoBorrow[a] == true), registers a transient borrow on the
// active scope so subsequent arguments observe the conflict. The
// transient borrows are dropped on return so they do not outlive the
// call expression.
func (c *checker) checkArgsWithTransientBorrows(
	args []ast.Expr, f *frame,
	kindOf func(i int) (consume, asMutBorrow bool),
) {
	if len(c.borrows) == 0 {
		c.borrows = append(c.borrows, nil)
	}
	top := len(c.borrows) - 1
	saved := len(c.borrows[top])
	defer func() { c.borrows[top] = c.borrows[top][:saved] }()

	for i, a := range args {
		consume, asMutBorrow := kindOf(i)
		c.checkExpr(a, f, consume)

		// Determine the borrow shape (mut?) and the place being borrowed.
		var root string
		var path []string
		var mut bool
		var ok bool
		if rx, isRef := a.(*ast.RefExpr); isRef {
			root, path, ok = extractPlace(rx.X)
			mut = rx.Mut
		} else if c.info != nil && c.info.AutoBorrow[a] {
			root, path, ok = extractPlace(a)
			mut = asMutBorrow
			// Auto-borrowed arg of a non-mut ref param is a shared borrow;
			// the kindRead/kindRef conflict check happens inside checkExpr
			// via usePlace already, so we only need to register here.
		}
		if !ok {
			continue
		}
		if _, bound := f.lookup(root); !bound {
			continue
		}
		// For auto-borrowed &mut, also surface the conflict explicitly
		// (checkExpr only ran a kindRead path for the non-RefExpr case).
		if mut && c.info != nil && c.info.AutoBorrow[a] {
			if b := c.borrowConflict(root, path, kindRefMut, a.Pos()); b != nil {
				c.borrowErrf(a.Pos(),
					"cannot pass %q as &mut: it is borrowed (at %s)",
					formatPlace(root, path), b.at)
				continue
			}
		}
		c.borrows[top] = append(c.borrows[top], &borrow{
			owner: "<arg>",
			root:  root,
			path:  append([]string(nil), path...),
			mut:   mut,
			at:    a.Pos(),
		})
	}
}

// isCopy reports whether values of t can be silently duplicated. In v0.4
// the primitives and all references are Copy; structs are not.
func isCopy(t types.Type) bool {
	switch t.Kind {
	case types.KI64, types.KF64, types.KBool, types.KString,
		types.KUnit, types.KRef, types.KUnknown,
		types.KOption, types.KResult:
		return true
	case types.KStruct, types.KEnum, types.KVec, types.KMap:
		return false
	}
	return true
}

// receiverStruct extracts the struct type of a method receiver, looking
// through one level of reference.
func receiverStruct(t types.Type) *types.StructTy {
	switch t.Kind {
	case types.KStruct:
		return t.Struct
	case types.KRef:
		if t.Inner != nil && t.Inner.Kind == types.KStruct {
			return t.Inner.Struct
		}
	}
	return nil
}

// checkMatchExpr dataflows a match expression. Scrutinee is consumed if
// it's non-Copy; each arm runs against a fresh snapshot; the final state
// is the union of per-arm end states.
func (c *checker) checkMatchExpr(m *ast.MatchExpr, f *frame) {
	scrutTy := c.info.ExprTypes[m.Scrut]
	c.checkExpr(m.Scrut, f, !isCopy(scrutTy))

	if len(m.Arms) == 0 {
		return
	}
	before := f.snapshot()
	var merged frame
	for i, arm := range m.Arms {
		*f = before.snapshot()
		c.pushScope(f)
		bindPatLocals(arm.Pat, f)
		if arm.Guard != nil {
			c.checkExpr(arm.Guard, f, false)
		}
		c.checkExpr(arm.Body, f, false)
		c.popScope(f)
		if i == 0 {
			merged = f.snapshot()
		} else {
			merged.mergeInto(*f)
		}
	}
	*f = merged
}

func bindPatLocals(p ast.Pattern, f *frame) {
	switch p := p.(type) {
	case *ast.BindPat:
		(*f)[len(*f)-1][p.Name] = &moveState{}
	case *ast.EnumPat:
		for _, sub := range p.Tuple {
			bindPatLocals(sub, f)
		}
	}
}
