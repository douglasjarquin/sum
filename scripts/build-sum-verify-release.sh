#!/usr/bin/env bash
# Build the sum-verify release binaries and checksums.txt, refusing any Go other
# than the exact version the committed mise.toml pins. The tag-only release
# workflow runs this script; tests/test_release_build.py exercises it on pull
# requests.
#
# Usage: scripts/build-sum-verify-release.sh <version> [<output dir>]
set -euo pipefail

version="${1:?usage: build-sum-verify-release.sh <version> [<output dir>]}"
out="${2:-dist}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version $version is not X.Y.Z" >&2; exit 1; }

case "$out" in /*) ;; *) out="$PWD/$out" ;; esac
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Released binaries embed the toolchain that built them. Read the pin with
# mise's TOML parser from the committed file only: `mise current go` follows
# MISE_GO_VERSION, `mise shell`, and local config, which could lower both the
# compiler and the expected version together. Never let GOTOOLCHAIN switch
# toolchains, and refuse any other go found first on PATH.
export GOTOOLCHAIN=local
pinned_go="$(mise config get --file mise.toml tools.go)" || { echo "cannot read tools.go from mise.toml" >&2; exit 1; }
if [[ ! "$pinned_go" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "mise.toml must pin an exact Go version (x.y.z) as tools.go, not '$pinned_go'" >&2
  exit 1
fi
want_go="go$pinned_go"
build_go="$(go env GOVERSION)"
if [[ "$build_go" != "$want_go" ]]; then
  echo "build Go $build_go is not the mise-pinned $want_go" >&2
  exit 1
fi

# A fresh output directory keeps stale binaries out of checksums.txt.
mkdir "$out"
for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  os="${target%/*}"
  arch="${target#*/}"
  bin="sum-verify-$os-$arch"
  (cd tools/verify && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-s -w -X github.com/douglasjarquin/sum/tools/verify/internal/tool.Version=$version" \
    -o "$out/$bin" ./cmd/sum-verify)
  # Read build info by basename so an output path with spaces cannot split the field.
  embedded_go="$(cd "$out" && go version "$bin" | awk '{print $2}')"
  if [[ "$embedded_go" != "$want_go" ]]; then
    echo "$bin embeds $embedded_go, want $want_go" >&2
    exit 1
  fi
done

cd "$out"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum sum-verify-* > checksums.txt
else
  shasum -a 256 sum-verify-* > checksums.txt
fi
go version sum-verify-*
