#!/usr/bin/env bash
# Cross-compile the ltm Go binary for every platform the VSCode extension supports.
# Output goes into extension/bin/<platform>-<arch>/ltm[.exe], picked at runtime.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
ext_root="$(cd "$here/.." && pwd)"
server_root="$(cd "$ext_root/../server" && pwd)"
out_root="$ext_root/bin"

rm -rf "$out_root"
mkdir -p "$out_root"

build() {
  local goos="$1" goarch="$2" suffix="${3:-}"
  local dir="$out_root/${goos}-${goarch}"
  local bin="ltm${suffix}"
  mkdir -p "$dir"
  echo "→ $goos/$goarch"
  (cd "$server_root" && \
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "$dir/$bin" .)
  # Ad-hoc codesign macOS targets so they survive the macOS Sequoia/Tahoe
  # provenance gate when spawned by signed apps (Claude Desktop, …).
  # Requires `codesign` (only present on macOS hosts).
  if [ "$goos" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
    codesign --sign - --force --timestamp=none "$dir/$bin" 2>&1 | \
      grep -v "replacing existing" || true
  fi
}

build darwin  arm64
build darwin  amd64
build linux   amd64
build linux   arm64
build windows amd64 .exe

echo
echo "Built binaries:"
find "$out_root" -type f -exec ls -lh {} \;
