---
paths:
  - "chain/**"
  - "record/**"
  - "client/**"
  - "internal/gateway/**"
  - "cmd/**"
---

# Cryptography and DNS invariants

Each rule below protects a claim in `docs/specs/pages/threat-model.md`. A change that breaks one rule is a design change, and it needs a spec update first.

## Verification

- Write no cryptographic code. Call `github.com/drand/drand/v2/crypto`.
- Take a public key only from the registry. Never read a key from DNS or from an upstream.
- The gateway verifies a round before the round enters the store.
- The client library verifies a round before it returns the round.
- Compute `randomness` from the signature. Never read `randomness` from a record or an upstream. The one exception is `scripts/refreshvectors`, which stores the value as expected test data in `testdata/vectors.json`.
- `Round` rejects a record with another round number.

## Round numbers

- A valid round number is from 1 to 2^53. Round 0 must never reach an upstream.
- A round number label has no leading zero.
- Use `Chain.RoundAfter` to pin a round. `Chain.RoundAt` returns a round that already exists.

## Answers

- A future round returns NODATA, never NXDOMAIN. A resolver can extend NXDOMAIN to all names below it (RFC 8020).
- An empty non-terminal returns NODATA, never NXDOMAIN. A QNAME-minimizing resolver asks for that name first.
- The negative TTL of a future round is `clamp(ceil(RoundTime(n) - now), 1, 300)`. It must not extend past the round time.
- A future round causes no upstream request.
- The SOA TTL and the SOA MINIMUM both carry the computed negative TTL (RFC 2308 section 3).
- The client library has no fallback transport. See `docs/adr/0001-no-client-fallback.md`.
- The clock can change only the error type of `Round`. The clock must never affect whether a round passes.
