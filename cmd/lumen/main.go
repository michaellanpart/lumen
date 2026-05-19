// Command lumen is the Lumen toolchain driver: build/run/repl/tokens/ast.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lumen-lang/lumen/internal/ast"
	"github.com/lumen-lang/lumen/internal/borrowck"
	"github.com/lumen-lang/lumen/internal/cbackend"
	"github.com/lumen-lang/lumen/internal/infer"
	"github.com/lumen-lang/lumen/internal/interp"
	"github.com/lumen-lang/lumen/internal/lexer"
	"github.com/lumen-lang/lumen/internal/parser"
	"github.com/lumen-lang/lumen/internal/types"
)

const usage = `Lumen v0.2 — a research systems language

Usage:
  lumen run    <file.lm>                       Parse and execute a Lumen program
  lumen build  <file.lm> [-o out] [flags]      Produce a standalone executable
                  --target=c|interp            c (default): AOT-compile via cc
                                               interp:      embed source into a
                                                            self-extracting binary
                  --keep-c                     Keep generated C source (target=c)
                  --cc=PATH                    C compiler to use (default: $CC or cc)
  lumen tokens <file.lm>                       Print the token stream
  lumen ast    <file.lm>                       Print a brief AST summary
  lumen repl                                   Start an interactive REPL
  lumen version                                Print version info
`

// lumenMagic marks the end of an embedded program in a standalone binary.
// Layout at end of file:  <source bytes> <8-byte LE length> <8-byte magic>
var lumenMagic = [8]byte{'L', 'M', 'N', 'B', 'I', 'N', '0', '1'}

func main() {
	// If this binary has an embedded program (produced by `lumen build`),
	// run it directly and ignore CLI subcommands.
	if src, name, ok := readEmbedded(); ok {
		runSource(name, src)
		return
	}
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "run":
		mustArgs(3)
		runFile(os.Args[2])
	case "build":
		mustArgs(3)
		buildStandalone(os.Args[2:])
	case "tokens":
		mustArgs(3)
		printTokens(os.Args[2])
	case "ast":
		mustArgs(3)
		printAST(os.Args[2])
	case "repl":
		repl()
	case "version", "-v", "--version":
		fmt.Println("lumen 0.2.0 (interpreter + C AOT backend)")
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func mustArgs(n int) {
	if len(os.Args) < n {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lumen: %s\n", err)
		os.Exit(1)
	}
	return string(b)
}

func runFile(path string) {
	prog, errs := loadProgramWithImports(path)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		os.Exit(1)
	}
	in := interp.New()
	if err := in.Run(prog); err != nil {
		fmt.Fprintln(os.Stderr, "runtime error:", err)
		os.Exit(1)
	}
}

func runSource(name, src string) {
	prog, errs := parser.Parse(name, src)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		os.Exit(1)
	}
	in := interp.New()
	if err := in.Run(prog); err != nil {
		fmt.Fprintln(os.Stderr, "runtime error:", err)
		os.Exit(1)
	}
}

// buildStandalone dispatches to the requested backend. The default target
// is `c` — we type-check the program and AOT-compile through the system C
// compiler. Target `interp` falls back to embedding the source into a
// self-extracting copy of the lumen toolchain (the v0.1 model).
func buildStandalone(args []string) {
	srcPath := args[0]
	out := strings.TrimSuffix(srcPath, ".lm")
	target := "c"
	keepC := false
	cc := ""
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" && i+1 < len(args):
			out = args[i+1]
			i++
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
		case a == "--keep-c":
			keepC = true
		case strings.HasPrefix(a, "--cc="):
			cc = strings.TrimPrefix(a, "--cc=")
		default:
			fmt.Fprintf(os.Stderr, "lumen build: unknown flag %q\n", a)
			os.Exit(2)
		}
	}
	src := readFile(srcPath)
	prog, errs := loadProgramWithImports(srcPath)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		os.Exit(1)
	}

	switch target {
	case "c":
		info, terrs := types.Check(prog)
		if len(terrs) > 0 {
			for _, e := range terrs {
				fmt.Fprintln(os.Stderr, "type error:", e)
			}
			fmt.Fprintln(os.Stderr, "lumen build: type-check failed; try --target=interp to fall back to the interpreter.")
			os.Exit(1)
		}
		infer.SelfMut(prog, info)
		infer.ParamMut(prog, info)
		infer.ParamBorrow(prog, info)
		infer.Mutability(prog, info)
		if berrs := borrowck.Check(prog, info); len(berrs) > 0 {
			for _, e := range berrs {
				fmt.Fprintln(os.Stderr, e)
			}
			fmt.Fprintln(os.Stderr, "lumen build: move/borrow check failed.")
			os.Exit(1)
		}
		if err := cbackend.Compile(prog, info, cbackend.Options{
			Output: out,
			KeepC:  keepC,
			CC:     cc,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "lumen build:", err)
			os.Exit(1)
		}
		if fi, err := os.Stat(out); err == nil {
			fmt.Fprintf(os.Stderr, "lumen build: wrote %s (%d bytes, native AOT)\n", out, fi.Size())
		}
	case "interp":
		buildStandaloneInterp(srcPath, src, out)
	default:
		fmt.Fprintf(os.Stderr, "lumen build: unknown target %q (expected c|interp)\n", target)
		os.Exit(2)
	}
}

// loadProgramWithImports parses srcPath and recursively loads `import`ed
// sibling modules (`import "foo"` or import blocks). Imported modules are
// flattened into one compilation unit in dependency-first order.
func loadProgramWithImports(srcPath string) (*ast.Program, []string) {
	entry, err := filepath.Abs(srcPath)
	if err != nil {
		entry = srcPath
	}
	visited := map[string]bool{}
	visiting := map[string]bool{}
	items := []ast.Item{}
	var errs []string

	var walk func(path string)
	walk = func(path string) {
		if visited[path] || len(errs) > 0 {
			return
		}
		if visiting[path] {
			errs = append(errs, fmt.Sprintf("import cycle detected at %s", path))
			return
		}
		visiting[path] = true

		srcBytes, rerr := os.ReadFile(path)
		if rerr != nil {
			errs = append(errs, rerr.Error())
			delete(visiting, path)
			return
		}
		src := string(srcBytes)
		for _, imp := range parseImports(path, src) {
			dep := imp
			if !filepath.IsAbs(dep) {
				dep = filepath.Join(filepath.Dir(path), dep)
			}
			if filepath.Ext(dep) == "" {
				dep += ".lm"
			}
			if abs, aerr := filepath.Abs(dep); aerr == nil {
				dep = abs
			}
			walk(dep)
		}

		prog, perrs := parser.Parse(path, src)
		if len(perrs) > 0 {
			errs = append(errs, perrs...)
			delete(visiting, path)
			return
		}
		items = append(items, prog.Items...)
		visited[path] = true
		delete(visiting, path)
	}

	walk(entry)
	if len(errs) > 0 {
		return nil, errs
	}
	return &ast.Program{File: entry, Items: items}, nil
}

// parseImports extracts import spec strings from either:
//   import "path"
//   import (
//     "a"
//     "b"
//   )
func parseImports(filePath, src string) []string {
	var out []string
	inBlock := false
	s := bufio.NewScanner(strings.NewReader(src))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if inBlock {
			if line == ")" {
				inBlock = false
				continue
			}
			if strings.HasPrefix(line, "\"") {
				if j := strings.Index(line[1:], "\""); j >= 0 {
					out = append(out, line[1:1+j])
				}
			}
			continue
		}
		if !strings.HasPrefix(line, "import") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "import"))
		if rest == "(" {
			inBlock = true
			continue
		}
		if strings.HasPrefix(rest, "\"") {
			if j := strings.Index(rest[1:], "\""); j >= 0 {
				out = append(out, rest[1:1+j])
			}
		}
	}
	_ = s.Err()
	_ = filePath
	return out
}

// buildStandaloneInterp implements the v0.1 self-extracting build target:
// copy the running lumen binary and append the program source so the result
// runs without any toolchain present.
func buildStandaloneInterp(srcPath, src, out string) {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	selfBytes, err := os.ReadFile(self)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	if _, _, ok := readEmbeddedFromBytes(selfBytes); ok {
		selfBytes = stripEmbedded(selfBytes)
	}

	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	defer f.Close()
	if _, err := f.Write(selfBytes); err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	if _, err := f.Write([]byte(src)); err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	var lenBuf [8]byte
	binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(src)))
	if _, err := f.Write(lenBuf[:]); err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	if _, err := f.Write(lumenMagic[:]); err != nil {
		fmt.Fprintln(os.Stderr, "lumen build:", err)
		os.Exit(1)
	}
	fi, _ := f.Stat()
	fmt.Fprintf(os.Stderr, "lumen build: wrote %s (%d bytes, %d bytes of Lumen source embedded, target=interp)\n",
		out, fi.Size(), len(src))
	_ = srcPath
}

func readEmbedded() (src, name string, ok bool) {
	self, err := os.Executable()
	if err != nil {
		return "", "", false
	}
	f, err := os.Open(self)
	if err != nil {
		return "", "", false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() < int64(len(lumenMagic)+8) {
		return "", "", false
	}
	trailer := make([]byte, len(lumenMagic)+8)
	if _, err := f.ReadAt(trailer, fi.Size()-int64(len(trailer))); err != nil {
		return "", "", false
	}
	if string(trailer[8:]) != string(lumenMagic[:]) {
		return "", "", false
	}
	srcLen := int64(binary.LittleEndian.Uint64(trailer[:8]))
	if srcLen <= 0 || srcLen > fi.Size()-int64(len(trailer)) {
		return "", "", false
	}
	buf := make([]byte, srcLen)
	if _, err := f.ReadAt(buf, fi.Size()-int64(len(trailer))-srcLen); err != nil && err != io.EOF {
		return "", "", false
	}
	return string(buf), "<embedded>", true
}

func readEmbeddedFromBytes(b []byte) (src, name string, ok bool) {
	trailerLen := len(lumenMagic) + 8
	if len(b) < trailerLen {
		return "", "", false
	}
	trailer := b[len(b)-trailerLen:]
	if string(trailer[8:]) != string(lumenMagic[:]) {
		return "", "", false
	}
	srcLen := int(binary.LittleEndian.Uint64(trailer[:8]))
	if srcLen <= 0 || srcLen > len(b)-trailerLen {
		return "", "", false
	}
	start := len(b) - trailerLen - srcLen
	return string(b[start : start+srcLen]), "<embedded>", true
}

func stripEmbedded(b []byte) []byte {
	trailerLen := len(lumenMagic) + 8
	if len(b) < trailerLen {
		return b
	}
	trailer := b[len(b)-trailerLen:]
	if string(trailer[8:]) != string(lumenMagic[:]) {
		return b
	}
	srcLen := int(binary.LittleEndian.Uint64(trailer[:8]))
	if srcLen <= 0 || srcLen > len(b)-trailerLen {
		return b
	}
	return b[:len(b)-trailerLen-srcLen]
}

func printTokens(path string) {
	src := readFile(path)
	lx := lexer.New(path, src)
	for _, t := range lx.Tokenize() {
		fmt.Println(t)
	}
	for _, e := range lx.Errors() {
		fmt.Fprintln(os.Stderr, "lex error:", e)
	}
}

func printAST(path string) {
	src := readFile(path)
	prog, errs := parser.Parse(path, src)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "error:", e)
	}
	fmt.Printf("Program %s (%d items)\n", prog.File, len(prog.Items))
	for _, it := range prog.Items {
		fmt.Printf("  %T at %s\n", it, it.Pos())
	}
}

func repl() {
	fmt.Println("Lumen REPL — type `:quit` to exit, `:load <file>` to load a file.")
	in := interp.New()
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var buf strings.Builder
	prompt := func() {
		if buf.Len() == 0 {
			fmt.Print("λ> ")
		} else {
			fmt.Print("... ")
		}
	}
	prompt()
	for sc.Scan() {
		line := sc.Text()
		switch strings.TrimSpace(line) {
		case ":quit", ":q":
			return
		}
		if strings.HasPrefix(strings.TrimSpace(line), ":load ") {
			path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ":load "))
			src := readFile(path)
			prog, errs := parser.Parse(path, src)
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			if err := in.Load(prog); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			buf.Reset()
			prompt()
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
		// crude balance check
		if !balanced(buf.String()) {
			prompt()
			continue
		}
		src := buf.String()
		buf.Reset()
		// Try as an expression first (wrap in a synthetic fn), then as items.
		if v, ok := tryEvalExpr(in, src); ok {
			if v != nil {
				fmt.Println(interp.Show(v))
			}
		} else {
			prog, errs := parser.Parse("<repl>", src)
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			if len(errs) == 0 {
				if err := in.Load(prog); err != nil {
					fmt.Fprintln(os.Stderr, err)
				}
			}
		}
		prompt()
	}
}

func tryEvalExpr(in *interp.Interpreter, src string) (interp.Value, bool) {
	wrapped := "fn __repl_expr__() { " + src + " }"
	prog, errs := parser.Parse("<repl>", wrapped)
	if len(errs) > 0 {
		return nil, false
	}
	if err := in.Load(prog); err != nil {
		return nil, false
	}
	// invoke it
	prog2, errs := parser.Parse("<repl>", "fn __repl_call__() { __repl_expr__() }")
	if len(errs) > 0 {
		return nil, false
	}
	_ = prog2
	// Easier: directly call through the interpreter's global lookup.
	// We use Run on a tiny program that calls __repl_expr__ and discards.
	caller, errs := parser.Parse("<repl>", "fn main() { __repl_expr__() }")
	if len(errs) > 0 {
		return nil, false
	}
	if err := in.Run(caller); err != nil {
		fmt.Fprintln(os.Stderr, "runtime error:", err)
	}
	return nil, true
}

func balanced(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
		}
	}
	return depth <= 0
}
