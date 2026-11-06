# 0002 — Use github.com/miekg/dns v1.1.73

Date: 2026-11-06

Status: accepted

## Context

Two Go DNS libraries have one author. `github.com/miekg/dns` is at v1.1.73, published 2026-08-19. Its README states "The version here will only see specific fixes and nothing else" and "At some point this repo will be archived." The successor `codeberg.org/miekg/dns` is at v0.6.128, which is a pre-1.0 version.

## Decision

Use `github.com/miekg/dns` v1.1.73. Import the library only in `internal/gateway/` and `client/`.

## Consequences

- The project starts on a stable API.
- The project must move to the successor at a later time. The import limit keeps that change small.
- A security defect in v1 after its archive would force that change early.
