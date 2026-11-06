# 0003 — Use the drand crypto package for verification

Date: 2026-11-06

Status: accepted

## Context

The project must verify three schemes on two curves. `github.com/drand/drand/v2/crypto` v2.1.8 verified real rounds on all three chains in a spike. The `crypto` and `common` packages pull 148 packages, which include grpc and protobuf, into the client library.

## Decision

Use `github.com/drand/drand/v2/crypto` at a pinned version. Write no cryptographic code in this project.

## Consequences

- An integrator receives a large dependency tree for a small function.
- The verification code is the same code that drand itself uses, so scheme changes come from one source.
- If the dependency size becomes a problem, a later ADR can select a smaller verifier. That verifier must pass all vectors.
