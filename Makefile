.PHONY: build build-lsp test run repl clean fmt install-lsp

build:
	go build -o bin/lumen ./cmd/lumen

build-lsp:
	go build -o bin/lumen-lsp ./cmd/lumen-lsp

install-lsp: build-lsp
	cp bin/lumen-lsp /usr/local/bin/lumen-lsp
	@echo "lumen-lsp installed to /usr/local/bin/lumen-lsp"

test:
	go test ./...

run: build
	./bin/lumen run examples/hello.lm
	./bin/lumen run examples/fib.lm
	./bin/lumen run examples/shapes.lm
	./bin/lumen run examples/closures.lm

repl: build
	./bin/lumen repl

fmt:
	go fmt ./...

clean:
	rm -rf bin
