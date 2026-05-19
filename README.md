# Lumen

> A systems language that compiles to native code via a C99 backend.
> Designed for clarity, safety, and performance — without sacrificing expressiveness.

```lumen
fn fib(n: i64) -> i64 {
    match n {
        0 => 0,
        1 => 1,
        _ => fib(n - 1) + fib(n - 2),
    }
}

fn main() {
    let mut i: i64 = 0;
    while i < 10 {
        println(fib(i));
        i = i + 1;
    }
}
```

## What is Lumen?

Lumen is a statically-typed, compiled systems language. Source files (`.lm`) are
type-checked and lowered to C99, then compiled to a native binary via the system C
compiler (`cc -O2`). There is no garbage collector; memory is managed explicitly or
via scoped ownership.

This repository contains:

1. **The Lumen language** — specified in [docs/SPEC.md](docs/SPEC.md) and
   [docs/GRAMMAR.ebnf](docs/GRAMMAR.ebnf).
2. **The Lumen toolchain (`lumen`)** — written in Go, reads `.lm` files and
   compiles or interprets them. The long-term plan is to self-host once the
   language is stable.

## Current status

| Feature                                                    | Status        |
|------------------------------------------------------------|---------------|
| Lexer, parser, AST                                         | ✅ done        |
| Tree-walking interpreter + REPL                            | ✅ done        |
| Hindley-Milner type inference                              | ✅ done        |
| Borrow checker (basic)                                     | ✅ done        |
| AOT C99 backend — native binary output                     | ✅ done        |
| Structs, enums (ADTs), pattern matching                    | ✅ done        |
| `impl` blocks and methods                                  | ✅ done        |
| `Option<T>`, `Result<T,E>`, `?` operator                  | ✅ done        |
| `Vec<T>`, `HashMap<K,V>`, `String` methods                | ✅ done        |
| Closures — non-capturing and **capturing**                 | ✅ done        |
| For-in loops                                               | ✅ done        |
| Multi-file imports                                         | ✅ done        |
| HTTP server builtins (`http_serve`, `http_serve_req`)      | ✅ done        |
| Auto-borrow at call sites                                  | ✅ done        |
| Spawn / lightweight tasks                                  | ✅ basic       |
| Effect / capability system                                 | ⏳ planned     |
| Refinement types + SMT discharge                           | ⏳ planned     |
| Self-hosting                                               | ⏳ planned     |

## Build

```sh
make build         # produces ./bin/lumen
make test          # runs the full test suite
make run           # runs all example .lm programs
make repl          # interactive REPL
```

Requires Go 1.22+ and a C compiler (`cc` / `clang` / `gcc`) on `$PATH`.

## Test

```sh
go test ./...
```

Or use:

```sh
make test
```

## CLI

```
lumen run <file.lm>      compile and run a Lumen program (AOT, via C backend)
lumen interp <file.lm>   interpret a Lumen program (tree-walking)
lumen tokens <file.lm>   print the token stream
lumen ast <file.lm>      print a brief AST summary
lumen repl               interactive REPL
lumen version            print version info
```

## Quick example — capturing closure

```lumen
fn make_adder(x: i64) -> func(i64) -> i64 {
    return func(n: i64) -> i64 { return n + x }
}

fn main() {
    let add10 = make_adder(10);
    println(add10(32))   // 42
}
```

## Project layout

```
cmd/lumen/          Toolchain entry point (CLI)
internal/lexer/     Source → tokens
internal/parser/    Tokens → AST
internal/ast/       AST node definitions
internal/types/     Type checker (v0.3+ typed core)
internal/infer/     Hindley-Milner type inference
internal/borrowck/  Borrow checker
internal/interp/    Tree-walking interpreter + prelude
internal/cbackend/  AOT C99 code emitter
runtime/lumen.h     Header-only C99 runtime (embedded in all AOT binaries)
docs/SPEC.md        Full language specification
docs/GRAMMAR.ebnf   EBNF grammar reference
examples/           Example .lm programs
```
