#!/usr/bin/env bash
# Build the release binaries and a SHA-256 checksum file.
# Usage: scripts/build-release.sh <tag> <output-dir>
# The script builds the two binaries for four targets. If one build fails, the
# script exits non-zero and writes no checksum file, so no release can follow.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: $0 <tag> <output-dir>" >&2
  exit 2
fi
tag="$1"
# Resolve the output directory before the cd, so that a relative path keeps the caller's meaning.
mkdir -p "$2"
out="$(cd "$2" && pwd)"
# The build paths are relative to the module root, so the script must run from there.
cd "$(dirname "$0")/.."

# GO is overridable so that the test can force a build failure.
go_cmd="${GO:-go}"
module="github.com/Crank-Git/entropy53"
commands="entropy53 entropy53d"
targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"

mkdir -p "$out"
for target in $targets; do
  goos="${target%/*}"
  goarch="${target#*/}"
  for cmd in $commands; do
    # CGO is off so that the binaries do not depend on the build host's libc.
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" "$go_cmd" build -trimpath \
      -ldflags "-s -w -X $module/internal/version.Version=$tag" \
      -o "$out/$cmd-$goos-$goarch" "./cmd/$cmd"
  done
done

# Run the checksum from inside the directory so that the file lists bare names.
cd "$out"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum entropy53* > SHA256SUMS
else
  shasum -a 256 entropy53* > SHA256SUMS
fi
