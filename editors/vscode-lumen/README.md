# Lumen for Visual Studio Code

Editor support for the [Lumen](https://github.com/lumen-lang/lumen) programming
language (`.lm` files).

## Features

- **Syntax highlighting** — TextMate grammar covering keywords, types, literals,
  function/method declarations, operators (`:=`, `::`, `->`, `=>`, `..`, `..=`),
  enum paths (`Foo::Bar`), attributes (`#[...]`), and triple-slash doc comments.
- **Language configuration** — bracket matching, auto-closing pairs, smart
  indentation, line/block comments, and comment continuation on Enter.
- **Snippets** — common scaffolds: `func`, `funci` (inferred return),
  `method`, `methodp` (pointer receiver), `struct`, `enum`, `impl`, `main`,
  `if`, `ifel`, `for`, `forc`, `loop`, `switch`, `let`, `var`, `ret`, `new`,
  `pln`.

## Install (from source)

```sh
cd editors/vscode-lumen
npm install -g @vscode/vsce      # one-time
vsce package                     # produces vscode-lumen-0.1.0.vsix
code --install-extension vscode-lumen-0.1.0.vsix
```

Or for quick local iteration: copy the `editors/vscode-lumen/` directory to
`~/.vscode/extensions/lumen-lang.vscode-lumen-0.1.0/` and reload VS Code.

## Roadmap

This extension is currently a TextMate-grammar + snippets package. A future
revision will add a real Language Server (powered by the existing `cmd/lumen`
toolchain) for:

- on-the-fly diagnostics from `parser` + `types.Check` + `borrowck`
- hover types from `internal/types`
- go-to-definition for free functions, methods, and type declarations
- completion driven by the type checker's `Info` tables

## License

MIT
