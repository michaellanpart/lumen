package interp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// registerHTTP installs the `http::` module and `Response::` constructors.
// This is the runtime bridge that lets pure Lumen code drive a real
// HTTP server. The Lumen surface stays unchanged — only the implementation
// of the leaf builtins lives in Go for now.
func registerHTTP(in *Interpreter) {
	g := in.globals

	g.Define("Response::ok", &BuiltinV{Name: "Response::ok", Fn: func(args []Value) (Value, error) {
		body := ""
		if len(args) >= 1 {
			body = stringify(args[0])
		}
		return newResponse(200, body, "text/plain; charset=utf-8"), nil
	}})

	g.Define("Response::with_status", &BuiltinV{Name: "Response::with_status", Fn: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("Response::with_status expects (status, body)")
		}
		status, ok := args[0].(*IntV)
		if !ok {
			return nil, fmt.Errorf("status must be int")
		}
		return newResponse(status.V, stringify(args[1]), "text/plain; charset=utf-8"), nil
	}})

	g.Define("Response::json", &BuiltinV{Name: "Response::json", Fn: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Response::json expects 1 argument")
		}
		body, err := lumenToJSON(args[0])
		if err != nil {
			return nil, err
		}
		return newResponse(200, body, "application/json"), nil
	}})

	g.Define("Response::json_status", &BuiltinV{Name: "Response::json_status", Fn: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("Response::json_status expects (status, body)")
		}
		status, ok := args[0].(*IntV)
		if !ok {
			return nil, fmt.Errorf("Response::json_status: status must be int")
		}
		body, err := lumenToJSON(args[1])
		if err != nil {
			return nil, err
		}
		return newResponse(status.V, body, "application/json"), nil
	}})

	g.Define("json::encode", &BuiltinV{Name: "json::encode", Fn: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("json::encode expects 1 argument")
		}
		s, err := lumenToJSON(args[0])
		if err != nil {
			return nil, err
		}
		return &StringV{V: s}, nil
	}})

	g.Define("http::serve", &BuiltinV{Name: "http::serve", Fn: func(args []Value) (Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("http::serve expects (addr, routes [, fallback])")
		}
		addrV, ok := args[0].(*StringV)
		if !ok {
			return nil, fmt.Errorf("addr must be String")
		}
		routesArr, ok := args[1].(*ArrayV)
		if !ok {
			return nil, fmt.Errorf("routes must be Vec")
		}
		type route struct {
			method, path string
			handler      Value
		}
		var routes []route
		for _, r := range routesArr.Elems {
			t, ok := r.(*TupleV)
			if !ok || len(t.Elems) != 3 {
				return nil, fmt.Errorf("each route must be a tuple (method, path, handler)")
			}
			m, _ := t.Elems[0].(*StringV)
			p, _ := t.Elems[1].(*StringV)
			if m == nil || p == nil {
				return nil, fmt.Errorf("route method/path must be String")
			}
			routes = append(routes, route{m.V, p.V, t.Elems[2]})
		}
		var fallback Value
		if len(args) >= 3 {
			fallback = args[2]
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
			bodyBytes, _ := io.ReadAll(req.Body)
			lreq := &StructV{
				Name: "Request",
				Fields: map[string]Value{
					"method": &StringV{V: req.Method},
					"path":   &StringV{V: req.URL.Path},
					"query":  &StringV{V: req.URL.RawQuery},
					"body":   &StringV{V: string(bodyBytes)},
				},
				Order: []string{"method", "path", "query", "body"},
			}
			var handler Value
			for _, r := range routes {
				if r.method == req.Method && r.path == req.URL.Path {
					handler = r.handler
					break
				}
			}
			if handler == nil {
				handler = fallback
			}
			if handler == nil {
				http.NotFound(w, req)
				return
			}
			resp, err := in.invokeCallable(handler, []Value{lreq})
			if err != nil {
				http.Error(w, "lumen handler error: "+err.Error(), http.StatusInternalServerError)
				return
			}
			writeHTTPResponse(w, resp)
		})

		return nil, http.ListenAndServe(addrV.V, mux)
	}})
}

func newResponse(status int64, body, ctype string) Value {
	return &StructV{
		Name: "Response",
		Fields: map[string]Value{
			"status":       &IntV{V: status},
			"body":         &StringV{V: body},
			"content_type": &StringV{V: ctype},
		},
		Order: []string{"status", "body", "content_type"},
	}
}

func writeHTTPResponse(w http.ResponseWriter, v Value) {
	status := 200
	body := ""
	ctype := "text/plain; charset=utf-8"
	if r, ok := v.(*RefV); ok {
		v = *r.To
	}
	switch x := v.(type) {
	case *StructV:
		if sv, ok := x.Fields["status"].(*IntV); ok {
			status = int(sv.V)
		}
		if sv, ok := x.Fields["body"].(*StringV); ok {
			body = sv.V
		}
		if sv, ok := x.Fields["content_type"].(*StringV); ok {
			ctype = sv.V
		}
	case *StringV:
		body = x.V
	default:
		body = Show(v)
	}
	w.Header().Set("Content-Type", ctype)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func stringify(v Value) string {
	if s, ok := v.(*StringV); ok {
		return s.V
	}
	return Show(v)
}

func lumenToJSON(v Value) (string, error) {
	b, err := json.Marshal(lumenToGo(v))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func lumenToGo(v Value) any {
	if r, ok := v.(*RefV); ok {
		v = *r.To
	}
	switch x := v.(type) {
	case *IntV:
		return x.V
	case *FloatV:
		return x.V
	case *BoolV:
		return x.V
	case *StringV:
		return x.V
	case *CharV:
		return string(x.V)
	case *UnitV, nil:
		return nil
	case *ArrayV:
		out := make([]any, len(x.Elems))
		for i, e := range x.Elems {
			out[i] = lumenToGo(e)
		}
		return out
	case *TupleV:
		out := make([]any, len(x.Elems))
		for i, e := range x.Elems {
			out[i] = lumenToGo(e)
		}
		return out
	case *StructV:
		m := map[string]any{}
		for _, k := range x.Order {
			m[k] = lumenToGo(x.Fields[k])
		}
		return m
	case *EnumV:
		switch {
		case len(x.Tuple) == 1:
			return map[string]any{x.Variant: lumenToGo(x.Tuple[0])}
		case len(x.Tuple) > 0:
			arr := make([]any, len(x.Tuple))
			for i, e := range x.Tuple {
				arr[i] = lumenToGo(e)
			}
			return map[string]any{x.Variant: arr}
		case len(x.Fields) > 0:
			inner := map[string]any{}
			for k, vv := range x.Fields {
				inner[k] = lumenToGo(vv)
			}
			return map[string]any{x.Variant: inner}
		default:
			return x.Variant
		}
	}
	return fmt.Sprintf("%v", v)
}

// invokeCallable lets builtins call back into Lumen functions.
// Serialized via in.Mu so HTTP handlers (called from net/http goroutines)
// can't race on shared interpreter state.
func (in *Interpreter) invokeCallable(callee Value, args []Value) (Value, error) {
	in.Mu.Lock()
	defer in.Mu.Unlock()
	switch f := callee.(type) {
	case *FnV:
		return in.callFn(f, args)
	case *LambdaV:
		return in.callLambda(f, args)
	case *BuiltinV:
		return f.Fn(args)
	}
	return nil, fmt.Errorf("not callable: %s", Show(callee))
}
