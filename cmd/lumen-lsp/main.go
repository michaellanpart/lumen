// lumen-lsp implements a Language Server Protocol server for the Lumen
// programming language. It communicates over stdio using JSON-RPC 2.0 and
// supports:
//   - textDocument/didOpen, didChange, didClose  → live diagnostics
//   - textDocument/hover                         → keyword docs + builtin sigs
//   - textDocument/completion                    → keyword + builtin completions
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/lumen-lang/lumen/internal/parser"
	"github.com/lumen-lang/lumen/internal/types"
)

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 wire types
// ---------------------------------------------------------------------------

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// LSP basic types
// ---------------------------------------------------------------------------

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"` // 1=error 2=warning 3=info
	Message  string   `json:"message"`
	Source   string   `json:"source"`
}

type completionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind"` // 14=keyword, 3=function, 7=field
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// ---------------------------------------------------------------------------
// Document store
// ---------------------------------------------------------------------------

type docStore struct {
	mu   sync.RWMutex
	docs map[string]string // uri → text
}

func (d *docStore) set(uri, text string) {
	d.mu.Lock()
	d.docs[uri] = text
	d.mu.Unlock()
}

func (d *docStore) get(uri string) (string, bool) {
	d.mu.RLock()
	t, ok := d.docs[uri]
	d.mu.RUnlock()
	return t, ok
}

func (d *docStore) del(uri string) {
	d.mu.Lock()
	delete(d.docs, uri)
	d.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Static knowledge tables
// ---------------------------------------------------------------------------

var keywordDocs = map[string]string{
	"fn":     "**fn** — declare a function\n```lumen\nfn name(param: Type) -> ReturnType { ... }\n```",
	"func":   "**func** — declare a function or closure\n```lumen\nfunc name(param: Type) -> ReturnType { ... }\n```",
	"let":    "**let** — immutable binding\n```lumen\nlet x: i64 = 42;\n```",
	"var":    "**var** — typed variable declaration\n```lumen\nvar x: i64 = 42;\n```",
	"mut":    "**mut** — mark a binding as mutable",
	"return": "**return** — return a value from a function",
	"if":     "**if** — conditional expression\n```lumen\nif cond { a } else { b }\n```",
	"else":   "**else** — alternative branch of an `if` expression",
	"while":  "**while** — loop while condition is true\n```lumen\nwhile cond { ... }\n```",
	"for":    "**for** — iterate over a range or collection\n```lumen\nfor x in vec { ... }\n```",
	"in":     "**in** — iterate over a value (`for x in collection`)",
	"match":  "**match** — exhaustive pattern match\n```lumen\nmatch x {\n    Some(v) => v,\n    None    => 0,\n}\n```",
	"struct": "**struct** — declare a product type\n```lumen\nstruct Point { x: f64, y: f64 }\n```",
	"enum":   "**enum** — declare a sum type (algebraic data type)\n```lumen\nenum Shape {\n    Circle(f64),\n    Rect(f64, f64),\n}\n```",
	"impl":   "**impl** — implement methods for a type\n```lumen\nimpl Point {\n    fn distance(&self) -> f64 { ... }\n}\n```",
	"import": "**import** — import another Lumen module\n```lumen\nimport \"path/to/module\"\n```",
	"self":   "**self** — receiver in a method (`&self`, `&mut self`, or `self`)",
	"true":   "**true** — boolean literal",
	"false":  "**false** — boolean literal",
	"_":      "**_** — wildcard / ignored binding in a pattern",
	"spawn":  "**spawn** — launch a concurrent task\n```lumen\nspawn { ... }\n```",
	"move":   "**move** — transfer ownership into a closure",
}

var builtinDocs = map[string]string{
	"println":   "**println**(value, ...) — print values followed by a newline",
	"print":     "**print**(value, ...) — print values without a trailing newline",
	"len":       "**len**(s: String | Array) -> i64 — length of a string or fixed array",
	"fmt":       "**fmt**(template: String, args...) -> String — format a string\n\nUse `{}` placeholders. Supports `i64`, `f64`, `bool`, `String`.",
	"parse_int": "**parse_int**(s: String) -> Option<i64> — parse a string as an integer",
	"http_serve": "**http_serve**(host: String, port: i64, body: String)\n\nStart a minimal HTTP server that replies to every request with `body`.",
	"http_serve_fn": "**http_serve_fn**(host: String, port: i64, handler: func(String) -> String, svc: T)\n\nStart an HTTP server; `handler` is called for each request with the service context.",
	"http_serve_req": "**http_serve_req**(host: String, port: i64, handler: func(Request) -> Response)\n\nStart a full HTTP server with structured `Request`/`Response` objects.",
}

var typeKeywords = map[string]string{
	"i64":    "**i64** — 64-bit signed integer",
	"f64":    "**f64** — 64-bit floating-point number",
	"bool":   "**bool** — boolean (`true` / `false`)",
	"String": "**String** — heap-allocated UTF-8 string (`const char *` at C level)",
	"Option": "**Option<T>** — nullable value\n\nVariants: `Option::Some(T)`, `Option::None()`",
	"Result": "**Result<T,E>** — value or error\n\nVariants: `Result::Ok(T)`, `Result::Err(E)`",
	"Vec":    "**Vec<T>** — growable array\n\nMethods: `push`, `pop`, `len`, `get`, `iter`",
	"HashMap": "**HashMap<K,V>** — hash map\n\nMethods: `insert`, `get`, `contains_key`, `remove`, `len`",
	"Request":  "**Request** — HTTP request\n\nFields: `method: String`, `path: String`, `query: String`, `body: String`",
	"Response": "**Response** — HTTP response\n\nConstructors: `Response::ok(body)`, `Response::with_status(status, body)`, `Response::json(body)`",
}

// ---------------------------------------------------------------------------
// Error position parser
// ---------------------------------------------------------------------------

// posRe matches "file:line:col: message" produced by token.Pos.String().
var posRe = regexp.MustCompile(`^[^:]+:(\d+):(\d+):\s*(.*)$`)

func parseDiag(errMsg string) (line, col int, msg string, ok bool) {
	m := posRe.FindStringSubmatch(errMsg)
	if m == nil {
		return 0, 0, errMsg, false
	}
	line, _ = strconv.Atoi(m[1])
	col, _ = strconv.Atoi(m[2])
	return line, col, m[3], true
}

func errsToDiags(errs []error) []diagnostic { //nolint
	out := make([]diagnostic, 0, len(errs))
	for _, e := range errs {
		s := e.Error()
		line, col, msg, ok := parseDiag(s)
		var r lspRange
		if ok && line > 0 {
			// LSP positions are 0-based.
			r = lspRange{
				Start: position{Line: line - 1, Character: col - 1},
				End:   position{Line: line - 1, Character: col},
			}
		}
		out = append(out, diagnostic{
			Range:    r,
			Severity: 1,
			Message:  msg,
			Source:   "lumen",
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Diagnostics: parse + type-check a document
// ---------------------------------------------------------------------------

func diagnose(src string) []diagnostic {
	prog, parseErrs := parser.Parse("<editor>", src)
	if len(parseErrs) > 0 {
		// Parser returns []string
		diags := make([]diagnostic, 0, len(parseErrs))
		for _, e := range parseErrs {
			line, col, msg, ok := parseDiag(e)
			var r lspRange
			if ok && line > 0 {
				r = lspRange{
					Start: position{Line: line - 1, Character: col - 1},
					End:   position{Line: line - 1, Character: col},
				}
			}
			diags = append(diags, diagnostic{Range: r, Severity: 1, Message: msg, Source: "lumen"})
		}
		return diags
	}
	if prog == nil {
		return []diagnostic{}
	}
	_, typeErrs := types.Check(prog)
	if len(typeErrs) > 0 {
		return errsToDiags(typeErrs)
	}
	return []diagnostic{}
}

// ---------------------------------------------------------------------------
// Hover: find the word under cursor and look it up
// ---------------------------------------------------------------------------

func wordAt(src string, line, char int) string {
	lines := strings.Split(src, "\n")
	if line >= len(lines) {
		return ""
	}
	row := lines[line]
	runes := []rune(row)
	if char >= len(runes) {
		char = len(runes) - 1
	}
	if char < 0 {
		return ""
	}
	// Expand left and right from cursor position.
	isIdent := func(r rune) bool {
		return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	l, r := char, char
	for l > 0 && isIdent(runes[l-1]) {
		l--
	}
	for r < len(runes) && isIdent(runes[r]) {
		r++
	}
	if l >= r {
		return ""
	}
	return string(runes[l:r])
}

func hoverInfo(word string) string {
	if d, ok := keywordDocs[word]; ok {
		return d
	}
	if d, ok := builtinDocs[word]; ok {
		return d
	}
	if d, ok := typeKeywords[word]; ok {
		return d
	}
	return ""
}

// ---------------------------------------------------------------------------
// Completions
// ---------------------------------------------------------------------------

var completionKeywords = []string{
	"fn", "func", "let", "var", "mut", "return",
	"if", "else", "while", "for", "in", "match",
	"struct", "enum", "impl", "import",
	"self", "true", "false", "spawn", "move",
}

var completionBuiltins = []string{
	"println", "print", "len", "fmt", "parse_int",
	"http_serve", "http_serve_fn", "http_serve_req",
}

var completionTypes = []string{
	"i64", "f64", "bool", "String",
	"Option", "Result", "Vec", "HashMap",
	"Request", "Response",
}

func buildCompletions() []completionItem {
	items := make([]completionItem, 0, 60)
	for _, kw := range completionKeywords {
		items = append(items, completionItem{Label: kw, Kind: 14, Detail: "keyword", Documentation: keywordDocs[kw]})
	}
	for _, b := range completionBuiltins {
		items = append(items, completionItem{Label: b, Kind: 3, Detail: "builtin function", Documentation: builtinDocs[b]})
	}
	for _, t := range completionTypes {
		items = append(items, completionItem{Label: t, Kind: 7, Detail: "type", Documentation: typeKeywords[t]})
	}
	return items
}

var cachedCompletions = buildCompletions()

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

func readMsg(r *bufio.Reader) ([]byte, error) {
	// Read Content-Length header(s).
	contentLen := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length: ") {
			n, _ := strconv.Atoi(strings.TrimPrefix(line, "Content-Length: "))
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMsg(w io.Writer, v interface{}) {
	body, _ := json.Marshal(v)
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	fmt.Fprint(w, header)
	w.Write(body)
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type server struct {
	w    *bufio.Writer
	docs *docStore
}

func (s *server) send(id json.RawMessage, result interface{}) {
	writeMsg(s.w, rpcMsg{JSONRPC: "2.0", ID: id, Result: result})
	s.w.Flush()
}

func (s *server) sendErr(id json.RawMessage, code int, msg string) {
	writeMsg(s.w, rpcMsg{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	s.w.Flush()
}

func (s *server) notify(method string, params interface{}) {
	writeMsg(s.w, rpcMsg{JSONRPC: "2.0", Method: method, Params: mustMarshal(params)})
	s.w.Flush()
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func (s *server) publishDiags(uri, text string) {
	diags := diagnose(text)
	s.notify("textDocument/publishDiagnostics", map[string]interface{}{
		"uri":         uri,
		"diagnostics": diags,
	})
}

func (s *server) handle(raw []byte) {
	var msg rpcMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}

	switch msg.Method {
	case "initialize":
		s.send(msg.ID, map[string]interface{}{
			"capabilities": map[string]interface{}{
				"textDocumentSync": map[string]interface{}{
					"openClose": true,
					"change":    1, // full sync
				},
				"hoverProvider":      true,
				"completionProvider": map[string]interface{}{"triggerCharacters": []string{}},
			},
			"serverInfo": map[string]string{
				"name":    "lumen-lsp",
				"version": "0.1.0",
			},
		})

	case "initialized":
		// no-op

	case "shutdown":
		s.send(msg.ID, nil)

	case "exit":
		os.Exit(0)

	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.docs.set(p.TextDocument.URI, p.TextDocument.Text)
			s.publishDiags(p.TextDocument.URI, p.TextDocument.Text)
		}

	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		if json.Unmarshal(msg.Params, &p) == nil && len(p.ContentChanges) > 0 {
			text := p.ContentChanges[len(p.ContentChanges)-1].Text
			s.docs.set(p.TextDocument.URI, text)
			s.publishDiags(p.TextDocument.URI, text)
		}

	case "textDocument/didClose":
		var p struct {
			TextDocument struct{ URI string `json:"uri"` } `json:"textDocument"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.docs.del(p.TextDocument.URI)
			// Clear diagnostics.
			s.notify("textDocument/publishDiagnostics", map[string]interface{}{
				"uri": p.TextDocument.URI, "diagnostics": []interface{}{},
			})
		}

	case "textDocument/hover":
		var p struct {
			TextDocument struct{ URI string `json:"uri"` } `json:"textDocument"`
			Position     position                           `json:"position"`
		}
		if json.Unmarshal(msg.Params, &p) != nil {
			s.send(msg.ID, nil)
			return
		}
		text, ok := s.docs.get(p.TextDocument.URI)
		if !ok {
			s.send(msg.ID, nil)
			return
		}
		word := wordAt(text, p.Position.Line, p.Position.Character)
		info := hoverInfo(word)
		if info == "" {
			s.send(msg.ID, nil)
			return
		}
		s.send(msg.ID, hoverResult{Contents: markupContent{Kind: "markdown", Value: info}})

	case "textDocument/completion":
		s.send(msg.ID, map[string]interface{}{
			"isIncomplete": false,
			"items":        cachedCompletions,
		})

	default:
		if msg.ID != nil {
			s.sendErr(msg.ID, -32601, "method not found: "+msg.Method)
		}
	}
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	log.SetOutput(os.Stderr)
	log.SetPrefix("[lumen-lsp] ")
	log.SetFlags(0)

	r := bufio.NewReader(os.Stdin)
	w := bufio.NewWriter(os.Stdout)

	srv := &server{
		w:    w,
		docs: &docStore{docs: map[string]string{}},
	}

	for {
		raw, err := readMsg(r)
		if err != nil {
			if err != io.EOF {
				log.Printf("read error: %v", err)
			}
			return
		}
		srv.handle(raw)
	}
}
