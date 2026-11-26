# syntax=docker/dockerfile:1.7
# One image for the coordinator and the analysis workers: the ABOM binary, its interface, ABOM's
# query packs with their dependencies installed, and the CodeQL CLI for the target platform, checked
# against the checksum its release publishes. Every base is pinned by digest, and the Go one is the
# toolchain go.mod names; scripts/check-versions.sh holds both.

FROM --platform=$BUILDPLATFORM golang:1.26.0-bookworm@sha256:2a0ba12e116687098780d3ce700f9ce3cb340783779646aafbabed748fa6677c AS binary
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY .codeql-version ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-X main.codeqlVersion=$(sed 's/^v//' .codeql-version)" \
      -o /out/codeql-abom ./cmd/codeql-abom

FROM --platform=$BUILDPLATFORM mcr.microsoft.com/dotnet/sdk:10.0@sha256:8c6edfb26c85153f236d26efebd9ad349aec403583f343f201ee67838b23658d AS interface
WORKDIR /src
# Restored from the project alone, so a change to the interface's sources reuses the restore.
COPY ui/Abom.WebAssembly.csproj ./ui/
RUN dotnet restore ui/Abom.WebAssembly.csproj --nologo -v quiet
COPY ui ./ui
RUN dotnet publish ui/Abom.WebAssembly.csproj -c Release -o /out/ui --no-restore --nologo -v quiet

FROM debian:bookworm-slim@sha256:e67eeead736ff7bbe258ef088bff683635c561b1fcfefad0806c1df4ab49ce0e AS codeql
ARG TARGETARCH
COPY .codeql-version /tmp/.codeql-version
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip && rm -rf /var/lib/apt/lists/*
RUN set -eu; version="$(cat /tmp/.codeql-version)"; \
    case "$TARGETARCH" in amd64) asset=codeql-linux64.zip ;; arm64) asset=codeql-linux-arm64.zip ;; *) exit 1 ;; esac; \
    base="https://github.com/github/codeql-cli-binaries/releases/download/${version}"; \
    curl -fsSL -o /tmp/codeql.zip "$base/$asset"; \
    curl -fsSL -o /tmp/codeql.sha "$base/$asset.checksum.txt"; \
    echo "$(cut -d' ' -f1 /tmp/codeql.sha)  /tmp/codeql.zip" | sha256sum -c -; \
    unzip -q /tmp/codeql.zip -d /opt; rm /tmp/codeql.zip

FROM debian:bookworm-slim@sha256:e67eeead736ff7bbe258ef088bff683635c561b1fcfefad0806c1df4ab49ce0e
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git graphviz \
      && rm -rf /var/lib/apt/lists/* \
      && groupadd --gid 10001 abom && useradd --uid 10001 --gid 10001 --create-home --home-dir /home/abom abom
COPY --from=codeql /opt/codeql /opt/codeql
COPY --chown=abom:abom codeql-workspace.yml /opt/abom/
COPY --chown=abom:abom ql /opt/abom/ql
ENV PATH=/opt/codeql:$PATH HOME=/home/abom
USER 10001:10001
WORKDIR /opt/abom
RUN set -eu; for manifest in $(find ql/actions -name codeql-pack.yml -not -path '*/test/*'); do \
      codeql pack install "$(dirname "$manifest")" >/dev/null; done
# Every worker is a fresh pod with no compilation cache, so without these each analysis spends
# minutes compiling the same queries. The image is immutable, so a .qlx cannot go stale.
RUN codeql query compile --precompile --threads=0 ql/actions/graph-extraction/src ql/actions/findings/src >/dev/null
COPY --from=binary /out/codeql-abom /usr/local/bin/codeql-abom
COPY --from=interface /out/ui/wwwroot /opt/abom/ui
ENTRYPOINT ["codeql-abom"]
CMD ["serve", "--help"]
