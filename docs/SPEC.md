# Lumen Language Specification

> **Status:** Draft v0.1 — *living document*
>
> Lumen is a systems programming language designed to take the best ideas
> from **C, C++, Rust, and Go** and combine them into a single coherent,
> mathematically grounded design.

---

## 1. Design Philosophy

A programming language is, at its core, a formal system: a grammar plus a
semantics. Lumen treats both with mathematical rigor.

| Concern                 | Lumen choice                                    | Inspired by      |
|-------------------------|-------------------------------------------------|------------------|
| Memory safety           | Affine types + borrow checker, **no GC**        | Rust             |
| Performance             | AOT to native via LLVM, zero-cost abstractions  | C, C++, Rust     |
| Compile speed           | Small core, fast frontend, incremental codegen  | Go               |
| Concurrency             | Lightweight tasks + typed channels + `select`   | Go               |
| Error handling          | `Result<T,E>` / `Option<T>`, no nulls, no panics in API surface | Rust |
| Generics                | Bounded parametric polymorphism with traits     | Rust + Go        |
| Metaprogramming         | Hygienic `comptime` evaluation                  | Zig              |
| Effects                 | Algebraic effect / capability system            | Koka, Eff        |
| Math types              | Refinement types (`{x: Int | x > 0}`)           | Liquid Haskell   |
| Interop                 | First-class C ABI                               | C, Rust, Zig     |
| Tooling                 | One binary: `lumen` (build, run, fmt, test, repl, doc) | Go, Cargo |

### Guiding axioms

1. **Correctness is composable.** Types, lifetimes, and effects are tracked
   together; well-typed programs cannot leak, race, or null-deref.
2. **Cost is visible.** Allocations, copies, and async suspensions are
   syntactically apparent or statically prevented.
3. **The fast path is the safe path.** Idiomatic Lumen is also the most
   efficient Lumen.
4. **No keyword salad.** ~30 reserved words. Every keyword earns its place.

---

## 2. Lexical Structure

### 2.1 Source encoding
UTF-8. Identifiers follow UAX #31 (`XID_Start` + `XID_Continue`).

### 2.2 Reserved keywords
```
fn  let  mut  const  comptime  return  if  else  match  while  for  in
break  continue  struct  enum  trait  impl  type  use  pub  mod
spawn  chan  select  defer  as  where  Self  self  true  false
```

### 2.3 Operators & punctuation
```
+ - * / %      arithmetic
== != < <= > >=  comparison
&& || !        logical
& | ^ ~ << >>  bitwise
= += -= *= /= %= &= |= ^= <<= >>=  assignment
-> => :: . , ; : ( ) { } [ ] ? @ # _
&  &mut        borrows
```

### 2.4 Literals
- **Integer:** `42`, `0xFF`, `0b1010`, `0o777`, `1_000_000`, `42i64`, `255u8`
- **Float:** `3.14`, `1e9`, `2.5f32`
- **Bool:** `true`, `false`
- **Char:** `'a'`, `'\n'`, `'\u{1F600}'`
- **String:** `"hello"`, raw: `r"C:\path"`, interpolated: `` `x = ${x}` ``
- **Unit:** `()`

### 2.5 Comments
```
// line
/* block /* nested */ */
/// doc on item
//! doc on enclosing module
```

---

## 3. Types

### 3.1 Primitive types
```
i8 i16 i32 i64 i128 isize
u8 u16 u32 u64 u128 usize
f32 f64
bool char str ()
```

`str` is a UTF-8 string slice; owned strings are `String`.

### 3.2 Compound types
```
[T; N]          // fixed-size array
[T]             // slice (fat pointer)
(T1, T2, ...)   // tuple
&T   &mut T     // shared / unique borrow
*const T *mut T // raw pointers (unsafe)
fn(T) -> U      // function pointer
Box<T>          // unique heap pointer
Rc<T>  Arc<T>   // reference-counted (single/multi-thread)
chan<T>         // typed channel
```

### 3.3 User-defined types

**Struct:**
```lumen
struct Point { x: f64, y: f64 }
struct Wrapper(i32, i32)             // tuple struct
struct Unit                          // unit struct
```

**Enum (sum / ADT):**
```lumen
enum Shape {
    Circle { radius: f64 },
    Rect(f64, f64),
    Triangle,
}
```

**Type alias:**
```lumen
type Vec2 = (f64, f64)
```

### 3.4 Generics & traits

```lumen
trait Eq { fn eq(self: &Self, other: &Self) -> bool }

trait Ord: Eq {
    fn cmp(self: &Self, other: &Self) -> Ordering
}

fn max<T: Ord>(a: T, b: T) -> T {
    if a.cmp(&b) == Ordering::Greater { a } else { b }
}

impl Eq for i32 {
    fn eq(self: &i32, other: &i32) -> bool { *self == *other }
}
```

Trait bounds support `where` clauses, associated types, and default methods —
identical surface to Rust, but **no orphan rule for sealed modules** (see §10).

### 3.5 Refinement types *(planned, v0.3)*
```lumen
type Nat = {x: i64 | x >= 0}
type NonEmpty<T> = {xs: [T] | xs.len > 0}

fn head<T>(xs: NonEmpty<T>) -> T { xs[0] }   // no bounds check needed
```
Refinements are discharged at compile time by an SMT solver
(planned: Z3 binding). Failures degrade gracefully to a runtime check
behind `#[refine(runtime)]`.

---

## 4. Memory Model & Borrow Checker

### 4.1 Ownership
Every value has exactly one owner. Assignment **moves** by default.
`Copy` types (primitives, `&T`, small `#[copy]` structs) duplicate instead.

### 4.2 Borrows
- `&T` — shared, immutable, aliasable.
- `&mut T` — unique, mutable, non-aliasable.
- A reference's lifetime is inferred and must not exceed the referent's.
- The classic Rust rule applies: **at any point, either one `&mut` xor
  any number of `&`**.

### 4.3 Lifetimes
Implicit and inferred wherever possible. Explicit form: `&'a T`.
Higher-ranked types: `for<'a> fn(&'a T) -> &'a U`.

### 4.4 Drop & `defer`
```lumen
fn read_file(path: &str) -> Result<String, IoError> {
    let f = File::open(path)?;
    defer f.close();             // runs on scope exit (success or unwind)
    f.read_to_string()
}
```
Destructors run in reverse declaration order. `defer` is sugar for an
anonymous RAII guard.

### 4.5 No null
The only way to express absence is `Option<T>`. Raw pointers exist but
require `unsafe` to deref.

---

## 5. Expressions & Statements

Everything that can be is an **expression** (Rust-style). Blocks evaluate
to their final expression. Semicolons turn an expression into a statement
yielding `()`.

```lumen
let x = if cond { 1 } else { 2 }
let y = match opt {
    Some(v) => v * 2,
    None    => 0,
}
```

### 5.1 Pattern matching
Exhaustiveness is checked by the compiler.
```lumen
match shape {
    Shape::Circle { radius } if radius > 0.0 => area_circle(radius),
    Shape::Rect(w, h)                        => w * h,
    Shape::Triangle                          => panic!("todo"),
}
```
Patterns: literals, identifiers, `_`, structs, enums, tuples, slices
(`[head, ..tail]`), ranges (`1..=10`), or-patterns (`A | B`), bindings (`x @ 1..=5`).

### 5.2 Control flow
`if`, `else`, `while`, `for x in iter`, `loop`, `break`, `continue`,
`return`, `match`. No C-style `for(;;)`; iteration is via iterators.

---

## 6. Functions

```lumen
fn add(x: i32, y: i32) -> i32 { x + y }

fn apply<T, U>(f: fn(T) -> U, x: T) -> U { f(x) }

fn divmod(a: i64, b: i64) -> (i64, i64) where b != 0 { (a / b, a % b) }
```

- Last expression is the return value.
- `?` propagates `Result`/`Option`.
- Closures: `|x| x + 1`, `move |x| x + captured`.
- Methods: defined in `impl` blocks; first parameter is `self`, `&self`, or `&mut self`.

---

## 7. Concurrency

Lumen unifies Go's ergonomics with Rust's safety.

### 7.1 Tasks
```lumen
spawn { do_work() }                       // fire-and-forget lightweight task
let h = spawn { compute() }               // returns Task<T>
let result = h.await
```
Tasks are M:N scheduled on a work-stealing runtime. The compiler
guarantees `Send`/`Sync` at the boundary; data races are a compile error.

### 7.2 Channels
```lumen
let (tx, rx) = chan::<i32>(buffered = 16)
spawn { for i in 0..10 { tx.send(i) } }
for v in rx { print(v) }
```

### 7.3 `select`
```lumen
select {
    v = rx1.recv() => handle(v),
    _ = timer(1.s) => print("timeout"),
    tx.send(x)     => print("sent"),
}
```

### 7.4 Structured concurrency
A `scope` block joins all child tasks before returning, and cancels
them on error. Inspired by Trio / Kotlin.
```lumen
scope |s| {
    s.spawn { fetch(a) }
    s.spawn { fetch(b) }
} // both joined here
```

---

## 8. Compile-time evaluation (`comptime`)

```lumen
comptime const TABLE: [u32; 256] = build_crc_table()

fn build_crc_table() -> [u32; 256] { /* ... */ }

fn print_at<comptime N: usize>(arr: &[i32; N]) {
    comptime for i in 0..N { print(arr[i]) }   // fully unrolled
}
```
- `comptime` functions may execute any pure subset of the language.
- Types are first-class values inside `comptime`.
- Generates monomorphized, statically-sized code.

---

## 9. Effect system *(planned, v0.4)*

Functions declare the **capabilities** they require:
```lumen
fn read_config(fs: &cap FileSystem) -> !io Config { ... }
fn pure_add(a: i64, b: i64) -> i64 { a + b }     // no effects
```
- `!io`, `!alloc`, `!async`, `!unwind` are built-in effect rows.
- Capabilities are ordinary values: passing one grants the effect.
- Pure functions are pure *by default*; you must opt in to side-effects.

---

## 10. Modules & Visibility

```
src/
  main.lm
  net/
    mod.lm
    http.lm
```
- `mod net` declares a submodule.
- `use net::http::Client`.
- `pub` exports; `pub(mod)` exports within the current module tree.
- **Sealed modules** (`mod sealed net { ... }`) restrict trait impls to
  inside the module — a stronger alternative to Rust's orphan rule.

---

## 11. C ABI Interop

```lumen
extern "C" {
    fn write(fd: i32, buf: *const u8, n: usize) -> isize
}

#[export("lumen_add")]
extern "C" fn add(a: i32, b: i32) -> i32 { a + b }
```
- `extern "C"` blocks declare foreign functions.
- `#[repr(C)]` on structs guarantees C layout.
- The build system generates a `.h` automatically from `pub extern "C"` items.

---

## 12. Standard Library Sketch (v0.1)

| Module      | Highlights                                                   |
|-------------|--------------------------------------------------------------|
| `core`      | `Option`, `Result`, `Ordering`, traits (`Eq`, `Ord`, `Iter`) |
| `mem`       | `Box`, `Rc`, `Arc`, `Cell`, `swap`, `replace`                |
| `vec`       | `Vec<T>` growable array                                      |
| `str`       | UTF-8 strings and slices                                     |
| `io`        | `print`, `println`, files, sockets (effect-tracked)          |
| `sync`      | `Mutex`, `RwLock`, atomics                                   |
| `async`     | `Task`, `chan`, `select`, `scope`                            |
| `math`      | refinement-aware numeric kernels                             |
| `ffi`       | C ABI helpers                                                |

---

## 13. Operational Semantics (sketch)

Lumen has a small-step structural operational semantics defined over a
core calculus **λ-Lumen**:

$$
\frac{e_1 \to e_1'}{e_1 \; e_2 \to e_1' \; e_2}
\qquad
\frac{}{(\lambda x.\, e)\; v \to e[v/x]}
$$

The surface language elaborates to λ-Lumen, which is typed via a bidirectional
algorithm with affine ownership annotations:

$$
\Gamma \vdash e : \tau \;\dashv\; \Delta
$$

where $\Gamma$ is the input context and $\Delta$ is the context after
ownership is consumed. The borrow checker is a separate pass over a CFG
of the typed term, computing per-edge **lifetime sets** with a unification-
based solver (see `internal/borrow/`).

A full formalization will land in `docs/FORMAL.md`.

---

## 14. Implementation Roadmap

| Phase | Milestone                                                       | Status |
|-------|-----------------------------------------------------------------|--------|
| 0.1   | Lexer, parser, AST, tree-walking interpreter, prelude, REPL     | **in progress** |
| 0.2   | Hindley-Milner type inference, trait resolution, monomorphization | planned |
| 0.3   | Borrow checker, lifetime inference, `Drop`/`defer` codegen      | planned |
| 0.4   | Effect system, capability passing                               | planned |
| 0.5   | LLVM backend via `llir/llvm` (Go binding)                       | planned |
| 0.6   | Concurrency runtime (M:N scheduler), channels, `select`         | planned |
| 0.7   | Refinement types + SMT discharge                                | planned |
| 0.8   | Self-hosting subset                                             | aspirational |

---

## 15. Hello world

```lumen
fn main() {
    println("Hello, Lumen!")
}
```

```lumen
// Fibonacci with pattern matching
fn fib(n: u32) -> u64 {
    match n {
        0 => 0,
        1 => 1,
        _ => fib(n - 1) + fib(n - 2),
    }
}
```
