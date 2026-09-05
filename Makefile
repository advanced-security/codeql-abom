# One definition of "verified", used by both a developer and CI. If they can drift, they will.
#
# Targets are in alphabetical order. Grouping them by theme meant every addition needed a
# judgement about where it belonged, and the answer drifted.

ABOM_ADDR ?= 127.0.0.1:8099
ABOM_DB   ?= /tmp/abom-real/abom.db
ABOM_LOG  ?= /tmp/abom-serve.log
ABOM_PID  ?= /tmp/abom-serve.pid
ABOM_UI   ?= /tmp/abom-ui
ABOM_WORK ?= /tmp/abom-work

# Extra flags for the server, so the common case needs no editing here. Discovery does not start
# on its own: `make start ABOM_SERVE_FLAGS=--expand` opts in.
ABOM_SERVE_FLAGS ?=

BIN      := bin/codeql-abom
GO_FILES := $(shell find . -name '*.go' -not -path './ui/*')

.PHONY: build check-corrupt clean fmt js lint logs package publish ql restart selfcheck \
	serve start status stop style test ui ui-test verify vet

verify: check-corrupt fmt vet lint test js style ql ui ui-test

build:
	@go build -o $(BIN) ./cmd/codeql-abom

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

clean:
	@rm -rf bin dist

fmt:
	@out=$$(gofmt -l . 2>&1 | grep -v '^ui/' || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

## js: the browser code is a third of the interface's behavior and none of it is typechecked.
js:
	@node --test ui/wwwroot/js/*.test.js && echo "js: ok"

# Part of `verify`, because CI runs it: a check that only runs in CI is one that fails after the
# push rather than before it. A missing tool is reported as missing, not silently skipped.
lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint is not installed: brew install golangci-lint"; exit 1; \
	fi
	@golangci-lint run ./... && echo "lint: clean"

## logs: follow the running server. Discovery writes one line per cycle and one per failure.
logs:
	@if [ ! -f "$(ABOM_LOG)" ]; then echo "no log at $(ABOM_LOG); make start first"; exit 1; fi
	@tail -n 40 -f "$(ABOM_LOG)"

## package: a self-contained drop of the binary and the interface it serves.
package: build publish
	@rm -rf dist && mkdir -p dist
	@cp $(BIN) dist/
	@cp -R "$(ABOM_UI)/wwwroot" dist/ui
	@echo "package: dist/codeql-abom + dist/ui"

## publish: build the interface into $(ABOM_UI). Separate from `ui`, which only typechecks it.
publish:
	@rm -rf "$(ABOM_UI)"
	@dotnet publish ui/Abom.WebAssembly.csproj -c Release -o "$(ABOM_UI)" --nologo -v quiet
	@echo "publish: $(ABOM_UI)/wwwroot"

ql:
	@codeql test run ql/actions/abom/test

restart: stop start

## selfcheck: run our own analysis over our own workflows, through our own database-creation
## path. Not in `verify`: it needs the CodeQL CLI and takes tens of seconds.
selfcheck:
	@go run ./cmd/codeql-abom selfcheck \
		--suite ql/actions/abom/src/codeql-suites/abom-selfcheck.qls

## serve: run in the foreground, for watching it work. `make start` for the background.
serve: build publish
	@mkdir -p "$(ABOM_WORK)"
	@$(BIN) serve --db "$(ABOM_DB)" --addr "$(ABOM_ADDR)" \
		--ui-dir "$(ABOM_UI)/wwwroot" --work-dir "$(ABOM_WORK)" $(ABOM_SERVE_FLAGS)

## start: serve in the background, rebuilding both halves first.
##
## Both halves every time, deliberately: serving a stale interface against a new binary looks
## like a bug in the interface, and costs far more to work out than the rebuild does.
start: build publish
	@if [ -f "$(ABOM_PID)" ] && kill -0 "$$(cat $(ABOM_PID))" 2>/dev/null; then \
		echo "start: already serving as pid $$(cat $(ABOM_PID)); use 'make restart'"; exit 1; \
	fi
	@mkdir -p "$(ABOM_WORK)"
	@$(BIN) serve --db "$(ABOM_DB)" --addr "$(ABOM_ADDR)" \
		--ui-dir "$(ABOM_UI)/wwwroot" --work-dir "$(ABOM_WORK)" $(ABOM_SERVE_FLAGS) \
		>> "$(ABOM_LOG)" 2>&1 & echo $$! > "$(ABOM_PID)"
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if curl -fsS -o /dev/null "http://$(ABOM_ADDR)/api/summary" 2>/dev/null; then \
			echo "start: http://$(ABOM_ADDR) (pid $$(cat $(ABOM_PID)), log $(ABOM_LOG))"; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "start: no answer from http://$(ABOM_ADDR) within 10s; see $(ABOM_LOG)"; \
	tail -n 20 "$(ABOM_LOG)"; exit 1

status:
	@if [ -f "$(ABOM_PID)" ] && kill -0 "$$(cat $(ABOM_PID))" 2>/dev/null; then \
		echo "status: serving http://$(ABOM_ADDR) (pid $$(cat $(ABOM_PID)))"; \
		curl -fsS "http://$(ABOM_ADDR)/api/discovery/status" && echo; \
	else \
		echo "status: not running"; \
	fi

stop:
	@if [ ! -f "$(ABOM_PID)" ]; then echo "stop: not running"; exit 0; fi; \
	pid=$$(cat "$(ABOM_PID)"); \
	if kill -0 "$$pid" 2>/dev/null; then \
		kill "$$pid"; echo "stop: stopped pid $$pid"; \
	else \
		echo "stop: not running"; \
	fi; \
	rm -f "$(ABOM_PID)"

## style: one visual vocabulary. Drift here is invisible to every other check, and review had
## already missed six font sizes, three heading shapes and forty-nine inline styles.
style:
	@node ui/tools/check-style.mjs

test:
	@go test ./... -count=1 -race

## ui: a Razor syntax error passes every Go check and fails in CI.
ui:
	@dotnet build ui/Abom.WebAssembly.csproj -c Release --nologo -v quiet && echo "ui: ok"

## ui-test: component behavior. `make ui` only compiles Razor, so every interface regression
## this project has shipped — a filter answering before it asked, controls enabled with nothing
## to control — passed every other check. bUnit has no CSS engine, so layout is still the
## screenshot pass's job.
ui-test:
	@dotnet test ui/tests/Abom.WebAssembly.Tests/Abom.WebAssembly.Tests.csproj \
		--nologo -v quiet && echo "ui-test: ok"

vet:
	@go vet ./... && echo "vet: ok"
