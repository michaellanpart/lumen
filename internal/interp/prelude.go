package interp

import (
	"fmt"
	"strings"
)

// registerPrelude defines the built-in functions and types that are always
// available to a Lumen program.
func registerPrelude(in *Interpreter) {
	g := in.globals

	// I/O
	g.Define("print", &BuiltinV{Name: "print", Fn: func(args []Value) (Value, error) {
		fmt.Print(formatArgs(args))
		return &UnitV{}, nil
	}})
	g.Define("println", &BuiltinV{Name: "println", Fn: func(args []Value) (Value, error) {
		fmt.Println(formatArgs(args))
		return &UnitV{}, nil
	}})
	g.Define("eprintln", &BuiltinV{Name: "eprintln", Fn: func(args []Value) (Value, error) {
		fmt.Fprintln(stderrish{}, formatArgs(args))
		return &UnitV{}, nil
	}})

	// `panic!` (regular fn for now)
	g.Define("panic", &BuiltinV{Name: "panic", Fn: func(args []Value) (Value, error) {
		return nil, fmt.Errorf("panic: %s", formatArgs(args))
	}})

	// Constructors / helpers
	g.Define("Vec", &BuiltinV{Name: "Vec", Fn: func(args []Value) (Value, error) {
		return &ArrayV{Elems: append([]Value{}, args...)}, nil
	}})
	g.Define("range", &BuiltinV{Name: "range", Fn: func(args []Value) (Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("range expects (start, end)")
		}
		a, ok1 := args[0].(*IntV)
		b, ok2 := args[1].(*IntV)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("range expects int args")
		}
		var out []Value
		for i := a.V; i < b.V; i++ {
			out = append(out, &IntV{V: i})
		}
		return &ArrayV{Elems: out}, nil
	}})
	g.Define("len", &BuiltinV{Name: "len", Fn: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("len expects 1 argument")
		}
		v := args[0]
		if r, ok := v.(*RefV); ok {
			v = *r.To
		}
		switch x := v.(type) {
		case *ArrayV:
			return &IntV{V: int64(len(x.Elems))}, nil
		case *StringV:
			return &IntV{V: int64(len(x.V))}, nil
		case *TupleV:
			return &IntV{V: int64(len(x.Elems))}, nil
		}
		return nil, fmt.Errorf("len: unsupported %T", v)
	}})

	// Built-in Option / Result variant constructors at the global path level,
	// so user code can write `Some(x)`, `None`, `Ok(x)`, `Err(e)` directly.
	g.Define("Some", &BuiltinV{Name: "Some", Fn: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Some expects 1 argument")
		}
		return &EnumV{Enum: "Option", Variant: "Some", Tuple: []Value{args[0]}}, nil
	}})
	g.Define("None", &EnumV{Enum: "Option", Variant: "None"})
	g.Define("Ok", &BuiltinV{Name: "Ok", Fn: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Ok expects 1 argument")
		}
		return &EnumV{Enum: "Result", Variant: "Ok", Tuple: []Value{args[0]}}, nil
	}})
	g.Define("Err", &BuiltinV{Name: "Err", Fn: func(args []Value) (Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("Err expects 1 argument")
		}
		return &EnumV{Enum: "Result", Variant: "Err", Tuple: []Value{args[0]}}, nil
	}})
	g.Define("Option::Some", g.mustGet("Some"))
	g.Define("Option::None", g.mustGet("None"))
	g.Define("Result::Ok", g.mustGet("Ok"))
	g.Define("Result::Err", g.mustGet("Err"))

	// runtime bridges (networking, JSON, etc.)
	registerHTTP(in)
}

func (e *Env) mustGet(name string) Value {
	v, _ := e.Get(name)
	return v
}

func formatArgs(args []Value) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = Show(a)
	}
	return strings.Join(parts, " ")
}

type stderrish struct{}

func (stderrish) Write(p []byte) (int, error) { return fmt.Print(string(p)) }

// builtinMethod returns a callable that implements a method on a builtin type,
// or false if no such method exists.
func builtinMethod(recv Value, name string) (func(args []Value) (Value, error), bool) {
	if r, ok := recv.(*RefV); ok {
		recv = *r.To
	}
	switch x := recv.(type) {
	case *ArrayV:
		switch name {
		case "len":
			return func(_ []Value) (Value, error) { return &IntV{V: int64(len(x.Elems))}, nil }, true
		case "push":
			return func(args []Value) (Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("push expects 1 arg")
				}
				x.Elems = append(x.Elems, args[0])
				return &UnitV{}, nil
			}, true
		case "pop":
			return func(_ []Value) (Value, error) {
				if len(x.Elems) == 0 {
					return &EnumV{Enum: "Option", Variant: "None"}, nil
				}
				last := x.Elems[len(x.Elems)-1]
				x.Elems = x.Elems[:len(x.Elems)-1]
				return &EnumV{Enum: "Option", Variant: "Some", Tuple: []Value{last}}, nil
			}, true
		case "get":
			return func(args []Value) (Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("get expects 1 arg")
				}
				i, ok := args[0].(*IntV)
				if !ok {
					return nil, fmt.Errorf("get expects int")
				}
				if i.V < 0 || int(i.V) >= len(x.Elems) {
					return &EnumV{Enum: "Option", Variant: "None"}, nil
				}
				return &EnumV{Enum: "Option", Variant: "Some", Tuple: []Value{x.Elems[i.V]}}, nil
			}, true
		}
	case *StringV:
		switch name {
		case "len":
			return func(_ []Value) (Value, error) { return &IntV{V: int64(len(x.V))}, nil }, true
		case "to_upper":
			return func(_ []Value) (Value, error) { return &StringV{V: strings.ToUpper(x.V)}, nil }, true
		case "to_lower":
			return func(_ []Value) (Value, error) { return &StringV{V: strings.ToLower(x.V)}, nil }, true
		case "trim":
			return func(_ []Value) (Value, error) { return &StringV{V: strings.TrimSpace(x.V)}, nil }, true
		case "contains":
			return func(args []Value) (Value, error) {
				s, ok := args[0].(*StringV)
				if !ok {
					return nil, fmt.Errorf("contains expects String")
				}
				return &BoolV{V: strings.Contains(x.V, s.V)}, nil
			}, true
		}
	case *EnumV:
		switch name {
		case "is_some":
			return func(_ []Value) (Value, error) { return &BoolV{V: x.Variant == "Some"}, nil }, true
		case "is_none":
			return func(_ []Value) (Value, error) { return &BoolV{V: x.Variant == "None"}, nil }, true
		case "is_ok":
			return func(_ []Value) (Value, error) { return &BoolV{V: x.Variant == "Ok"}, nil }, true
		case "is_err":
			return func(_ []Value) (Value, error) { return &BoolV{V: x.Variant == "Err"}, nil }, true
		case "unwrap":
			return func(_ []Value) (Value, error) {
				if (x.Variant == "Some" || x.Variant == "Ok") && len(x.Tuple) == 1 {
					return x.Tuple[0], nil
				}
				return nil, fmt.Errorf("unwrap on %s::%s", x.Enum, x.Variant)
			}, true
		case "unwrap_or":
			return func(args []Value) (Value, error) {
				if (x.Variant == "Some" || x.Variant == "Ok") && len(x.Tuple) == 1 {
					return x.Tuple[0], nil
				}
				if len(args) == 1 {
					return args[0], nil
				}
				return nil, fmt.Errorf("unwrap_or expects 1 arg")
			}, true
		}
	case *IntV:
		switch name {
		case "to_string":
			return func(_ []Value) (Value, error) { return &StringV{V: fmt.Sprintf("%d", x.V)}, nil }, true
		}
	case *FloatV:
		switch name {
		case "to_string":
			return func(_ []Value) (Value, error) { return &StringV{V: fmt.Sprintf("%g", x.V)}, nil }, true
		}
	}
	return nil, false
}
