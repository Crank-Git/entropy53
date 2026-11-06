# entropy53

entropy53 serves drand rounds over DNS. A gateway answers TXT queries on a delegated zone. A client verifies each round with a BLS signature against a public key in the registry, so the gateway is not trusted. The main use is a pinned round for a draw. The `latest` name is for convenience only.

Design lives in `docs/specs/`. Read `docs/specs/spec.md` and the relevant `docs/specs/features/*.md` before you build a feature. The words in `docs/specs/spec.md` § Terms are the project vocabulary.

## Stack

- Go 1.27. Module `github.com/Crank-Git/entropy53`.
- `github.com/miekg/dns` v1.1.73 for DNS. Import it only in `internal/gateway/` and `client/`.
- `github.com/drand/drand/v2/crypto` v2.1.8 for verification. Write no cryptographic code.

## Layout

- `cmd/entropy53d/`: gateway main.
- `cmd/entropy53/`: CLI main.
- `chain/`: public. Registry and round arithmetic.
- `record/`: public. Record body encoder and decoder.
- `client/`: public. Client library.
- `internal/gateway/`: names, answers, store, upstream, follower, limits.
- `internal/version/`: build version source.
- `scripts/`: release build script and its test.
- `testdata/vectors.json`: real rounds.
- `test/interop/`: tests against real resolvers.
- `draft/`: Internet-Draft source.

## Commands

These commands apply after Epic 0.

- Test: `go test -race ./...`
- Interop test: `go test -tags interop ./test/interop/...` (needs Docker)
- Lint: `golangci-lint run`
- Format: `gofmt -w .`
- Build: `go build ./...`
- Refresh vectors: `make refresh-vectors` (manual, contacts an upstream)

## Conventions

- Verify before use. The gateway verifies before it keeps a round. The client library verifies before it returns a round.
- The client library computes `randomness`. No code reads `randomness` from a gateway or an upstream.
- Use `Chain.RoundAfter` to pin a round. `Chain.RoundAt` returns a round that already exists.
- A future round returns NODATA, never NXDOMAIN. An empty non-terminal returns NODATA, never NXDOMAIN.
- No normal test contacts a real upstream.
- A commit message states what changed and why, in the imperative.

## Branches

`dev` receives each batch. The maintainer promotes `dev` to `main` as a separate approved step. A `v*` tag on `main` creates a GitHub Release.

## Filing gate

- **A finding earns a tracker issue in five cases, and never otherwise.** Behavior. A user-visible output. A guard that guards nothing. A blocked epic. A question the maintainer must rule. One carve-out: anything under `docs/specs/` that describes the wrong product earns a spec-update issue, because the spec decides what gets built.

  **Every other finding is repaired by the change set that found it, and no issue is filed.** A falsified sentence, a moved citation, a stale count, a missing term and prose drift each reach the repair. A repair that turns out to touch behavior stops and is filed as a behavior finding.

  Where the repair goes:
  - Found while building an issue → that issue's own PR, limited to files it already touches.
  - Found at review of the batch integration branch → a documentation commit on that branch, before the CI-trigger commit.
  - Found with nothing open → the next batch into `dev`.

- A changelog or spec-changelog entry records a **decision**, made at a batch gate. No rule, hook, CI check, or test may require a changelog row, a spec edit, or a docs-accuracy pass **per change set or per batch member** — a batch that shipped as planned earns no line. A test that asserts per-member bookkeeping agreement re-creates the churn the filing gate exists to prevent, and fails review here.
