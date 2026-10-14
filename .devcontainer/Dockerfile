# The Demo Codespace image: the system packages ABOM needs, and the user the Demo runs as. Versioned
# tools are installed by on-create.sh, at the versions the checkout names.
# Pinned as tag@digest so Dependabot can follow the tag. Codespaces' Dev Containers CLI 0.83.3 logs
# "Could not parse image name" for that form, then pulls and reads the image anyway.
FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04@sha256:d94c97dd9cacf183d0a6fd12a8e87b526e9e928307674ae9c94139139c0c6eae

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        graphviz iptables sqlite3 unzip \
    && rm -rf /var/lib/apt/lists/*

# Demo runs as its own user, so the firewall can confine it to loopback without confining the
# editor. A Codespace runs this container on its VM's network, where that rule matches the user ID
# across the VM, so the ID is one outside the range the VM's own services take. The editor's user
# joins its group to read and edit the practice query pack, and may run commands as it: the base
# image lets vscode run commands as root alone.
RUN useradd --uid 4999 --user-group --create-home --shell /usr/sbin/nologin abom-demo \
    && usermod -aG abom-demo vscode \
    && echo 'vscode ALL=(abom-demo) NOPASSWD: ALL' >/etc/sudoers.d/abom-demo \
    && chmod 0440 /etc/sudoers.d/abom-demo \
    && visudo --check --quiet
