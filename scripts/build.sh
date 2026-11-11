#!/bin/sh
# Cross-compiles kinkoku into kinkoku.koplugin/bin/<arch>/kinkoku.
# Usage: scripts/build.sh [version]
set -eu
cd "$(dirname "$0")/.."
version=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}

build() { # arch goarch goarm
	out=kinkoku.koplugin/bin/$1/kinkoku
	mkdir -p "$(dirname "$out")"
	CGO_ENABLED=0 GOOS=linux GOARCH=$2 GOARM=$3 \
		go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$out" ./cmd/kinkoku
	printf '%-6s %s bytes\n' "$1" "$(wc -c < "$out" | tr -d ' ')"
}

build armv6 arm 6
build armv7 arm 7
build arm64 arm64 ""
build x64 amd64 ""
