// Package cbackend lowers a v0.2-typed Lumen program to portable C99,
// then invokes the system C compiler.
package cbackend

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/types"
	"github.com/lumen-lang/lumen/runtime"
)

// Options configures Compile.
type Options struct {
	Output     string // path to final executable
	RuntimeDir string // dir containing lumen.h
	KeepC      bool   // keep generated .c file for inspection
	CC         string // override C compiler (default: $CC or "cc")
	OptFlags   []string
}

// Compile emits C for prog (already type-checked by `info`) and links a
// native executable at opts.Output.
func Compile(prog *ast.Program, info *types.Info, opts Options) error {
	if opts.Output == "" {
		opts.Output = "a.out"
	}
	if len(opts.OptFlags) == 0 {
		opts.OptFlags = []string{"-O2", "-std=c99"}
	}
	cc := opts.CC
	if cc == "" {
		if env := os.Getenv("CC"); env != "" {
			cc = env
		} else {
			cc = "cc"
		}
	}
	src, err := Emit(prog, info)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "lumen-c-*")
	if err != nil {
		return err
	}
	if !opts.KeepC {
		defer os.RemoveAll(tmp)
	}
	cpath := filepath.Join(tmp, "lumen_out.c")
	if err := os.WriteFile(cpath, src, 0644); err != nil {
		return err
	}
	// Write the embedded runtime header alongside the generated C source
	// unless the caller already provided one.
	runtimeDir := opts.RuntimeDir
	if runtimeDir == "" {
		runtimeDir = tmp
		if err := os.WriteFile(filepath.Join(tmp, "lumen.h"), runtime.LumenH(), 0644); err != nil {
			return err
		}
	}

	args := []string{}
	args = append(args, opts.OptFlags...)
	args = append(args, "-I", runtimeDir)
	args = append(args, "-o", opts.Output, cpath)
	// pthread for the http_serve runtime builtin (no-op on macOS where
	// pthread is in libc; required on Linux).
	args = append(args, "-lpthread")
	cmd := exec.Command(cc, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w (C source kept at %s)", cc, err, cpath)
	}
	if opts.KeepC {
		fmt.Fprintf(os.Stderr, "lumen: kept C source at %s\n", cpath)
	}
	return nil
}

// Emit lowers the program to a C99 source buffer (exposed for tests/tools).
func Emit(prog *ast.Program, info *types.Info) ([]byte, error) {
	e := &emitter{info: info}
	e.planLambdas()
	e.writeln(`#include "lumen.h"`)
	e.writeln("")
	// Struct forward declarations (so types can mention each other in
	// any order). C only allows forward struct decls; field types are
	// resolved when the body is emitted below.
	for _, st := range info.StructOrd {
		fmt.Fprintf(&e.buf, "typedef struct %s %s;\n", cStructName(st.Name), cStructName(st.Name))
	}
	for _, et := range info.EnumOrd {
		fmt.Fprintf(&e.buf, "typedef struct %s %s;\n", cEnumName(et.Name), cEnumName(et.Name))
	}
	if len(info.StructOrd)+len(info.EnumOrd) > 0 {
		e.writeln("")
	}
	// Struct bodies.
	for _, st := range info.StructOrd {
		e.emitStructBody(st)
	}
	// Enum bodies (tagged unions).
	for _, et := range info.EnumOrd {
		e.emitEnumBody(et)
	}
	// Function forward declarations (free fns + methods).
	for _, sig := range info.Order {
		e.writeFnHeader(sig)
		e.writeln(";")
	}
	for _, sig := range info.Methods {
		e.writeFnHeader(sig)
		e.writeln(";")
	}
	for _, lam := range e.lambdas {
		// Emit the env struct typedef (if any) before the forward declaration
		// so the type is visible everywhere the lambda is referenced.
		name := e.lambdaMap[lam]
		if caps := info.LambdaCaptures[lam]; len(caps) > 0 {
			envTy := name + "_env_t"
			fmt.Fprintf(&e.buf, "typedef struct { ")
			for _, cap := range caps {
				fmt.Fprintf(&e.buf, "%s %s; ", cTypeOf(cap.Ty), cap.Name)
			}
			fmt.Fprintf(&e.buf, "} %s;\n", envTy)
		}
		e.writeLambdaHeader(lam)
		e.writeln(";")
	}
	e.writeln("")
	// Bodies.
	for _, sig := range info.Order {
		if err := e.emitFn(sig); err != nil {
			return nil, err
		}
	}
	for _, sig := range info.Methods {
		if err := e.emitFn(sig); err != nil {
			return nil, err
		}
	}
	for _, lam := range e.lambdas {
		if err := e.emitLambda(lam); err != nil {
			return nil, err
		}
	}
	// If a `main` exists, wrap it as C int main.
	if main, ok := info.Fns["main"]; ok {
		e.writeln("")
		e.writeln("int main(void) {")
		if main.Return.Kind == types.KI64 {
			e.writeln("    return (int)Lm_main(NULL);")
		} else {
			e.writeln("    Lm_main(NULL);")
			e.writeln("    return 0;")
		}
		e.writeln("}")
	}
	return e.buf.Bytes(), nil
}

// --- emitter ---

type emitter struct {
	info *types.Info
	buf  bytes.Buffer
	ind  int
	tmp  int
	ret  types.Type

	lambdaMap map[*ast.Lambda]string
	lambdas   []*ast.Lambda
}

func (e *emitter) write(s string)   { e.buf.WriteString(s) }
func (e *emitter) writeln(s string) { e.buf.WriteString(s); e.buf.WriteByte('\n') }
func (e *emitter) indent() {
	for i := 0; i < e.ind; i++ {
		e.buf.WriteString("    ")
	}
}

func (e *emitter) fresh(prefix string) string {
	e.tmp++
	return fmt.Sprintf("__lm_%s_%d", prefix, e.tmp)
}

func (e *emitter) planLambdas() {
	e.lambdaMap = map[*ast.Lambda]string{}
	for _, sig := range e.info.Order {
		e.collectLambdaBlock(sig.Decl.Body)
	}
	for _, sig := range e.info.Methods {
		e.collectLambdaBlock(sig.Decl.Body)
	}
}

func (e *emitter) collectLambdaBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			e.collectLambdaExpr(s.Value)
		case *ast.ExprStmt:
			e.collectLambdaExpr(s.X)
		}
	}
	if b.Tail != nil {
		e.collectLambdaExpr(b.Tail)
	}
}

func (e *emitter) collectLambdaExpr(x ast.Expr) {
	switch x := x.(type) {
	case *ast.Lambda:
		if _, ok := e.lambdaMap[x]; !ok {
			e.lambdaMap[x] = e.fresh("lambda")
			e.lambdas = append(e.lambdas, x)
		}
		e.collectLambdaExpr(x.Body)
	case *ast.Binary:
		e.collectLambdaExpr(x.L)
		e.collectLambdaExpr(x.R)
	case *ast.Unary:
		e.collectLambdaExpr(x.X)
	case *ast.Call:
		e.collectLambdaExpr(x.Callee)
		for _, a := range x.Args {
			e.collectLambdaExpr(a)
		}
	case *ast.MethodCall:
		e.collectLambdaExpr(x.Recv)
		for _, a := range x.Args {
			e.collectLambdaExpr(a)
		}
	case *ast.FieldAccess:
		e.collectLambdaExpr(x.X)
	case *ast.IndexExpr:
		e.collectLambdaExpr(x.X)
		e.collectLambdaExpr(x.I)
	case *ast.AssignExpr:
		e.collectLambdaExpr(x.L)
		e.collectLambdaExpr(x.R)
	case *ast.IfExpr:
		e.collectLambdaExpr(x.Cond)
		e.collectLambdaBlock(x.Then)
		if x.Else != nil {
			e.collectLambdaExpr(x.Else)
		}
	case *ast.WhileExpr:
		e.collectLambdaExpr(x.Cond)
		e.collectLambdaBlock(x.Body)
	case *ast.ForExpr:
		e.collectLambdaExpr(x.Iter)
		e.collectLambdaBlock(x.Body)
	case *ast.LoopExpr:
		e.collectLambdaBlock(x.Body)
	case *ast.Block:
		e.collectLambdaBlock(x)
	case *ast.ReturnExpr:
		if x.X != nil {
			e.collectLambdaExpr(x.X)
		}
	case *ast.BreakExpr:
		if x.X != nil {
			e.collectLambdaExpr(x.X)
		}
	case *ast.MatchExpr:
		e.collectLambdaExpr(x.Scrut)
		for _, arm := range x.Arms {
			if arm.Guard != nil {
				e.collectLambdaExpr(arm.Guard)
			}
			e.collectLambdaExpr(arm.Body)
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			e.collectLambdaExpr(f.Value)
		}
	case *ast.TupleExpr:
		for _, el := range x.Elems {
			e.collectLambdaExpr(el)
		}
	case *ast.ArrayLit:
		for _, el := range x.Elems {
			e.collectLambdaExpr(el)
		}
	case *ast.RefExpr:
		e.collectLambdaExpr(x.X)
	case *ast.DerefExpr:
		e.collectLambdaExpr(x.X)
	case *ast.CastExpr:
		e.collectLambdaExpr(x.X)
	case *ast.TryExpr:
		e.collectLambdaExpr(x.X)
	case *ast.SpawnExpr:
		e.collectLambdaBlock(x.Body)
	}
}

func (e *emitter) writeLambdaHeader(l *ast.Lambda) {
	fnTy := e.info.ExprTypes[l]
	name := e.lambdaMap[l]
	if fnTy.Kind != types.KFn || fnTy.Fn == nil {
		e.write("void ")
		e.write(name)
		e.write("(void)")
		return
	}
	e.write(cTypeOf(fnTy.Fn.Return))
	e.write(" ")
	e.write(name)
	e.write("(")
	if len(l.Params) == 0 {
		e.write("void *_env")
	} else {
		for i, p := range l.Params {
			if i > 0 {
				e.write(", ")
			}
			e.write(cTypeOf(fnTy.Fn.Params[i]))
			e.write(" ")
			e.write(p.Name)
		}
		e.write(", void *_env")
	}
	e.write(")")
}

func (e *emitter) emitLambda(l *ast.Lambda) error {
	name := e.lambdaMap[l]
	captures := e.info.LambdaCaptures[l] // nil if non-capturing

	e.writeLambdaHeader(l)
	e.writeln(" {")
	e.ind++
	fnTy := e.info.ExprTypes[l]
	retTy := types.TUnit
	if fnTy.Kind == types.KFn && fnTy.Fn != nil {
		retTy = fnTy.Fn.Return
	}
	prevRet := e.ret
	e.ret = retTy

	// Emit env pointer unpacking (or silence the unused-parameter warning).
	if len(captures) > 0 {
		envTy := name + "_env_t"
		e.indent()
		fmt.Fprintf(&e.buf, "%s *__env = (%s *)_env;\n", envTy, envTy)
		for _, cap := range captures {
			e.indent()
			fmt.Fprintf(&e.buf, "%s %s = __env->%s;\n", cTypeOf(cap.Ty), cap.Name, cap.Name)
		}
	} else {
		e.indent()
		e.writeln("(void)_env;")
	}

	if blk, ok := l.Body.(*ast.Block); ok {
		for _, s := range blk.Stmts {
			if err := e.emitStmt(s); err != nil {
				return err
			}
		}
		if blk.Tail != nil {
			if retTy.Kind == types.KUnit {
				if err := e.emitExprAsStmt(blk.Tail); err != nil {
					return err
				}
			} else if _, isRet := blk.Tail.(*ast.ReturnExpr); isRet {
				if err := e.emitExprAsStmt(blk.Tail); err != nil {
					return err
				}
			} else {
				v, err := e.emitExpr(blk.Tail)
				if err != nil {
					return err
				}
				e.indent()
				fmt.Fprintf(&e.buf, "return %s;\n", v)
			}
		}
	} else if retTy.Kind == types.KUnit {
		if err := e.emitExprAsStmt(l.Body); err != nil {
			return err
		}
	} else {
		v, err := e.emitExpr(l.Body)
		if err != nil {
			return err
		}
		e.indent()
		fmt.Fprintf(&e.buf, "return %s;\n", v)
	}
	e.ret = prevRet
	e.ind--
	e.writeln("}")
	e.writeln("")
	return nil
}

func cTypeOf(t types.Type) string {
	switch t.Kind {
	case types.KI64:
		return "int64_t"
	case types.KF64:
		return "double"
	case types.KBool:
		return "bool"
	case types.KString:
		return "const char *"
	case types.KUnit:
		return "void"
	case types.KStruct:
		return cStructName(t.Struct.Name)
	case types.KEnum:
		return cEnumName(t.Enum.Name)
	case types.KRef:
		inner := cTypeOf(*t.Inner)
		if t.Mut {
			return inner + " *"
		}
		return "const " + inner + " *"
	case types.KArray:
		if t.Array == nil {
			return "void *"
		}
		return cTypeOf(t.Array.Elem) + " *"
	case types.KFn:
		return "Lm_Closure"
	case types.KVec:
		return "Lm_Vec"
	case types.KOption:
		return "Lm_Option"
	case types.KResult:
		return "Lm_Result"
	case types.KMap:
		return "Lm_HashMap"
	}
	return "void"
}

// Lumen function names get an Lm_ prefix to avoid colliding with libc symbols.
func cName(name string) string           { return "Lm_" + name }
func cStructName(name string) string     { return "Lm_" + name }
func cEnumName(name string) string       { return "Lm_" + name }
func cMethodName(owner, m string) string { return "Lm_" + owner + "_" + m }

// vecElemSuffix returns the suffix used by the Lm_vec_push_*/Lm_vec_get_*
// family of helpers in lumen.h for the given element type.
func vecElemSuffix(elem types.Type) string {
	switch elem.Kind {
	case types.KI64:
		return "i64"
	case types.KF64:
		return "f64"
	case types.KBool:
		return "bool"
	case types.KString:
		return "str"
	default:
		return "raw"
	}
}

// valMember returns the Lm__Val union member name for the given type.
// Used when accessing Option.val or Result.ok / Result.err fields.
func valMember(t types.Type) string {
	switch t.Kind {
	case types.KI64:
		return "i"
	case types.KF64:
		return "f"
	case types.KBool:
		return "b"
	case types.KString:
		return "s"
	default:
		return "i" // fallback (unit/unknown)
	}
}
func cVariantField(v string) string { return "v_" + v }
func cEnumTagOf(et string, v string) string {
	return "LM_" + et + "_" + v
}

func (e *emitter) emitStructBody(st *types.StructTy) {
	fmt.Fprintf(&e.buf, "struct %s {\n", cStructName(st.Name))
	for _, f := range st.Fields {
		fmt.Fprintf(&e.buf, "    %s %s;\n", cTypeOf(f.Ty), f.Name)
	}
	e.writeln("};")
	e.writeln("")
}

// emitEnumBody lowers an enum to a C tagged union. Each variant becomes
// a named member of an inner union; the integer tag selects which member
// is active. Tags are surfaced as preprocessor constants so generated
// match-switches read naturally.
func (e *emitter) emitEnumBody(et *types.EnumTy) {
	for _, v := range et.Variants {
		fmt.Fprintf(&e.buf, "#define %s %d\n", cEnumTagOf(et.Name, v.Name), v.Tag)
	}
	fmt.Fprintf(&e.buf, "struct %s {\n", cEnumName(et.Name))
	e.writeln("    int32_t tag;")
	// The union is only needed if at least one variant carries data;
	// for all-unit enums we keep a dummy payload to make `.data` valid.
	hasPayload := false
	for _, v := range et.Variants {
		if !v.IsUnit && len(v.Fields) > 0 {
			hasPayload = true
			break
		}
	}
	if !hasPayload {
		e.writeln("};")
		e.writeln("")
		return
	}
	e.writeln("    union {")
	for _, v := range et.Variants {
		if v.IsUnit || len(v.Fields) == 0 {
			continue
		}
		fmt.Fprintf(&e.buf, "        struct {\n")
		for i, ft := range v.Fields {
			fmt.Fprintf(&e.buf, "            %s f%d;\n", cTypeOf(ft), i)
		}
		fmt.Fprintf(&e.buf, "        } %s;\n", cVariantField(v.Name))
	}
	e.writeln("    } data;")
	e.writeln("};")
	e.writeln("")
}

func (e *emitter) writeFnHeader(sig *types.FnSig) {
	e.write(cTypeOf(sig.Return))
	e.write(" ")
	e.write(sig.CName())
	e.write("(")
	hasArg := false
	if sig.Owner != "" && sig.Self != types.SelfNone {
		owner := e.info.Structs[sig.Owner]
		selfName := sig.SelfName
		if selfName == "" {
			selfName = "self"
		}
		switch sig.Self {
		case types.SelfValue:
			fmt.Fprintf(&e.buf, "%s %s", cStructName(owner.Name), selfName)
		case types.SelfRef:
			fmt.Fprintf(&e.buf, "const %s *%s", cStructName(owner.Name), selfName)
		case types.SelfRefMut:
			fmt.Fprintf(&e.buf, "%s *%s", cStructName(owner.Name), selfName)
		}
		hasArg = true
	}
	for _, p := range sig.Params {
		if hasArg {
			e.write(", ")
		}
		e.write(cTypeOf(p.Ty))
		e.write(" ")
		e.write(p.Name)
		hasArg = true
	}
	if !hasArg {
		e.write("void *_env")
	} else {
		e.write(", void *_env")
	}
	e.write(")")
}

func (e *emitter) emitFn(sig *types.FnSig) error {
	e.writeFnHeader(sig)
	e.writeln(" {")
	e.ind++
	prevRet := e.ret
	e.ret = sig.Return
	if err := e.emitFnBody(sig); err != nil {
		return err
	}
	e.ret = prevRet
	e.ind--
	e.writeln("}")
	e.writeln("")
	return nil
}

func (e *emitter) emitFnBody(sig *types.FnSig) error {
	for _, s := range sig.Decl.Body.Stmts {
		if err := e.emitStmt(s); err != nil {
			return err
		}
	}
	tail := sig.Decl.Body.Tail
	if tail == nil {
		return nil
	}
	// A trailing `return ...` (Go-shape) is already a statement — emit it
	// directly rather than wrapping it in another return.
	if _, isRet := tail.(*ast.ReturnExpr); isRet {
		return e.emitExprAsStmt(tail)
	}
	if sig.Return.Kind == types.KUnit {
		return e.emitExprAsStmt(tail)
	}
	e.indent()
	e.write("return ")
	x, err := e.emitExpr(tail)
	if err != nil {
		return err
	}
	e.write(x)
	e.writeln(";")
	return nil
}

func (e *emitter) emitStmt(s ast.Stmt) error {
	switch s := s.(type) {
	case *ast.LetStmt:
		bp := s.Pattern.(*ast.BindPat)
		t := e.info.ExprTypes[s.Value]
		ct := cTypeOf(t)
		e.indent()
		// Vec variables must always be mutable (push/pop modify them in place).
		isMut := s.Mut || t.Kind == types.KVec
		if !isMut && !strings.HasPrefix(ct, "const ") {
			e.write("const ")
		}
		e.write(ct)
		e.write(" ")
		e.write(bp.Name)
		e.write(" = ")
		x, err := e.emitExpr(s.Value)
		if err != nil {
			return err
		}
		// Lm_Closure is a struct type; C forbids casts to struct types.
		// For non-fn values, emit an explicit C cast to silence warnings.
		if t.Kind != types.KFn {
			x = "((" + ct + ")" + x + ")"
		}
		e.write(x)
		e.writeln(";")
		return nil
	case *ast.ExprStmt:
		return e.emitExprAsStmt(s.X)
	}
	return fmt.Errorf("cbackend: unsupported stmt %T", s)
}

// emitExprAsStmt prefers writing structured control-flow as native C
// statements rather than wrapping them in GCC statement expressions.
func (e *emitter) emitExprAsStmt(x ast.Expr) error {
	switch x := x.(type) {
	case *ast.IfExpr:
		return e.emitIfStmt(x)
	case *ast.WhileExpr:
		return e.emitWhileStmt(x)
	case *ast.ForExpr:
		return e.emitForInStmt(x)
	case *ast.Block:
		e.indent()
		e.writeln("{")
		e.ind++
		for _, s := range x.Stmts {
			if err := e.emitStmt(s); err != nil {
				return err
			}
		}
		if x.Tail != nil {
			// Recurse through emitExprAsStmt so a tail that is itself a
			// statement-only expression (println/print, return, nested
			// block, if/while) is emitted correctly instead of being
			// (mis)treated as a value.
			if err := e.emitExprAsStmt(x.Tail); err != nil {
				return err
			}
		}
		e.ind--
		e.indent()
		e.writeln("}")
		return nil
	case *ast.ReturnExpr:
		e.indent()
		if x.X == nil {
			e.writeln("return;")
			return nil
		}
		v, err := e.emitExpr(x.X)
		if err != nil {
			return err
		}
		e.write("return ")
		e.write(v)
		e.writeln(";")
		return nil
	case *ast.Call:
		if id, ok := x.Callee.(*ast.Ident); ok && (id.Name == "println" || id.Name == "print") {
			return e.emitPrintCall(id.Name, x.Args)
		}
	}
	// Generic fallback: evaluate as a C expression and discard.
	e.indent()
	s, err := e.emitExpr(x)
	if err != nil {
		return err
	}
	e.write(s)
	e.writeln(";")
	return nil
}

func (e *emitter) emitIfStmt(x *ast.IfExpr) error {
	e.indent()
	cond, err := e.emitExpr(x.Cond)
	if err != nil {
		return err
	}
	e.write("if (")
	e.write(cond)
	e.writeln(") {")
	e.ind++
	if err := e.emitBlockBody(x.Then); err != nil {
		return err
	}
	e.ind--
	e.indent()
	if x.Else == nil {
		e.writeln("}")
		return nil
	}
	switch el := x.Else.(type) {
	case *ast.Block:
		e.writeln("} else {")
		e.ind++
		if err := e.emitBlockBody(el); err != nil {
			return err
		}
		e.ind--
		e.indent()
		e.writeln("}")
	case *ast.IfExpr:
		e.write("} else ")
		// Re-emit nested if without the leading indent.
		cond, err := e.emitExpr(el.Cond)
		if err != nil {
			return err
		}
		e.write("if (")
		e.write(cond)
		e.writeln(") {")
		e.ind++
		if err := e.emitBlockBody(el.Then); err != nil {
			return err
		}
		e.ind--
		e.indent()
		if el.Else == nil {
			e.writeln("}")
			return nil
		}
		// chain
		return e.emitElseChain(el.Else)
	}
	return nil
}

func (e *emitter) emitElseChain(el ast.Expr) error {
	switch el := el.(type) {
	case *ast.Block:
		e.write("} else {\n")
		e.ind++
		if err := e.emitBlockBody(el); err != nil {
			return err
		}
		e.ind--
		e.indent()
		e.writeln("}")
		return nil
	case *ast.IfExpr:
		e.write("} else ")
		cond, err := e.emitExpr(el.Cond)
		if err != nil {
			return err
		}
		e.write("if (")
		e.write(cond)
		e.writeln(") {")
		e.ind++
		if err := e.emitBlockBody(el.Then); err != nil {
			return err
		}
		e.ind--
		e.indent()
		if el.Else == nil {
			e.writeln("}")
			return nil
		}
		return e.emitElseChain(el.Else)
	}
	return fmt.Errorf("cbackend: unsupported else branch %T", el)
}

func (e *emitter) emitBlockBody(b *ast.Block) error {
	for _, s := range b.Stmts {
		if err := e.emitStmt(s); err != nil {
			return err
		}
	}
	if b.Tail != nil {
		// Tail value of a side-effect block is discarded as a C statement.
		// (If the block is the tail of a non-unit fn body, emitFnBody
		// handles the `return` form directly.)
		if err := e.emitExprAsStmt(b.Tail); err != nil {
			return err
		}
	}
	return nil
}

func (e *emitter) emitWhileStmt(x *ast.WhileExpr) error {
	e.indent()
	cond, err := e.emitExpr(x.Cond)
	if err != nil {
		return err
	}
	e.write("while (")
	e.write(cond)
	e.writeln(") {")
	e.ind++
	if err := e.emitBlockBody(x.Body); err != nil {
		return err
	}
	e.ind--
	e.indent()
	e.writeln("}")
	return nil
}

// emitForInStmt lowers `for pat in iter { body }`.
// Supports Vec<T> and fixed arrays. The loop variable is bound to each
// element in order; break/continue work naturally via C break/continue.
func (e *emitter) emitForInStmt(x *ast.ForExpr) error {
	iterTy := e.info.ExprTypes[x.Iter]

	bp, hasName := x.Pat.(*ast.BindPat)

	iter, err := e.emitExpr(x.Iter)
	if err != nil {
		return err
	}

	// Generate unique temp names to avoid shadowing.
	iterVar := e.fresh("_iter")
	idxVar := e.fresh("_i")

	switch iterTy.Kind {
	case types.KVec:
		if iterTy.Vec == nil {
			return fmt.Errorf("cbackend: for-in Vec has nil element type")
		}
		elemCT := cTypeOf(iterTy.Vec.Elem)
		suffix := vecElemSuffix(iterTy.Vec.Elem)
		e.indent()
		fmt.Fprintf(&e.buf, "{ Lm_Vec %s = %s;\n", iterVar, iter)
		e.ind++
		e.indent()
		fmt.Fprintf(&e.buf, "for (int64_t %s = 0; %s < Lm_vec_len(&%s); %s++) {\n", idxVar, idxVar, iterVar, idxVar)
		e.ind++
		if hasName {
			e.indent()
			fmt.Fprintf(&e.buf, "%s %s = Lm_vec_get_%s(&%s, %s);\n", elemCT, bp.Name, suffix, iterVar, idxVar)
		}
		if err := e.emitBlockBody(x.Body); err != nil {
			return err
		}
		e.ind--
		e.indent()
		e.writeln("}")
		e.ind--
		e.indent()
		e.writeln("}")

	case types.KArray:
		if iterTy.Array == nil || !iterTy.Array.HasLen {
			return fmt.Errorf("cbackend: for-in requires a sized array")
		}
		elemCT := cTypeOf(iterTy.Array.Elem)
		n := iterTy.Array.Len
		arrVar := e.fresh("_arr")
		e.indent()
		fmt.Fprintf(&e.buf, "{ %s *%s = %s;\n", elemCT, arrVar, iter)
		e.ind++
		e.indent()
		fmt.Fprintf(&e.buf, "for (int64_t %s = 0; %s < INT64_C(%d); %s++) {\n", idxVar, idxVar, n, idxVar)
		e.ind++
		if hasName {
			e.indent()
			fmt.Fprintf(&e.buf, "%s %s = %s[%s];\n", elemCT, bp.Name, arrVar, idxVar)
		}
		if err := e.emitBlockBody(x.Body); err != nil {
			return err
		}
		e.ind--
		e.indent()
		e.writeln("}")
		e.ind--
		e.indent()
		e.writeln("}")

	default:
		return fmt.Errorf("cbackend: for-in unsupported iter type %s", iterTy)
	}
	return nil
}

func (e *emitter) emitPrintCall(name string, args []ast.Expr) error {
	for i, a := range args {
		if i > 0 {
			e.indent()
			e.writeln("Lm_print_sp();")
		}
		v, err := e.emitExpr(a)
		if err != nil {
			return err
		}
		t := e.info.ExprTypes[a]
		e.indent()
		switch t.Kind {
		case types.KI64:
			e.write("Lm_print_i64(")
		case types.KF64:
			e.write("Lm_print_f64(")
		case types.KBool:
			e.write("Lm_print_bool(")
		case types.KString:
			e.write("Lm_print_str(")
		default:
			return fmt.Errorf("cbackend: %s cannot print value of type %s", name, t)
		}
		e.write(v)
		e.writeln(");")
	}
	if name == "println" {
		e.indent()
		e.writeln("Lm_print_nl();")
	}
	return nil
}

// emitArg emits a call-site argument, wrapping it in `&` when the type
// checker recorded an implicit borrow (v0.7 auto-borrow at call sites).
func (e *emitter) emitArg(a ast.Expr) (string, error) {
	v, err := e.emitExpr(a)
	if err != nil {
		return "", err
	}
	if e.info.AutoBorrow[a] {
		return "(&" + v + ")", nil
	}
	return v, nil
}

// emitExpr returns a C expression string for x. For expressions that don't
// translate cleanly into C expressions (if/while/block as a value), we
// use a GCC/Clang statement expression `({ ... })`.
func (e *emitter) emitExpr(x ast.Expr) (string, error) {
	switch x := x.(type) {
	case *ast.IntLit:
		return fmt.Sprintf("INT64_C(%d)", x.Value), nil
	case *ast.FloatLit:
		return strconv.FormatFloat(x.Value, 'g', -1, 64), nil
	case *ast.BoolLit:
		if x.Value {
			return "true", nil
		}
		return "false", nil
	case *ast.StringLit:
		return cStringLit(x.Value), nil
	case *ast.UnitLit:
		return "((void)0)", nil
	case *ast.Path:
		return e.emitPathExpr(x)
	case *ast.MatchExpr:
		return e.emitMatchExpr(x)
	case *ast.Ident:
		// A bare ident that resolves to a top-level free function is a
		// first-class function value: emit a Lm_Closure fat pointer.
		if t, ok := e.info.ExprTypes[x]; ok && t.Kind == types.KFn {
			if sig, ok := e.info.Fns[x.Name]; ok && sig.Owner == "" {
				return fmt.Sprintf("(Lm_Closure){(void*)%s, NULL}", sig.CName()), nil
			}
		}
		return x.Name, nil
	case *ast.Lambda:
		if name, ok := e.lambdaMap[x]; ok {
			captures := e.info.LambdaCaptures[x]
			if len(captures) == 0 {
				return fmt.Sprintf("(Lm_Closure){(void*)%s, NULL}", name), nil
			}
			// Capturing lambda: allocate env struct and fill it.
			envTy := name + "_env_t"
			var sb strings.Builder
			sb.WriteString("({ ")
			fmt.Fprintf(&sb, "%s *__lm_env = (%s*)malloc(sizeof(%s)); ", envTy, envTy, envTy)
			for _, cap := range captures {
				fmt.Fprintf(&sb, "__lm_env->%s = %s; ", cap.Name, cap.Name)
			}
			fmt.Fprintf(&sb, "(Lm_Closure){(void*)%s, __lm_env}; })", name)
			return sb.String(), nil
		}
		return "", fmt.Errorf("cbackend: lambda literal not planned")
	case *ast.Unary:
		v, err := e.emitExpr(x.X)
		if err != nil {
			return "", err
		}
		return "(" + x.Op + v + ")", nil
	case *ast.Binary:
		l, err := e.emitExpr(x.L)
		if err != nil {
			return "", err
		}
		r, err := e.emitExpr(x.R)
		if err != nil {
			return "", err
		}
		// String operations: equality/inequality use strcmp; + uses Lm_str_cat.
		if lt, ok := e.info.ExprTypes[x.L]; ok && lt.Kind == types.KString {
			switch x.Op {
			case "==":
				return "(strcmp(" + l + ", " + r + ") == 0)", nil
			case "!=":
				return "(strcmp(" + l + ", " + r + ") != 0)", nil
			case "+":
				// rhs may be String, i64, f64, or bool — use tagged runtime helper.
				rt, _ := e.info.ExprTypes[x.R]
				switch rt.Kind {
				case types.KString:
					return "Lm_str_cat_s(" + l + ", " + r + ")", nil
				case types.KI64:
					return "Lm_str_cat_i(" + l + ", " + r + ")", nil
				case types.KF64:
					return "Lm_str_cat_f(" + l + ", " + r + ")", nil
				case types.KBool:
					return "Lm_str_cat_b(" + l + ", " + r + ")", nil
				}
			}
		}
		return "(" + l + " " + x.Op + " " + r + ")", nil
	case *ast.Call:
		return e.emitCallExpr(x)
	case *ast.MethodCall:
		return e.emitMethodCallExpr(x)
	case *ast.FieldAccess:
		recv, err := e.emitExpr(x.X)
		if err != nil {
			return "", err
		}
		// Use `->` for ref receivers, `.` for value receivers.
		recvTy := e.info.ExprTypes[x.X]
		if recvTy.Kind == types.KRef {
			return "(" + recv + "->" + x.Name + ")", nil
		}
		return "(" + recv + "." + x.Name + ")", nil
	case *ast.StructLit:
		return e.emitStructLitExpr(x)
	case *ast.ArrayLit:
		return e.emitArrayLitExpr(x)
	case *ast.RefExpr:
		v, err := e.emitExpr(x.X)
		if err != nil {
			return "", err
		}
		return "(&" + v + ")", nil
	case *ast.IndexExpr:
		arrTy := e.info.ExprTypes[x.X]
		arr, err := e.emitExpr(x.X)
		if err != nil {
			return "", err
		}
		idx, err := e.emitExpr(x.I)
		if err != nil {
			return "", err
		}
		// Vec<T> indexing → Lm_vec_get_SUFFIX
		if arrTy.Kind == types.KVec && arrTy.Vec != nil {
			suffix := vecElemSuffix(arrTy.Vec.Elem)
			return fmt.Sprintf("Lm_vec_get_%s(&(%s), %s)", suffix, arr, idx), nil
		}
		return "(" + arr + "[(size_t)(" + idx + ")])", nil
	case *ast.TryExpr:
		innerTy := e.info.ExprTypes[x.X]
		inner, err := e.emitExpr(x.X)
		if err != nil {
			return "", err
		}
		switch innerTy.Kind {
		case types.KOption:
			if innerTy.Option == nil {
				return "", fmt.Errorf("cbackend: malformed Option in try expression")
			}
			if e.ret.Kind != types.KOption {
				return "", fmt.Errorf("cbackend: `?` on Option requires enclosing function return Option, got %s", e.ret)
			}
			tmp := e.fresh("tryo")
			mem := valMember(innerTy.Option.Elem)
			return fmt.Sprintf("({ Lm_Option %s = %s; if (!%s.present) return %s; %s.val.%s; })", tmp, inner, tmp, tmp, tmp, mem), nil
		case types.KResult:
			if innerTy.Result == nil {
				return "", fmt.Errorf("cbackend: malformed Result in try expression")
			}
			if e.ret.Kind != types.KResult {
				return "", fmt.Errorf("cbackend: `?` on Result requires enclosing function return Result, got %s", e.ret)
			}
			tmp := e.fresh("tryr")
			mem := valMember(innerTy.Result.Ok)
			return fmt.Sprintf("({ Lm_Result %s = %s; if (!%s.is_ok) return %s; %s.ok.%s; })", tmp, inner, tmp, tmp, tmp, mem), nil
		default:
			return "", fmt.Errorf("cbackend: `?` requires Option/Result, got %s", innerTy)
		}
	case *ast.AssignExpr:
		l, err := e.emitExpr(x.L)
		if err != nil {
			return "", err
		}
		r, err := e.emitExpr(x.R)
		if err != nil {
			return "", err
		}
		// In Lumen v0.2, assignment is a unit-typed expression. Cast away
		// the C int result so it can't be misused as a value.
		return "((void)(" + l + " = " + r + "))", nil
	}
	return "", fmt.Errorf("cbackend: unsupported expression %T as value", x)
}

func (e *emitter) emitCallExpr(c *ast.Call) (string, error) {
	if p, ok := c.Callee.(*ast.Path); ok {
		if len(p.Segments) == 2 {
			if et, ok := e.info.Enums[p.Segments[0]]; ok {
				return e.emitVariantCtorExpr(c, et, p.Segments[1])
			}
		}
		return e.emitAssocCallExpr(c, p)
	}
	id, ok := c.Callee.(*ast.Ident)
	if ok && (id.Name == "println" || id.Name == "print") {
		return "", fmt.Errorf("cbackend: %s used as a value (only allowed as a statement)", id.Name)
	}
	if ok && id.Name == "len" {
		if len(c.Args) != 1 {
			return "", fmt.Errorf("cbackend: len expects exactly 1 arg")
		}
		at := e.info.ExprTypes[c.Args[0]]
		switch at.Kind {
		case types.KString:
			v, err := e.emitExpr(c.Args[0])
			if err != nil {
				return "", err
			}
			return "((int64_t)strlen(" + v + "))", nil
		case types.KArray:
			if at.Array == nil || !at.Array.HasLen {
				return "", fmt.Errorf("cbackend: len() on unsized array is not supported yet")
			}
			return fmt.Sprintf("INT64_C(%d)", at.Array.Len), nil
		default:
			return "", fmt.Errorf("cbackend: len() unsupported for %s", at)
		}
	}
	if ok && id.Name == "fmt" {
		// fmt(template, args...) -> String
		// Lowered to Lm_fmt(template, nargs, tag0, val0, tag1, val1, ...)
		// Tags: 's'=String, 'i'=i64, 'f'=f64, 'b'=bool
		if len(c.Args) == 0 {
			return "", fmt.Errorf("cbackend: fmt requires at least one argument")
		}
		tmpl, err := e.emitExpr(c.Args[0])
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Lm_fmt(%s, %d", tmpl, len(c.Args)-1)
		for _, a := range c.Args[1:] {
			at := e.info.ExprTypes[a]
			v, err := e.emitExpr(a)
			if err != nil {
				return "", err
			}
			var tag string
			switch at.Kind {
			case types.KString:
				tag = "'s'"
			case types.KI64:
				tag = "'i'"
			case types.KF64:
				tag = "'f'"
			case types.KBool:
				tag = "'b'"
			default:
				return "", fmt.Errorf("cbackend: fmt: unsupported arg type %s", at)
			}
			fmt.Fprintf(&sb, ", %s, (uintptr_t)(%s)", tag, v)
		}
		sb.WriteString(")")
		return sb.String(), nil
	}
	if ok && id.Name == "parse_int" {
		if len(c.Args) != 1 {
			return "", fmt.Errorf("cbackend: parse_int expects exactly 1 arg")
		}
		v, err := e.emitExpr(c.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Lm_parse_int(%s)", v), nil
	}
	if ok {
		if sig, isGlobalFn := e.info.Fns[id.Name]; !isGlobalFn || sig.Owner != "" {
			ok = false
		}
	}
	if ok {
		var sb strings.Builder
		sb.WriteString(cName(id.Name))
		sb.WriteString("(")
		for i, a := range c.Args {
			if i > 0 {
				sb.WriteString(", ")
			}
			v, err := e.emitArg(a)
			if err != nil {
				return "", err
			}
			sb.WriteString(v)
		}
		if len(c.Args) > 0 {
			sb.WriteString(", NULL")
		} else {
			sb.WriteString("NULL")
		}
		sb.WriteString(")")
		return sb.String(), nil
	}

	calleeTy := e.info.ExprTypes[c.Callee]
	if calleeTy.Kind != types.KFn || calleeTy.Fn == nil {
		return "", fmt.Errorf("cbackend: call on non-function callee %T (%s)", c.Callee, calleeTy)
	}
	calleeExpr, err := e.emitExpr(c.Callee)
	if err != nil {
		return "", err
	}

	// Indirect call through a Lm_Closure fat pointer.
	// Store the closure in a temp so .fn is only evaluated once.
	closTmp := e.fresh("clos")
	var sb strings.Builder
	sb.WriteString("({ Lm_Closure ")
	sb.WriteString(closTmp)
	sb.WriteString(" = ")
	sb.WriteString(calleeExpr)
	sb.WriteString("; ")
	sb.WriteString("((")
	sb.WriteString(cTypeOf(calleeTy.Fn.Return))
	sb.WriteString(" (*) (")
	if len(calleeTy.Fn.Params) == 0 {
		sb.WriteString("void *")
	} else {
		for i, pt := range calleeTy.Fn.Params {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(cTypeOf(pt))
		}
		sb.WriteString(", void *")
	}
	sb.WriteString("))")
	sb.WriteString(closTmp)
	sb.WriteString(".fn)")
	sb.WriteString("(")
	for i, a := range c.Args {
		if i > 0 {
			sb.WriteString(", ")
		}
		v, err := e.emitArg(a)
		if err != nil {
			return "", err
		}
		sb.WriteString(v)
	}
	if len(c.Args) > 0 {
		sb.WriteString(", ")
	}
	sb.WriteString(closTmp)
	sb.WriteString(".env); })")
	return sb.String(), nil
}

func (e *emitter) emitArrayLitExpr(al *ast.ArrayLit) (string, error) {
	t, ok := e.info.ExprTypes[al]
	if !ok || t.Kind != types.KArray || t.Array == nil {
		return "", fmt.Errorf("cbackend: array literal missing array type")
	}
	elemCT := cTypeOf(t.Array.Elem)
	n := len(al.Elems)
	arr := e.fresh("arr")

	var sb strings.Builder
	sb.WriteString("({ ")
	fmt.Fprintf(&sb, "%s *%s = (%s*)malloc(sizeof(%s) * %d); ", elemCT, arr, elemCT, elemCT, n)
	for i, el := range al.Elems {
		v, err := e.emitExpr(el)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "%s[%d] = %s; ", arr, i, v)
	}
	fmt.Fprintf(&sb, "%s; })", arr)
	return sb.String(), nil
}

// emitAssocCallExpr lowers `Type::fn(args)` (associated function call).
// The checker has already verified the path resolves and arity matches.
func (e *emitter) emitAssocCallExpr(c *ast.Call, p *ast.Path) (string, error) {
	if len(p.Segments) != 2 {
		return "", fmt.Errorf("cbackend: unsupported path call with %d segments", len(p.Segments))
	}
	typeName := p.Segments[0]

	// Vec::new() — lower to Lm_vec_new(sizeof(elemType)).
	if typeName == "Vec" && p.Segments[1] == "new" {
		// Get the resolved Vec type from the call's ExprTypes entry.
		vt := e.info.ExprTypes[c]
		if vt.Kind != types.KVec || vt.Vec == nil || vt.Vec.Elem.Kind == types.KUnit {
			return "", fmt.Errorf("cbackend: Vec::new() requires a type annotation (e.g. `v: Vec<i64> := Vec::new()`)")
		}
		return fmt.Sprintf("Lm_vec_new(sizeof(%s))", cTypeOf(vt.Vec.Elem)), nil
	}

	// Option constructors.
	if typeName == "Option" {
		switch p.Segments[1] {
		case "None":
			return "(Lm_Option){ .present = false }", nil
		case "Some":
			if len(c.Args) != 1 {
				return "", fmt.Errorf("cbackend: Option::Some expects 1 arg")
			}
			ot := e.info.ExprTypes[c]
			if ot.Kind != types.KOption || ot.Option == nil {
				return "", fmt.Errorf("cbackend: Option::Some: expected Option type in ExprTypes")
			}
			v, err := e.emitArg(c.Args[0])
			if err != nil {
				return "", err
			}
			mem := valMember(ot.Option.Elem)
			return fmt.Sprintf("(Lm_Option){ .present = true, .val.%s = %s }", mem, v), nil
		}
	}

	// Result constructors.
	if typeName == "Result" {
		switch p.Segments[1] {
		case "Ok":
			if len(c.Args) != 1 {
				return "", fmt.Errorf("cbackend: Result::Ok expects 1 arg")
			}
			rt := e.info.ExprTypes[c]
			if rt.Kind != types.KResult || rt.Result == nil {
				return "", fmt.Errorf("cbackend: Result::Ok: expected Result type in ExprTypes")
			}
			v, err := e.emitArg(c.Args[0])
			if err != nil {
				return "", err
			}
			mem := valMember(rt.Result.Ok)
			return fmt.Sprintf("(Lm_Result){ .is_ok = true, .ok.%s = %s }", mem, v), nil
		case "Err":
			if len(c.Args) != 1 {
				return "", fmt.Errorf("cbackend: Result::Err expects 1 arg")
			}
			rt := e.info.ExprTypes[c]
			if rt.Kind != types.KResult || rt.Result == nil {
				return "", fmt.Errorf("cbackend: Result::Err: expected Result type in ExprTypes")
			}
			v, err := e.emitArg(c.Args[0])
			if err != nil {
				return "", err
			}
			mem := valMember(rt.Result.Err)
			return fmt.Sprintf("(Lm_Result){ .is_ok = false, .err.%s = %s }", mem, v), nil
		}
	}

	// HashMap constructor.
	if typeName == "HashMap" && p.Segments[1] == "new" {
		mt := e.info.ExprTypes[c]
		if mt.Kind != types.KMap || mt.Map == nil {
			return "", fmt.Errorf("cbackend: HashMap::new() requires a type annotation (e.g. `m: HashMap<String, i64> := HashMap::new()`)")
		}
		return "Lm_hashmap_new()", nil
	}

	if typeName == "Self" {
		return "", fmt.Errorf("cbackend: `Self::` in associated call should be resolved by the checker")
	}
	var sb strings.Builder
	sb.WriteString(cMethodName(typeName, p.Segments[1]))
	sb.WriteString("(")
	for i, a := range c.Args {
		if i > 0 {
			sb.WriteString(", ")
		}
		v, err := e.emitArg(a)
		if err != nil {
			return "", err
		}
		sb.WriteString(v)
	}
	sb.WriteString(")")
	return sb.String(), nil
}

// emitMethodCallExpr lowers `recv.method(args)`. We auto-borrow the
// receiver when the method takes &self / &mut self and the receiver
// itself is a struct value (lvalue).
func (e *emitter) emitMethodCallExpr(mc *ast.MethodCall) (string, error) {
	recvTy := e.info.ExprTypes[mc.Recv]

	// Built-in String methods.
	if recvTy.Kind == types.KString {
		recv, err := e.emitExpr(mc.Recv)
		if err != nil {
			return "", err
		}
		switch mc.Method {
		case "len":
			return fmt.Sprintf("((int64_t)strlen(%s))", recv), nil
		case "contains":
			v, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(strstr(%s, %s) != NULL)", recv, v), nil
		case "starts_with":
			v, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_str_starts_with(%s, %s)", recv, v), nil
		case "ends_with":
			v, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_str_ends_with(%s, %s)", recv, v), nil
		case "trim":
			return fmt.Sprintf("Lm_str_trim(%s)", recv), nil
		case "to_upper":
			return fmt.Sprintf("Lm_str_to_upper(%s)", recv), nil
		case "to_lower":
			return fmt.Sprintf("Lm_str_to_lower(%s)", recv), nil
		case "slice":
			start, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			end, err2 := e.emitArg(mc.Args[1])
			if err2 != nil {
				return "", err2
			}
			return fmt.Sprintf("Lm_str_slice(%s, %s, %s)", recv, start, end), nil
		case "replace":
			from, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			to, err2 := e.emitArg(mc.Args[1])
			if err2 != nil {
				return "", err2
			}
			return fmt.Sprintf("Lm_str_replace(%s, %s, %s)", recv, from, to), nil
		case "index_of":
			v, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_str_index_of(%s, %s)", recv, v), nil
		case "split":
			v, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_str_split(%s, %s)", recv, v), nil
		default:
			return "", fmt.Errorf("cbackend: String has no method %q", mc.Method)
		}
	}

	// Built-in Vec<T> methods.
	if recvTy.Kind == types.KVec && recvTy.Vec != nil {
		recv, err := e.emitExpr(mc.Recv)
		if err != nil {
			return "", err
		}
		suffix := vecElemSuffix(recvTy.Vec.Elem)
		switch mc.Method {
		case "push":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: Vec::push expects 1 arg")
			}
			v, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_vec_push_%s(&(%s), %s)", suffix, recv, v), nil
		case "len":
			return fmt.Sprintf("Lm_vec_len(&(%s))", recv), nil
		case "pop":
			return fmt.Sprintf("Lm_vec_pop_%s(&(%s))", suffix, recv), nil
		case "get":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: Vec::get expects 1 arg")
			}
			idx, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_vec_get_%s(&(%s), %s)", suffix, recv, idx), nil
		default:
			return "", fmt.Errorf("cbackend: Vec has no method %q", mc.Method)
		}
	}

	// Built-in Option<T> methods.
	if recvTy.Kind == types.KOption && recvTy.Option != nil {
		recv, err := e.emitExpr(mc.Recv)
		if err != nil {
			return "", err
		}
		elem := recvTy.Option.Elem
		mem := valMember(elem)
		switch mc.Method {
		case "is_some":
			return fmt.Sprintf("(%s.present)", recv), nil
		case "is_none":
			return fmt.Sprintf("(!%s.present)", recv), nil
		case "unwrap":
			return fmt.Sprintf("Lm__option_unwrap(%s).%s", recv, mem), nil
		case "unwrap_or":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: Option::unwrap_or expects 1 arg")
			}
			d, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s.present ? %s.val.%s : %s)", recv, recv, mem, d), nil
		default:
			return "", fmt.Errorf("cbackend: Option has no method %q", mc.Method)
		}
	}

	// Built-in Result<T, E> methods.
	if recvTy.Kind == types.KResult && recvTy.Result != nil {
		recv, err := e.emitExpr(mc.Recv)
		if err != nil {
			return "", err
		}
		okMem := valMember(recvTy.Result.Ok)
		errMem := valMember(recvTy.Result.Err)
		switch mc.Method {
		case "is_ok":
			return fmt.Sprintf("(%s.is_ok)", recv), nil
		case "is_err":
			return fmt.Sprintf("(!%s.is_ok)", recv), nil
		case "unwrap":
			return fmt.Sprintf("Lm__result_unwrap(%s).%s", recv, okMem), nil
		case "unwrap_err":
			return fmt.Sprintf("Lm__result_unwrap_err(%s).%s", recv, errMem), nil
		case "unwrap_or":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: Result::unwrap_or expects 1 arg")
			}
			d, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("(%s.is_ok ? %s.ok.%s : %s)", recv, recv, okMem, d), nil
		default:
			return "", fmt.Errorf("cbackend: Result has no method %q", mc.Method)
		}
	}

	// HashMap<K, V> methods.
	if recvTy.Kind == types.KMap && recvTy.Map != nil {
		recv, err := e.emitExpr(mc.Recv)
		if err != nil {
			return "", err
		}
		valMem := valMember(recvTy.Map.Val)
		keyMem := valMember(recvTy.Map.Key)
		switch mc.Method {
		case "insert":
			if len(mc.Args) != 2 {
				return "", fmt.Errorf("cbackend: HashMap::insert expects 2 args")
			}
			k, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			v, err := e.emitArg(mc.Args[1])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_hashmap_insert(&%s, (Lm__Val){.%s=%s}, (Lm__Val){.%s=%s})", recv, keyMem, k, valMem, v), nil
		case "get":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: HashMap::get expects 1 arg")
			}
			k, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_hashmap_get(&%s, (Lm__Val){.%s=%s})", recv, keyMem, k), nil
		case "contains_key":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: HashMap::contains_key expects 1 arg")
			}
			k, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_hashmap_contains(&%s, (Lm__Val){.%s=%s})", recv, keyMem, k), nil
		case "remove":
			if len(mc.Args) != 1 {
				return "", fmt.Errorf("cbackend: HashMap::remove expects 1 arg")
			}
			k, err := e.emitArg(mc.Args[0])
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Lm_hashmap_remove(&%s, (Lm__Val){.%s=%s})", recv, keyMem, k), nil
		case "len":
			return fmt.Sprintf("Lm_hashmap_len(&%s)", recv), nil
		default:
			return "", fmt.Errorf("cbackend: HashMap has no method %q", mc.Method)
		}
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
		return "", fmt.Errorf("cbackend: method call on non-struct type %s", recvTy)
	}
	m, ok := st.Methods[mc.Method]
	if !ok {
		return "", fmt.Errorf("cbackend: struct %s has no method %q", st.Name, mc.Method)
	}
	recv, err := e.emitExpr(mc.Recv)
	if err != nil {
		return "", err
	}

	// Decide what to pass for the receiver slot.
	var recvArg string
	switch m.Self {
	case types.SelfValue:
		// pass by value; recv is already a value
		recvArg = recv
	case types.SelfRef, types.SelfRefMut:
		if recvTy.Kind == types.KRef {
			recvArg = recv // already a pointer
		} else {
			recvArg = "(&" + recv + ")" // auto-borrow lvalue
		}
	default:
		return "", fmt.Errorf("cbackend: %s::%s is not a method", st.Name, mc.Method)
	}

	var sb strings.Builder
	sb.WriteString(cMethodName(st.Name, mc.Method))
	sb.WriteString("(")
	sb.WriteString(recvArg)
	for _, a := range mc.Args {
		v, err := e.emitArg(a)
		if err != nil {
			return "", err
		}
		sb.WriteString(", ")
		sb.WriteString(v)
	}
	sb.WriteString(")")
	return sb.String(), nil
}

// emitStructLitExpr lowers `Foo { a: x, b: y }` to a C99 compound literal.
// We always emit fields in declaration order so the generated C is
// deterministic and matches what `cc` expects in struct layout.
func (e *emitter) emitStructLitExpr(sl *ast.StructLit) (string, error) {
	// The checker accepts `Self { ... }` inside an impl; we recover the
	// concrete struct via the recorded type of the literal expression.
	var st *types.StructTy
	if t, ok := e.info.ExprTypes[sl]; ok && t.Kind == types.KStruct {
		st = t.Struct
	}
	if st == nil {
		name := sl.Path[0]
		s, ok := e.info.Structs[name]
		if !ok {
			return "", fmt.Errorf("cbackend: unknown struct %q", name)
		}
		st = s
	}
	// Index the literal's fields by name.
	provided := make(map[string]ast.Expr, len(sl.Fields))
	for _, fi := range sl.Fields {
		provided[fi.Name] = fi.Value
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "((%s){", cStructName(st.Name))
	for i, f := range st.Fields {
		if i > 0 {
			sb.WriteString(", ")
		}
		v, err := e.emitExpr(provided[f.Name])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, ".%s = %s", f.Name, v)
	}
	sb.WriteString("})")
	return sb.String(), nil
}

// cStringLit returns a C double-quoted string literal for s.
func cStringLit(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				sb.WriteString(fmt.Sprintf(`\x%02x`, r))
			} else if r < 0x80 {
				sb.WriteRune(r)
			} else {
				// Emit as UTF-8 byte escapes for portability.
				bs := []byte(string(r))
				for _, b := range bs {
					sb.WriteString(fmt.Sprintf(`\x%02x`, b))
				}
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// --- enums, variant constructors, and match lowering ---

// emitPathExpr handles `Enum::UnitVariant` as a value expression.
func (e *emitter) emitPathExpr(p *ast.Path) (string, error) {
	if len(p.Segments) == 2 {
		if et, ok := e.info.Enums[p.Segments[0]]; ok {
			v, _ := et.Variant(p.Segments[1])
			if v != nil && v.IsUnit {
				return fmt.Sprintf("((%s){.tag = %s})",
					cEnumName(et.Name), cEnumTagOf(et.Name, v.Name)), nil
			}
		}
	}
	return "", fmt.Errorf("cbackend: unsupported path expression %v", p.Segments)
}

// emitVariantCtorExpr lowers `Enum::Variant(args)` into a C99 compound
// literal that initializes the tag and the matching union member.
func (e *emitter) emitVariantCtorExpr(c *ast.Call, et *types.EnumTy, vname string) (string, error) {
	v, _ := et.Variant(vname)
	if v == nil {
		return "", fmt.Errorf("cbackend: enum %s has no variant %q", et.Name, vname)
	}
	if v.IsUnit {
		return fmt.Sprintf("((%s){.tag = %s})",
			cEnumName(et.Name), cEnumTagOf(et.Name, v.Name)), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "((%s){.tag = %s, .data.%s = {",
		cEnumName(et.Name), cEnumTagOf(et.Name, v.Name), cVariantField(v.Name))
	for i, a := range c.Args {
		if i > 0 {
			sb.WriteString(", ")
		}
		av, err := e.emitExpr(a)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, ".f%d = %s", i, av)
	}
	sb.WriteString("}})")
	return sb.String(), nil
}

// emitMatchExpr lowers a `match` to a GCC/Clang statement expression so it
// can appear in any expression position. Layout:
//
//	({
//	    <ScrutTy> __s = <scrut>;
//	    <RetTy>   __r;            // omitted for unit-typed matches
//	    switch (__s.tag) {
//	        case LM_E_V: { <bindings> __r = <body>; break; }
//	        ...
//	    }
//	    __r;                       // (void)0; for unit-typed matches
//	})
func (e *emitter) emitMatchExpr(m *ast.MatchExpr) (string, error) {
	scrutTy := e.info.ExprTypes[m.Scrut]
	if scrutTy.Kind == types.KOption || scrutTy.Kind == types.KResult {
		return e.emitOptionResultMatch(m, scrutTy)
	}
	if scrutTy.Kind != types.KEnum {
		return "", fmt.Errorf("cbackend: match on non-enum scrutinee %s not supported", scrutTy)
	}
	scrutStr, err := e.emitExpr(m.Scrut)
	if err != nil {
		return "", err
	}
	resTy := e.info.ExprTypes[m]
	isUnit := resTy.Kind == types.KUnit || resTy.Kind == types.KUnknown

	sub := &emitter{info: e.info}
	sub.writeln("({")
	sub.ind++
	sub.indent()
	fmt.Fprintf(&sub.buf, "%s __s = %s;\n", cTypeOf(scrutTy), scrutStr)
	if !isUnit {
		sub.indent()
		fmt.Fprintf(&sub.buf, "%s __r;\n", cTypeOf(resTy))
	}
	sub.indent()
	sub.writeln("switch (__s.tag) {")
	sub.ind++
	et := scrutTy.Enum
	for _, arm := range m.Arms {
		sub.indent()
		if name, ok := patVariantName(arm.Pat); ok {
			v, _ := et.Variant(name)
			if v == nil {
				return "", fmt.Errorf("cbackend: unknown variant %s::%s", et.Name, name)
			}
			fmt.Fprintf(&sub.buf, "case %s: {\n", cEnumTagOf(et.Name, v.Name))
		} else {
			sub.writeln("default: {")
		}
		sub.ind++
		// If the catch-all pattern binds a name, materialize the binding.
		if bp, ok := arm.Pat.(*ast.BindPat); ok {
			sub.indent()
			fmt.Fprintf(&sub.buf, "%s %s = __s;\n", cTypeOf(scrutTy), bp.Name)
		}
		// Enum-pattern field bindings.
		if ep, ok := arm.Pat.(*ast.EnumPat); ok && ep.HasTuple {
			v, _ := et.Variant(ep.Path[1])
			for i, sp := range ep.Tuple {
				if bp, ok := sp.(*ast.BindPat); ok {
					sub.indent()
					fmt.Fprintf(&sub.buf, "%s %s = __s.data.%s.f%d;\n",
						cTypeOf(v.Fields[i]), bp.Name, cVariantField(v.Name), i)
				}
			}
		}
		if err := sub.emitArmBody(arm.Body, isUnit); err != nil {
			return "", err
		}
		sub.indent()
		sub.writeln("break;")
		sub.ind--
		sub.indent()
		sub.writeln("}")
	}
	// Make sure switch is total for the C compiler even when Lumen's
	// exhaustiveness check has already accepted the match.
	if !hasCatchAll(m.Arms) {
		sub.indent()
		sub.writeln("default: { __builtin_unreachable(); break; }")
	}
	sub.ind--
	sub.indent()
	sub.writeln("}")
	sub.indent()
	if isUnit {
		sub.writeln("(void)0;")
	} else {
		sub.writeln("__r;")
	}
	sub.ind--
	sub.indent()
	sub.write("})")
	return sub.buf.String(), nil
}

// emitArmBody emits an arm body, writing either a statement-expression
// assignment to __r (value match) or plain statements (unit match).
// emitOptionResultMatch lowers match on Option<T> or Result<T,E> to a
// statement expression using if/else chains.
//
//	({
//	    Lm_Option __s = scrut;
//	    RetTy __r;
//	    if (__s.present) { T x = __s.val.<mem>; __r = <some_body>; }
//	    else              {                      __r = <none_body>; }
//	    __r;
//	})
func (e *emitter) emitOptionResultMatch(m *ast.MatchExpr, scrutTy types.Type) (string, error) {
	scrutStr, err := e.emitExpr(m.Scrut)
	if err != nil {
		return "", err
	}
	resTy := e.info.ExprTypes[m]
	isUnit := resTy.Kind == types.KUnit || resTy.Kind == types.KUnknown

	sub := &emitter{info: e.info}
	sub.writeln("({")
	sub.ind++
	sub.indent()
	fmt.Fprintf(&sub.buf, "%s __s = %s;\n", cTypeOf(scrutTy), scrutStr)
	if !isUnit {
		sub.indent()
		fmt.Fprintf(&sub.buf, "%s __r;\n", cTypeOf(resTy))
	}

	// Find catch-all arm (bind or wildcard) for default branch.
	findCatchAll := func() *ast.MatchArm {
		for i := range m.Arms {
			if isCatchAll(m.Arms[i].Pat) {
				return &m.Arms[i]
			}
		}
		return nil
	}

	// Emit one branch for a named pattern arm.
	emitBranch := func(arm *ast.MatchArm, ep *ast.EnumPat) error {
		switch {
		case scrutTy.Kind == types.KOption && ep.Path[1] == "Some":
			elemTy := types.TUnit
			if scrutTy.Option != nil {
				elemTy = scrutTy.Option.Elem
			}
			mem := valMember(elemTy)
			if ep.HasTuple && len(ep.Tuple) == 1 {
				if bp, ok := ep.Tuple[0].(*ast.BindPat); ok {
					sub.indent()
					fmt.Fprintf(&sub.buf, "%s %s = __s.val.%s;\n", cTypeOf(elemTy), bp.Name, mem)
				}
			}
		case scrutTy.Kind == types.KOption && ep.Path[1] == "None":
			// nothing to bind
		case scrutTy.Kind == types.KResult && ep.Path[1] == "Ok":
			okTy := types.TUnit
			if scrutTy.Result != nil {
				okTy = scrutTy.Result.Ok
			}
			mem := valMember(okTy)
			if ep.HasTuple && len(ep.Tuple) == 1 {
				if bp, ok := ep.Tuple[0].(*ast.BindPat); ok {
					sub.indent()
					fmt.Fprintf(&sub.buf, "%s %s = __s.ok.%s;\n", cTypeOf(okTy), bp.Name, mem)
				}
			}
		case scrutTy.Kind == types.KResult && ep.Path[1] == "Err":
			errTy := types.TUnit
			if scrutTy.Result != nil {
				errTy = scrutTy.Result.Err
			}
			mem := valMember(errTy)
			if ep.HasTuple && len(ep.Tuple) == 1 {
				if bp, ok := ep.Tuple[0].(*ast.BindPat); ok {
					sub.indent()
					fmt.Fprintf(&sub.buf, "%s %s = __s.err.%s;\n", cTypeOf(errTy), bp.Name, mem)
				}
			}
		}
		return sub.emitArmBody(arm.Body, isUnit)
	}

	// Build a list of named arms and handle catch-all.
	var namedArms []struct {
		cond string
		arm  *ast.MatchArm
		ep   *ast.EnumPat
	}
	var defaultArm *ast.MatchArm
	for i := range m.Arms {
		arm := &m.Arms[i]
		if ep, ok := arm.Pat.(*ast.EnumPat); ok && len(ep.Path) == 2 {
			var cond string
			switch ep.Path[1] {
			case "Some":
				cond = "__s.present"
			case "None":
				cond = "!__s.present"
			case "Ok":
				cond = "__s.is_ok"
			case "Err":
				cond = "!__s.is_ok"
			}
			namedArms = append(namedArms, struct {
				cond string
				arm  *ast.MatchArm
				ep   *ast.EnumPat
			}{cond, arm, ep})
		} else if isCatchAll(arm.Pat) {
			defaultArm = arm
		}
	}
	if defaultArm == nil {
		defaultArm = findCatchAll()
	}

	// Emit if/else chain.
	for i, na := range namedArms {
		sub.indent()
		if i == 0 {
			fmt.Fprintf(&sub.buf, "if (%s) {\n", na.cond)
		} else {
			fmt.Fprintf(&sub.buf, "} else if (%s) {\n", na.cond)
		}
		sub.ind++
		if err := emitBranch(na.arm, na.ep); err != nil {
			return "", err
		}
		sub.ind--
	}
	if defaultArm != nil {
		if len(namedArms) > 0 {
			sub.indent()
			sub.writeln("} else {")
		} else {
			sub.indent()
			sub.writeln("{")
		}
		sub.ind++
		// Bind catch-all name if BindPat.
		if bp, ok := defaultArm.Pat.(*ast.BindPat); ok {
			sub.indent()
			fmt.Fprintf(&sub.buf, "%s %s = __s;\n", cTypeOf(scrutTy), bp.Name)
		}
		if err := sub.emitArmBody(defaultArm.Body, isUnit); err != nil {
			return "", err
		}
		sub.ind--
		sub.indent()
		sub.writeln("}")
	} else if len(namedArms) > 0 {
		sub.indent()
		sub.writeln("}")
	}

	sub.indent()
	if isUnit {
		sub.writeln("(void)0;")
	} else {
		sub.writeln("__r;")
	}
	sub.ind--
	sub.indent()
	sub.write("})")
	return sub.buf.String(), nil
}

func (e *emitter) emitArmBody(body ast.Expr, isUnit bool) error {
	if blk, ok := body.(*ast.Block); ok {
		for _, s := range blk.Stmts {
			if err := e.emitStmt(s); err != nil {
				return err
			}
		}
		if blk.Tail == nil {
			return nil
		}
		if isUnit {
			return e.emitExprAsStmt(blk.Tail)
		}
		// A `return` in an arm body diverges; emit it as a statement and
		// skip the __r assignment (control never reaches it).
		if _, isRet := blk.Tail.(*ast.ReturnExpr); isRet {
			return e.emitExprAsStmt(blk.Tail)
		}
		v, err := e.emitExpr(blk.Tail)
		if err != nil {
			return err
		}
		e.indent()
		fmt.Fprintf(&e.buf, "__r = %s;\n", v)
		return nil
	}
	if isUnit {
		return e.emitExprAsStmt(body)
	}
	if _, isRet := body.(*ast.ReturnExpr); isRet {
		return e.emitExprAsStmt(body)
	}
	v, err := e.emitExpr(body)
	if err != nil {
		return err
	}
	e.indent()
	fmt.Fprintf(&e.buf, "__r = %s;\n", v)
	return nil
}

func patVariantName(p ast.Pattern) (string, bool) {
	if ep, ok := p.(*ast.EnumPat); ok && len(ep.Path) == 2 {
		return ep.Path[1], true
	}
	return "", false
}

func hasCatchAll(arms []ast.MatchArm) bool {
	for _, a := range arms {
		switch a.Pat.(type) {
		case *ast.WildcardPat, *ast.BindPat:
			return true
		}
	}
	return false
}

func isCatchAll(p ast.Pattern) bool {
	switch p.(type) {
	case *ast.WildcardPat, *ast.BindPat:
		return true
	}
	return false
}
