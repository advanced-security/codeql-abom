# One definition of "verified", used by both a developer and CI. If they can drift, they will.
#
# Targets are in alphabetical order. Grouping them by theme meant every addition needed a
# judgement about where it belonged, and the answer drifted.

# Everything this repository writes at runtime lives under one directory inside the checkout,
# gitignored, rather than in the OS temp directory. /tmp is world-writable and shared with every
# other process on the machine, and on Linux it is cleared on reboot -- which would have taken the
# working corpus with it, since that is what ABOM_DB names.
#
# Not ".abom-tmp": only `work` and `gotest` below are actually temporary. The corpus under `db`
# is the expensive thing here, and a directory whose name says "tmp" is one somebody deletes.
ABOM_TMP  ?= $(CURDIR)/.abom

ABOM_ADDR ?= 127.0.0.1:8099
ABOM_DB   ?= $(ABOM_TMP)/db/abom.db
ABOM_LOG  ?= $(ABOM_TMP)/logs/serve.log
ABOM_PID  ?= $(ABOM_TMP)/serve.pid
ABOM_UI   ?= $(ABOM_TMP)/ui
ABOM_WORK ?= $(ABOM_TMP)/work

# Extra flags for the server, so the common case needs no editing here. Discovery does not start
# on its own: `make start ABOM_SERVE_FLAGS=--expand` opts in.
ABOM_SERVE_FLAGS ?=

BIN      := bin/codeql-abom
GO_FILES := $(shell find . -name '*.go' -not -path './ui/*')

.PHONY: bench build check-corrupt corpus check-versions clean e2e fmt itest js lint logs package publish ql \
	quality restart selfcheck serve start status stop style test ui ui-test verify vet

.DEFAULT_GOAL := build

verify: check-corrupt check-versions fmt vet lint test js style ql ui ui-test e2e

## bench: time the store read paths against a real corpus.
##
## No -race, deliberately: the detector costs 2x to 20x, so a benchmark under it measures the
## detector. `make test` keeps -race, which is where it belongs.
##
## ABOM_BENCH_DB must name a snapshot, not the live database. Without it every benchmark skips
## rather than measuring an empty store, because a fast nothing is how a CLI timing ranked work
## first that the served path was never paying for.
ABOM_BENCH_DB ?= $(ABOM_TMP)/bench.db
bench:
	@if [ ! -f "$(ABOM_BENCH_DB)" ]; then \
		echo "bench: no corpus at $(ABOM_BENCH_DB); take one with"; \
		echo "  sqlite3 -readonly \"file:$(ABOM_DB)?immutable=1\" \".backup $(ABOM_BENCH_DB)\""; \
	fi
	@ABOM_BENCH_DB="$(ABOM_BENCH_DB)" go test ./internal/... -run '^$$' -bench . -benchmem

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

## corpus: build a populated database from the recorded fixtures under testdata/corpus.
##
## Offline and deterministic: the CodeQL half is recorded, not re-run, so this needs no CLI, no
## network and no token. Regenerating what it reads is the separate, slower step in
## scripts/regenerate-corpus-fixtures.sh, which does need the CLI.
##
## Refuses ABOM_DB for the reason `e2e` does — a builder that replaces its output must not be
## pointed at the working corpus.
ABOM_CORPUS_DB ?= $(ABOM_TMP)/corpus/abom.db
corpus:
	@if [ "$(abspath $(ABOM_CORPUS_DB))" = "$(abspath $(ABOM_DB))" ]; then \
		echo "corpus: refusing to overwrite $(ABOM_DB), which is the working database."; exit 1; \
	fi
	@go run ./cmd/abom-corpus --db "$(ABOM_CORPUS_DB)"

## e2e: drive the published interface in a real browser.
##
## Outside `verify` for the same reason `itest` and `selfcheck` are: it needs tools the repository
## does not carry. This one needs npm and a downloaded Chromium, which is the first npm dependency
## in a tool whose subject is supply-chain risk, so it is opt-in until that trade is settled.
##
## It owns geometry. `ui-test` has no CSS engine, so a grid that overflows and a label that wraps
## onto its own row are green everywhere else.
##
## Runs against a database it creates and throws away. `make corpus` builds a seeded one from
## recorded CodeQL output, and pointing this at it reaches the assertions an empty database cannot
## show — 5 of them fail there today, because the fixture is 3 repositories and carries no
## advisory. Growing it until that run is green is what `plans/ui/testing.md` owes:
##
##   make corpus && make e2e ABOM_E2E_DB=.abom/corpus/abom.db
##   make e2e ABOM_E2E_FLAGS="--headed --project=chromium"
ABOM_E2E_DIR   ?= $(ABOM_TMP)/e2e
ABOM_E2E_DB    ?= $(ABOM_E2E_DIR)/abom.db
ABOM_E2E_PORT  ?= 8231
ABOM_E2E_FLAGS ?=
# Which browser runs the suite. Empty takes the pinned Chromium, which is what CI should use
# because it is identical everywhere. `chrome` borrows the system install, and is the way through
# on a machine where that download will not complete — a partial one leaves a directory that looks
# installed and an app bundle with no framework inside, which fails as a launch error rather than
# as a missing browser.
ABOM_E2E_CHANNEL ?=
e2e: build publish
	@if [ "$(abspath $(ABOM_E2E_DB))" = "$(abspath $(ABOM_DB))" ]; then \
		echo "e2e: refusing to run against $(ABOM_DB), which is the working database."; \
		echo "e2e: take a copy first, and note that cp does not read the WAL:"; \
		echo "  sqlite3 \"$(ABOM_DB)\" \".backup $(ABOM_TMP)/copy.db\""; exit 1; \
	fi
	@if [ ! -d ui/tests/e2e/node_modules ]; then \
		echo "e2e: installing the harness into ui/tests/e2e"; \
		(cd ui/tests/e2e && npm ci --no-fund --no-audit) || exit 1; \
	fi
	@if [ "$(ABOM_E2E_DB)" = "$(ABOM_E2E_DIR)/abom.db" ]; then rm -rf "$(ABOM_E2E_DIR)"; fi
	@mkdir -p "$(ABOM_E2E_DIR)"
	@cd ui/tests/e2e && \
		ABOM_E2E_BIN="$(CURDIR)/$(BIN)" \
		ABOM_E2E_DB="$(ABOM_E2E_DB)" \
		ABOM_E2E_UI="$(ABOM_UI)/wwwroot" \
		ABOM_E2E_PORT="$(ABOM_E2E_PORT)" \
		ABOM_E2E_CHANNEL="$(ABOM_E2E_CHANNEL)" \
		npx playwright test $(ABOM_E2E_FLAGS)

fmt:
	@out=$$(gofmt -l . 2>&1 | grep -v '^ui/' || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

## js: the browser code is a third of the interface's behavior and none of it is typechecked.
js:
	@node --test ui/wwwroot/js/*.test.js ui/tests/e2e/*.test.mjs && echo "js: ok"

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
	@codeql test run ql/actions/graph-extraction/test ql/actions/findings/test

restart: stop start

## itest: exercise the CodeQL protocols that only a real server can answer for. Not in `verify`:
## it builds a database and takes tens of seconds. The query server is the CLI's IDE-facing
## plumbing, so its message shapes are not a stable interface and drift silently. Serialized with
## -p 1: each package shells out to CodeQL, and every invocation shares one compilation cache.
ABOM_ITEST_DB ?= $(ABOM_TMP)/itest-db
## A second database, from a tree with a root action.yml: the sparse-filter check cannot fail
## against a source that has nothing outside .github/, which is the shape it exists to catch.
ABOM_ITEST_SPARSE_DB ?= $(ABOM_TMP)/itest-sparse-db
## A third, built with --overlay-base: the CLI accepts that flag for actions and exits 0 while
## writing overlayBaseDatabase: false, so the blocker is asserted rather than remembered.
ABOM_ITEST_OVERLAY_DB ?= $(ABOM_TMP)/itest-overlay-db
itest:
	@if ! command -v codeql >/dev/null 2>&1; then \
		echo "the CodeQL CLI is not installed, and every test here shells out to it"; exit 1; \
	fi
	@mkdir -p "$(ABOM_TMP)/gotest"
	@if [ ! -d "$(ABOM_ITEST_DB)" ]; then \
		codeql database create "$(ABOM_ITEST_DB)" --language=actions \
			--source-root=ql/actions/graph-extraction/test/AbomEdges; \
	fi
	@if [ ! -d "$(ABOM_ITEST_SPARSE_DB)" ]; then \
		codeql database create "$(ABOM_ITEST_SPARSE_DB)" --language=actions \
			--source-root=internal/expand/testdata/sparse; \
	fi
	@if [ ! -d "$(ABOM_ITEST_OVERLAY_DB)" ]; then \
		codeql database create "$(ABOM_ITEST_OVERLAY_DB)" --language=actions --overlay-base \
			--source-root=internal/expand/testdata/sparse; \
	fi
	@ABOM_ITEST_DB="$(ABOM_ITEST_DB)" ABOM_ITEST_SPARSE_DB="$(ABOM_ITEST_SPARSE_DB)" \
		ABOM_ITEST_OVERLAY_DB="$(ABOM_ITEST_OVERLAY_DB)" TMPDIR="$(ABOM_TMP)/gotest" \
		go test -p 1 ./internal/runner/ ./internal/pipeline/ ./internal/expand/ -count=1
	@echo "itest: ok"

## quality: analyze our own source with our own tool, in every language we write.
## Separate from `verify` on purpose: `verify` answers "is this change correct" on every edit,
## this answers "what does our own analyzer say about this codebase" and builds a database per
## language to do it. Fails above zero findings, in every language; `self-audit.yml` runs the
## same set as a matrix. Pass --max-findings=-1 to take a measurement without failing.
##
## The JavaScript root is the repository, not `ui/wwwroot`. Two reasons, and the second is the
## one that bites: `ui/tools` holds JavaScript too, and `paths-ignore` in the Code Scanning config
## is resolved against whatever root is passed -- so `ui/bin` excludes nothing unless the root is
## the one Default Setup reads. A narrower root both misses files and silently loses the
## exclusions that keep this answering the same question Default Setup answers.
quality: selfcheck
	@go run ./cmd/codeql-abom selfcheck --language=go \
		--suite advanced-security/abom-go-quality-src:codeql-suites/abom-go-quality.qls
	@go run ./cmd/codeql-abom selfcheck --language=csharp --source-root=ui \
		--suite advanced-security/abom-csharp-quality-src:codeql-suites/abom-csharp-quality.qls
	@go run ./cmd/codeql-abom selfcheck --language=javascript \
		--suite advanced-security/abom-javascript-quality-src:codeql-suites/abom-javascript-quality.qls
	@go run ./cmd/codeql-abom selfcheck --language=python --source-root=scripts \
		--suite advanced-security/abom-python-quality-src:codeql-suites/abom-python-quality.qls

## selfcheck: run our own analysis over our own workflows, through our own database-creation
## path. Not in `verify`: it needs the CodeQL CLI and takes tens of seconds.
selfcheck:
	@go run ./cmd/codeql-abom selfcheck \
		--suite advanced-security/abom-actions-selfcheck-src:codeql-suites/abom-selfcheck.qls

## serve: run in the foreground, for watching it work. `make start` for the background.
##
## The database and log directories are made here because nothing else does: store.Open does not
## create its parent, and a redirect into a missing directory fails before the server ever runs.
serve: build publish
	@mkdir -p "$(ABOM_WORK)" "$(dir $(ABOM_DB))" "$(dir $(ABOM_LOG))"
	@$(BIN) serve --db "$(ABOM_DB)" --addr "$(ABOM_ADDR)" \
		--ui-dir "$(ABOM_UI)/wwwroot" --work-dir "$(ABOM_WORK)" $(ABOM_SERVE_FLAGS)

## start: serve in the background, rebuilding both halves first.
##
## Both halves every time, deliberately: serving a stale interface against a new binary looks
## like a bug in the interface, and costs far more to work out than the rebuild does.
start: build publish
	@# One dev server per machine, found by process rather than by address: the pid file only
	@# knows what this Makefile started, and a second server on another port is still a second
	@# writer against the database. Matching on the executable name keeps this recipe's own
	@# shell out of the results.
	@running=$$(for pid in $$(pgrep -x $(notdir $(BIN)) 2>/dev/null); do \
		case "$$(ps -o args= -p $$pid 2>/dev/null)" in (*" serve"*) echo $$pid;; esac; \
	done | tr '\n' ' '); \
	if [ -n "$$running" ]; then \
		echo "start: a server is already running, and only one is allowed:"; \
		ps -o pid=,args= -p $$running; \
		echo "start: use 'make restart', or 'make stop' first"; exit 1; \
	fi
	@mkdir -p "$(ABOM_WORK)" "$(dir $(ABOM_DB))" "$(dir $(ABOM_LOG))"
	@$(BIN) serve --db "$(ABOM_DB)" --addr "$(ABOM_ADDR)" \
		--ui-dir "$(ABOM_UI)/wwwroot" --work-dir "$(ABOM_WORK)" $(ABOM_SERVE_FLAGS) \
		>> "$(ABOM_LOG)" 2>&1 & echo $$! > "$(ABOM_PID)"
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		if ! kill -0 "$$(cat $(ABOM_PID))" 2>/dev/null; then \
			echo "start: the server exited during startup; see $(ABOM_LOG)"; \
			tail -n 20 "$(ABOM_LOG)"; rm -f "$(ABOM_PID)"; exit 1; \
		fi; \
		if curl -fsS -o /dev/null "http://$(ABOM_ADDR)/api/summary" 2>/dev/null; then \
			echo "start: http://$(ABOM_ADDR) (pid $$(cat $(ABOM_PID)), log $(ABOM_LOG))"; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "start: no answer from http://$(ABOM_ADDR) within 10s; see $(ABOM_LOG)"; \
	tail -n 20 "$(ABOM_LOG)"; exit 1

status:
	@# Found the same way stop finds it, so a server started by hand is reported rather than
	@# denied. "Not running" gates running the test suites, so it has to mean it.
	@running=$$(for pid in $$(pgrep -x $(notdir $(BIN)) 2>/dev/null); do \
		case "$$(ps -o args= -p $$pid 2>/dev/null)" in (*" serve"*) echo $$pid;; esac; \
	done | tr '\n' ' '); \
	if [ -z "$$running" ]; then echo "status: not running"; exit 0; fi; \
	ps -o pid=,args= -p $$running; \
	curl -fsS "http://$(ABOM_ADDR)/api/discovery/status" && echo

## Stops every abom server on the machine, not merely what the pid file remembers: one started
## by hand still holds the port and still writes to a database, and leaving it is what makes
## `restart` a no-op and the next `start` refuse.
stop:
	@stopped=0; \
	for pid in $$(pgrep -x $(notdir $(BIN)) 2>/dev/null); do \
		case "$$(ps -o args= -p $$pid 2>/dev/null)" in \
			(*" serve"*) kill "$$pid" 2>/dev/null && echo "stop: stopped pid $$pid"; stopped=1;; \
		esac; \
	done; \
	rm -f "$(ABOM_PID)"; \
	if [ $$stopped -eq 0 ]; then echo "stop: not running"; exit 0; fi; \
	for i in 1 2 3 4 5; do \
		still=$$(for pid in $$(pgrep -x $(notdir $(BIN)) 2>/dev/null); do \
			case "$$(ps -o args= -p $$pid 2>/dev/null)" in (*" serve"*) echo $$pid;; esac; \
		done); \
		[ -z "$$still" ] && exit 0; \
		sleep 1; \
	done; \
	echo "stop: a server is still running after 5s"; exit 1

## style: one visual vocabulary. Drift here is invisible to every other check, and review had
## already missed six font sizes, three heading shapes and forty-nine inline styles.
style:
	@node ui/tools/check-style.mjs

## check-versions: exact pinning means the same upstream version is written in a suite, a pack
## manifest and a lock file. `codeql pack upgrade` rewrites two of the three.
check-versions:
	@./scripts/check-versions.sh

## test: needs git, which several tests clone from a local path to exercise checkout against the
## real thing. Reported here rather than 30 seconds in, and never skipped: a suite that shrinks
## to fit the machine reports success for tests it did not run.
##
## TMPDIR is what `t.TempDir()` reads, so all 144 of them land under $(ABOM_TMP) without a single
## test naming a path.
test:
	@if ! command -v git >/dev/null 2>&1; then \
		echo "git is not installed, and the checkout tests need it"; exit 1; \
	fi
	@mkdir -p "$(ABOM_TMP)/gotest"
	@TMPDIR="$(ABOM_TMP)/gotest" go test ./... -count=1 -race

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
