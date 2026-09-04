# One definition of "verified", used by both a developer and CI. If they can drift, they will.

GO_FILES := $(shell find . -name '*.go' -not -path './ui/*')

.PHONY: verify fmt vet test lint ql js ui selfcheck check-corrupt build clean

verify: check-corrupt fmt vet lint test js ql ui

## check-corrupt: catch a duplicated package clause.
##
## This environment intermittently prepends a second `package` line when writing a Go file.
## gofmt reports it only as a parse error, which reads like a syntax mistake in the code rather
## than a corrupted write, so it is checked explicitly and first.
check-corrupt:
	@fail=0; \
	for f in $(GO_FILES); do \
		n=$$(grep -c '^package ' "$$f"); \
		if [ "$$n" -gt 1 ]; then echo "CORRUPT: $$f has $$n package clauses"; fail=1; fi; \
	done; \
	if [ $$fail -ne 0 ]; then exit 1; fi; \
	echo "corruption: none"

fmt:
	@out=$$(gofmt -l . 2>&1 | grep -v '^ui/' || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

vet:
	@go vet ./... && echo "vet: ok"

# Part of `verify`, because CI runs it: a check that only runs in CI is one that fails after the
# push rather than before it. A missing tool is reported as missing, not silently skipped.
lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint is not installed: brew install golangci-lint"; exit 1; \
	fi
	@golangci-lint run ./... && echo "lint: clean"

test:
	@go test ./... -count=1 -race

## js: the browser code is a third of the interface's behavior and none of it is typechecked.
js:
	@node --test ui/wwwroot/js/*.test.js && echo "js: ok"

## ui: a Razor syntax error passes every Go check and fails in CI.
ui:
	@dotnet build ui/Abom.WebAssembly.csproj -c Release --nologo -v quiet && echo "ui: ok"

ql:
	@codeql test run ql/actions/abom/test

## selfcheck: run our own analysis over our own workflows, through our own database-creation
## path. Not in `verify`: it needs the CodeQL CLI and takes tens of seconds.
selfcheck:
	@go run ./cmd/codeql-abom selfcheck \
		--suite ql/actions/abom/src/codeql-suites/abom-selfcheck.qls

build:
	@go build -o bin/codeql-abom ./cmd/codeql-abom

clean:
	@rm -rf bin
