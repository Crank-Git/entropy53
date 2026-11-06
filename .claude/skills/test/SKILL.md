---
name: test
description: Run the entropy53 test suites and select which suite a change needs. Use when asked to test, verify or check a change.
allowed-tools: Bash, Read
---

# Test entropy53

| Change touches | Run |
|---|---|
| Any Go code | `go test -race ./...` and `golangci-lint run` |
| `internal/gateway/`, `record/` or `client/` | Also `go test -tags interop ./test/interop/...` |
| `record/` or `testdata/vectors.json` | Also `go test ./draft/...` |
| `draft/` | `go test ./draft/...`, then `kdrfc draft/draft-crank-drand-dns.md` |

The interop tests need Docker. If Docker is not available on a developer machine, the tests skip. In CI, they fail.

A run with no Docker on a developer machine prints a plain `ok`. Add `-v` to see each skip.

To run a fuzz target for the 60 seconds that the acceptance criteria state, use `go test -fuzz=<name> -fuzztime=60s ./<package>`.

Normal tests never contact a real upstream. If a test needs a real round, read it from `testdata/vectors.json`.

Run these on a laptop one at a time. The race detector and the interop containers use many cores.
