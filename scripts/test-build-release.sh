#!/usr/bin/env bash
# Dry-run test for build-release.sh. It writes only inside a temporary directory.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# A passing build lists eight binaries and one checksum file.
"$root/scripts/build-release.sh" v0.0.0-test "$work/ok" >/dev/null
count="$(find "$work/ok" -type f ! -name SHA256SUMS | wc -l | tr -d ' ')"
[ "$count" = 8 ] || fail "expected 8 binaries, found $count"
[ -s "$work/ok/SHA256SUMS" ] || fail "SHA256SUMS is missing or empty"
[ "$(wc -l < "$work/ok/SHA256SUMS" | tr -d ' ')" = 8 ] || fail "SHA256SUMS must list 8 files"
for target in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
  for cmd in entropy53 entropy53d; do
    [ -f "$work/ok/$cmd-$target" ] || fail "missing $cmd-$target"
  done
done

# The tag reaches the binary through the version variable.
host_bin="$work/ok/entropy53-$(go env GOOS)-$(go env GOARCH)"
if [ -x "$host_bin" ]; then
  [ "$("$host_bin" --version)" = "v0.0.0-test" ] || fail "version string does not match the tag"
fi

# A failed build stops the script and leaves no checksum file.
if GO=false "$root/scripts/build-release.sh" v0.0.0-test "$work/bad" >/dev/null 2>&1; then
  fail "script succeeded although the build failed"
fi
[ ! -e "$work/bad/SHA256SUMS" ] || fail "checksum file exists after a failed build"

echo "ok"
