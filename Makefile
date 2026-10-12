# One definition of "verified", shared by developers and CI.

# Runtime output stays under one gitignored checkout directory; the corpus is not temporary.
ABOM_TMP  ?= $(CURDIR)/.abom

ABOM_ADDR ?= 127.0.0.1:8099
ABOM_DB   ?= $(ABOM_TMP)/db/abom.db
ABOM_LOG  ?= $(ABOM_TMP)/logs/serve.log
ABOM_PID  ?= $(ABOM_TMP)/serve.pid
ABOM_UI   ?= $(ABOM_TMP)/ui
ABOM_WORK ?= $(ABOM_TMP)/work
# The operator's query pack; without one no request can be authored and the catalog shows no progress.
ABOM_QL   ?= $(ABOM_TMP)/ql/custom
# Built CodeQL databases are kept here, so a run queries them again rather than extracting anew.
ABOM_DATABASES ?= $(ABOM_TMP)/databases
db-absolute = $(if $(filter postgres://% postgresql://%,$(1)),$(1),$(abspath $(1)))

# Extra server flags; `ABOM_SERVE_FLAGS=--expand` starts discovery.
ABOM_SERVE_FLAGS ?=

# Pin the Go toolchain so vet, test and lint read the same export data as CI.
GOTOOLCHAIN := go1.26.0
export GOTOOLCHAIN

BIN      := bin/codeql-abom
# Repository Go source only: a killed go test leaves unformatted cgo output under $(ABOM_TMP).
GO_FILES := $(shell find . \( -path ./ui -o -path '$(patsubst $(CURDIR)/%,./%,$(abspath $(ABOM_TMP)))' \) -prune -o -name '*.go' -print)

# Empty is a development build. A release sets it, e.g. ABOM_VERSION=v2.27.1-next.1.
ABOM_VERSION ?=
CODEQL_VERSION := $(patsubst v%,%,$(shell cat .codeql-version))
LDFLAGS := -X main.codeqlVersion=$(CODEQL_VERSION) $(if $(ABOM_VERSION),-X main.version=$(patsubst v%,%,$(ABOM_VERSION)))
# What every platform's archive shares: the published interface, its notices and its SDK.
ABOM_RELEASE ?= $(ABOM_TMP)/release

.PHONY: backend-test bench build check-corrupt corpus check-release-version check-versions clean e2e e2e-corpus e2e-corpus-run e2e-run \
	fmt itest js lint logs package pins publish ql \
	quality release-archive release-ui restart script-test security-test selfcheck serve start status stop style test ui \
	ui-test verify vet

.DEFAULT_GOAL := build

verify: check-corrupt check-versions check-release-version pins fmt vet lint test script-test js style ql ui ui-test e2e

## bench: time the store read paths against a real corpus.
ABOM_BENCH_DB ?= $(ABOM_TMP)/bench.db
bench:
	@if [ ! -f "$(ABOM_BENCH_DB)" ]; then \
		echo "bench: set ABOM_BENCH_DB to an isolated corpus snapshot"; \
	fi
	@ABOM_BENCH_DB="$(ABOM_BENCH_DB)" go test ./internal/... -run '^$$' -bench . -benchmem

build:
	@go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/codeql-abom

## check-corrupt: catch a duplicated package clause.
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
ABOM_CORPUS_DB ?= $(ABOM_TMP)/corpus/abom.db
# The query pack a server is given with --custom-ql-dir, replaced on every build like the database.
ABOM_CORPUS_QL ?= $(ABOM_TMP)/corpus/ql
corpus:
	@if [ "$(abspath $(ABOM_CORPUS_DB))" = "$(abspath $(ABOM_DB))" ]; then \
		echo "corpus: refusing to overwrite the configured working database."; exit 1; \
	fi
	@ABOM_DB="$(ABOM_CORPUS_DB)" go run ./cmd/abom-corpus --queries "$(ABOM_CORPUS_QL)"

## e2e: drive the published interface in a real browser.
ABOM_E2E_DIR   ?= $(ABOM_TMP)/e2e
ABOM_E2E_DB    ?= $(ABOM_E2E_DIR)/abom.db
ABOM_E2E_PORT  ?= 8231
ABOM_E2E_FLAGS ?=
# Playwright positional filters; empty runs every spec.
ABOM_E2E_SPECS ?=
# One page or tag: `/codeql?tab=runs`, `inventory:workflows`, `node`.
ABOM_E2E_PAGE ?=
# Browser channel; empty uses Playwright's pinned Chromium.
ABOM_E2E_CHANNEL ?=
# Query pack for --custom-ql-dir; empty serves none.
ABOM_E2E_QL ?=
## e2e-corpus: the same suite against the shared data set rather than an empty database.
e2e-corpus: corpus
	@ABOM_E2E_DB="$(call db-absolute,$(ABOM_CORPUS_DB))" ABOM_E2E_QL="$(abspath $(ABOM_CORPUS_QL))" $(MAKE) e2e

## e2e-run, e2e-corpus-run: run against existing binary, UI and database.
e2e-corpus-run:
	@case "$(ABOM_CORPUS_DB)" in postgres://*|postgresql://*) ;; *) \
	if [ ! -f "$(ABOM_CORPUS_DB)" ]; then \
		echo "e2e-corpus-run: no fixture database; 'make corpus' builds it."; \
		echo "e2e-corpus-run: the server would otherwise make an empty one, and every"; \
		echo "e2e-corpus-run: populated assertion would skip and be reported as a pass."; exit 1; \
	fi ;; esac
	@if [ ! -f "$(ABOM_CORPUS_QL)/qlpack.yml" ]; then \
		echo "e2e-corpus-run: no query pack at $(ABOM_CORPUS_QL); 'make corpus' lays it out."; exit 1; \
	fi
	@ABOM_E2E_DB="$(call db-absolute,$(ABOM_CORPUS_DB))" ABOM_E2E_QL="$(abspath $(ABOM_CORPUS_QL))" $(MAKE) e2e-run

## `e2e` builds, then runs; recursion keeps `make -j e2e` from starting too early.
e2e: build publish
	@$(MAKE) e2e-run

e2e-run:
	@if [ "$(abspath $(ABOM_E2E_DB))" = "$(abspath $(ABOM_DB))" ]; then \
		echo "e2e: refusing to run against the configured working database."; \
		echo "e2e: take a copy first, and note that cp does not read the WAL:"; \
		echo "  use a backend-native snapshot rather than the live store"; exit 1; \
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
		ABOM_E2E_PAGE="$(ABOM_E2E_PAGE)" \
		ABOM_E2E_QL="$(ABOM_E2E_QL)" \
		npx playwright test $(ABOM_E2E_FLAGS) $(ABOM_E2E_SPECS)

fmt:
	@out=$$(gofmt -l $(GO_FILES) 2>&1 || true); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; \
	echo "gofmt: clean"

## js: test browser code that Go and Razor checks do not see.
js:
	@node --test ui/wwwroot/js/*.test.js ui/tests/e2e/*.test.mjs ui/tools/*.test.mjs && echo "js: ok"

## script-test: test development harness lifecycle without starting a database.
script-test:
	@python3 -m unittest discover -s scripts -p 'test_*.py'

## backend-test: run the store suite and replica routing on isolated Docker PostgreSQL alone.
backend-test:
	@python3 scripts/with-postgres.py test

# Part of `verify`; missing golangci-lint is reported directly.
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

## release-ui: the interface every platform's archive shares, into $(ABOM_RELEASE).
release-ui: publish
	@rm -rf "$(ABOM_RELEASE)" && mkdir -p "$(ABOM_RELEASE)"
	@cp -R "$(ABOM_UI)/wwwroot" "$(ABOM_RELEASE)/wwwroot"
	@python3 scripts/release/notices.py nuget >"$(ABOM_RELEASE)/ui-notices.txt"
	@python3 scripts/release/notices.py nuget-cyclonedx >"$(ABOM_RELEASE)/ui.cdx.json"
	@printf '{"dotnet": "%s"}\n' "$$(dotnet --version)" >"$(ABOM_RELEASE)/ui-tools.json"
	@echo "release-ui: $(ABOM_RELEASE)"

## release-archive: this platform's archive under dist/, from what release-ui wrote.
release-archive:
	@if [ -z "$(ABOM_VERSION)" ]; then \
		echo "release-archive: set ABOM_VERSION, such as ABOM_VERSION=v$(CODEQL_VERSION)"; exit 1; \
	fi
	@if [ ! -f "$(ABOM_RELEASE)/ui-tools.json" ]; then \
		echo "release-archive: nothing at $(ABOM_RELEASE); run 'make release-ui' first"; exit 1; \
	fi
	@scripts/release/assemble.sh "$(ABOM_VERSION)" "$(ABOM_RELEASE)" dist

## pins: verify `.github/workflows/actions.lock` without editing it.
pins:
	@if ! gh actions-lock --help >/dev/null 2>&1; then \
		echo "gh actions-lock is not installed: gh extension install github/gh-actions-lock"; exit 1; \
	fi
	@gh actions-lock --verify-local --no-fix --no-interactive

## publish: build the interface into $(ABOM_UI). Separate from `ui`, which only typechecks it.
publish:
	@rm -rf "$(ABOM_UI)"
	@dotnet publish ui/Abom.WebAssembly.csproj -c Release -o "$(ABOM_UI)" --nologo -v quiet
	@echo "publish: $(ABOM_UI)/wwwroot"

## ql: query tests. Build lockfile-bearing test databases before `codeql test run`.
ql:
	@codeql test run --threads=0 ql/actions/graph-extraction/test ql/actions/findings/test

restart: stop start

## itest: exercise CodeQL protocols that only a real server can answer.
# Sparse-filter coverage needs source outside .github/.
ABOM_ITEST_DB ?= $(ABOM_TMP)/itest-db
ABOM_ITEST_SPARSE_DB ?= $(ABOM_TMP)/itest-sparse-db
# Assert that --overlay-base stays blocked for actions databases.
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
		ABOM_ITEST_OVERLAY_DB="$(ABOM_ITEST_OVERLAY_DB)" ABOM_ITEST_CODEQL=1 TMPDIR="$(ABOM_TMP)/gotest" \
		go test -p 1 ./internal/runner/ ./internal/pipeline/ ./internal/expand/ -count=1
	@echo "itest: ok"

## quality: analyze our own source with our own tool, in every language we write.
quality: selfcheck
	@go run ./cmd/codeql-abom selfcheck --language=go \
		--suite advanced-security/abom-go-quality-src:codeql-suites/abom-go-quality.qls
	@go run ./cmd/codeql-abom selfcheck --language=csharp --source-root=ui \
		--suite advanced-security/abom-csharp-quality-src:codeql-suites/abom-csharp-quality.qls
	@go run ./cmd/codeql-abom selfcheck --language=javascript \
		--suite advanced-security/abom-javascript-quality-src:codeql-suites/abom-javascript-quality.qls
	@go run ./cmd/codeql-abom selfcheck --language=python --source-root=scripts \
		--suite advanced-security/abom-python-quality-src:codeql-suites/abom-python-quality.qls

## security-test: enforce the HTTP and deployed-query trust boundaries without external services.
security-test:
	@go test ./internal/serve ./internal/customql \
		-run '^(TestHTTPBoundary|TestQueryLifecycle|TestDeployedSuite)' -count=1

## selfcheck: run security regressions and our own analysis over our own workflows.
selfcheck: security-test
	@go run ./cmd/codeql-abom selfcheck \
		--suite advanced-security/abom-actions-selfcheck-src:codeql-suites/abom-selfcheck.qls

## serve: run in the foreground, for watching it work. `make start` for the background.
serve: build publish
	@mkdir -p "$(ABOM_WORK)" "$(dir $(ABOM_DB))" "$(dir $(ABOM_LOG))"
	@ABOM_DB="$(ABOM_DB)" $(BIN) serve --addr "$(ABOM_ADDR)" \
		--ui-dir "$(ABOM_UI)/wwwroot" --work-dir "$(ABOM_WORK)" --custom-ql-dir "$(ABOM_QL)" \
		--database-cache-dir "$(ABOM_DATABASES)" $(ABOM_SERVE_FLAGS)

## start: serve in the background, rebuilding both halves first.
start: build publish
	@# One dev server per machine; match executable names so this shell is excluded.
	@running=$$(for pid in $$(pgrep -x $(notdir $(BIN)) 2>/dev/null); do \
		case "$$(ps -o args= -p $$pid 2>/dev/null)" in (*" serve"*) echo $$pid;; esac; \
	done | tr '\n' ' '); \
	if [ -n "$$running" ]; then \
		echo "start: a server is already running, and only one is allowed:"; \
		ps -o pid=,args= -p $$running; \
		echo "start: use 'make restart', or 'make stop' first"; exit 1; \
	fi
	@mkdir -p "$(ABOM_WORK)" "$(dir $(ABOM_DB))" "$(dir $(ABOM_LOG))"
	@ABOM_DB="$(ABOM_DB)" $(BIN) serve --addr "$(ABOM_ADDR)" \
		--ui-dir "$(ABOM_UI)/wwwroot" --work-dir "$(ABOM_WORK)" --custom-ql-dir "$(ABOM_QL)" \
		--database-cache-dir "$(ABOM_DATABASES)" $(ABOM_SERVE_FLAGS) \
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
	@# Report servers started by hand too; test suites depend on this answer.
	@running=$$(for pid in $$(pgrep -x $(notdir $(BIN)) 2>/dev/null); do \
		case "$$(ps -o args= -p $$pid 2>/dev/null)" in (*" serve"*) echo $$pid;; esac; \
	done | tr '\n' ' '); \
	if [ -z "$$running" ]; then echo "status: not running"; exit 0; fi; \
	ps -o pid=,args= -p $$running; \
	curl -fsS "http://$(ABOM_ADDR)/api/discovery/status" && echo

## stop: stop every abom server on the machine, including one started by hand.
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

## style: enforce one visual vocabulary.
style:
	@node ui/tools/check-style.mjs

## check-versions: keep suite, manifest and lock pins aligned.
check-versions:
	@./scripts/check-versions.sh

## check-release-version: a pre-release suffix reaches ABOM's pack versions and nothing else.
check-release-version:
	@./scripts/check-release-version.sh

## test: run Go tests under $(ABOM_TMP) because checkout tests need git.
test: script-test
	@if ! command -v git >/dev/null 2>&1; then \
		echo "git is not installed, and the checkout tests need it"; exit 1; \
	fi
	@mkdir -p "$(ABOM_TMP)/gotest"
	@TMPDIR="$(ABOM_TMP)/gotest" python3 scripts/with-postgres.py suite

## ui: catch Razor syntax errors that Go checks cannot see.
ui:
	@dotnet build ui/Abom.WebAssembly.csproj -c Release --nologo -v quiet && echo "ui: ok"

## ui-test: component behavior; layout stays in browser tests.
ui-test:
	@dotnet test ui/tests/Abom.WebAssembly.Tests/Abom.WebAssembly.Tests.csproj \
		--nologo -v quiet && echo "ui-test: ok"

vet:
	@go vet ./... && echo "vet: ok"
