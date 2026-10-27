#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# A Go distribution downloaded into the project can be used without a system install.
portable_go="$PWD/.cache/gomod/golang.org/toolchain@v0.0.1-go1.26.9.linux-amd64"
if ! command -v go >/dev/null && [[ -f "$portable_go/bin/go" ]]; then
  chmod u+x "$portable_go/bin/go" "$portable_go/pkg/tool/linux_amd64/"*
  export PATH="$portable_go/bin:$PATH"
fi
for tool in go gcc make; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool. See docs/VERIFICATION.md." >&2; exit 1; }
done
[[ $(go env GOOS) == linux ]] || { echo 'Linux Go is required; do not use go.exe from WSL.' >&2; exit 1; }
export GOPATH="$PWD/.cache/linux/gopath"
export GOMODCACHE="$PWD/.cache/gomod"
export GOCACHE="$PWD/.cache/linux/go-build"
export GOTMPDIR="$PWD/.cache/linux/tmp"
mkdir -p "$GOTMPDIR" bin/linux
go version
gcc --version | head -n 1
make LOCALBIN="$PWD/bin/linux" vet build test-race test-integration
