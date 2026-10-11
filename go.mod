module github.com/advanced-security/codeql-abom

go 1.25

// Read by actions/setup-go via go-version-file, so CI installs the same toolchain the Makefile
// pins. The `go` line above stays at the language version we actually require.
toolchain go1.26.0

require (
	github.com/CycloneDX/cyclonedx-go v0.12.0
	github.com/github/actions-lockfile/go v0.0.4
	github.com/github/copilot-sdk/go v1.0.14
	github.com/lib/pq v1.10.9
	github.com/mattn/go-sqlite3 v1.14.50
	github.com/spdx/tools-golang v0.5.7
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/anchore/go-struct-converter v0.1.0 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/ebitengine/purego v0.10.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
)
