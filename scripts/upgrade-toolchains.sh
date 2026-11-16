#!/bin/bash
# Upgrades the language toolchains of a running image (the same steps as install_prerequisite.sh, for a container that is not rebuilt):
# Go, yaegi, JDK and Rust. Safe to run again. Run as root.
set -euo pipefail
GO_VERSION=${GO_VERSION:-1.23.12}
YAEGI_VERSION=${YAEGI_VERSION:-v0.16.1}
RUST_VERSION=${RUST_VERSION:-1.85.1}
JDK_PACKAGE=${JDK_PACKAGE:-openjdk-21-jdk-headless}
arch=$(dpkg --print-architecture)

# Go: the official release, ahead of the apt one on the PATH
if ! /usr/local/go/bin/go version 2>/dev/null | grep -q "go$GO_VERSION "; then
  curl -fsSL "https://go.dev/dl/go$GO_VERSION.linux-$arch.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz && rm /tmp/go.tar.gz
fi
ln -sf /usr/local/go/bin/go /usr/local/bin/go
ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# yaegi: its standard library is the one of the Go it was built with, so it is built again with the new Go
tmp=$(mktemp -d)
env -u GOROOT GO111MODULE=on GOFLAGS= GOTOOLCHAIN=local GOPATH="$tmp/gopath" GOBIN=/usr/local/bin \
  /usr/local/go/bin/go install "github.com/traefik/yaegi/cmd/yaegi@$YAEGI_VERSION"
chmod -R u+w "$tmp"; rm -rf "$tmp"

# JDK
apt-get update -y
apt-get install -y --no-install-recommends "$JDK_PACKAGE"
jdk_home=$(ls -d /usr/lib/jvm/java-21-openjdk-"$arch" 2>/dev/null || true)
if [ -n "$jdk_home" ]; then
  for t in java javac jshell jar javadoc jcmd; do
    [ -x "$jdk_home/bin/$t" ] && update-alternatives --set "$t" "$jdk_home/bin/$t" || true
  done
fi

# Rust: rustup's toolchain (evcxr and cargo from ~/.cargo/bin use it)
if [ -x "$HOME/.cargo/bin/rustup" ]; then
  "$HOME/.cargo/bin/rustup" toolchain install "$RUST_VERSION" --profile minimal
  "$HOME/.cargo/bin/rustup" default "$RUST_VERSION"
fi
go version; yaegi version | head -1; java -version 2>&1 | head -1; "$HOME/.cargo/bin/rustc" --version; evcxr --version | head -1
