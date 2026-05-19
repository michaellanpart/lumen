# Array + Sorting Enablement Audit (v0.7 typed-core)

## Goal
Enable a fair in-language Lumen sorting benchmark by adding typed-core array support (types, literals, indexing, assignment, lowering, and builtins).

## Current State

### 1) AST + parser: mostly present
- AST already models array and index syntax:
  - `ArrayType`, `ArrayLit`, `IndexExpr` in `internal/ast/ast.go`.
- Parser already parses array type syntax (`[N]T` and fallback forms) in `internal/parser/parser.go`.

### 2) Type checker: missing typed-core support
- `resolveType` currently supports named/ref types only in typed-core mode.
  - No `*ast.ArrayType` handling in `internal/types/check.go`.
- Expression typing lacks:
  - `*ast.ArrayLit`
  - `*ast.IndexExpr`
- Assignment typing currently only permits variable and field lvalues.
  - Index lvalue assignment is rejected in typed-core mode.
- Builtin typing lacks array-aware `len` in typed-core mode.

### 3) C backend: no array lowering path yet
- `cTypeOf` has no array kind support.
- Expression lowering has no `ArrayLit` / `IndexExpr` lowering path.
- Assignment lowering can emit generic `l = r`, but type-checker currently blocks index lvalues.

### 4) Borrow checker: traversal support exists
- Borrow checker AST walk already traverses `ArrayLit` and `IndexExpr`.
- Builtin signature handling will need `len` behavior aligned once checker supports arrays.

### 5) Interpreter: supports arrays (reference only)
- Interpreter already supports arrays/indexing and `len` over arrays.
- This confirms language-level behavior target; typed-core + C backend is the missing path.

## Minimal implementation sequence
1. Add array type kind to typed checker type model.
2. Add `resolveType` support for `*ast.ArrayType`.
3. Add expression typing for `*ast.ArrayLit` and `*ast.IndexExpr`.
4. Allow index lvalues in assignment checks.
5. Add typed builtin `len` support for arrays.
6. Add C backend lowering for array values and indexing.
7. Add compiler tests for array typing/index reads/index writes.
8. Add Lumen sort benchmark and re-enable Lumen in sort suite.

## Definition of done for benchmark fairness
- Lumen has an in-language sort workload equivalent to C/Rust/Go deterministic integer sort.
- `benchmarks/run_fair_suite.sh` includes Lumen in sort results.
- Fair report no longer carries the sort-exclusion note.
