# The Demo Codespace image: ABOM's pinned toolchain and nothing a user supplies.
FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

# Matches .codeql-version; on-create.sh refuses an image whose CLI differs.
ARG CODEQL_VERSION=v2.27.1

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        graphviz iptables sqlite3 unzip \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/codeql.zip \
        "https://github.com/github/codeql-cli-binaries/releases/download/${CODEQL_VERSION}/codeql-linux64.zip" \
    && unzip -q /tmp/codeql.zip -d /opt \
    && rm /tmp/codeql.zip \
    && /opt/codeql/codeql version --format=terse

# Demo runs as its own user, so the firewall can confine it to loopback without confining the
# editor. The editor's user joins its group to read and edit the practice query pack.
RUN useradd --system --create-home --shell /usr/sbin/nologin abom-demo \
    && usermod -aG abom-demo vscode
