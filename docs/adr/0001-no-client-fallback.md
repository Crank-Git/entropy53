# 0001 — The client library has no fallback transport

Date: 2026-11-06

Status: accepted

## Context

DNS can fail a client in three ways: a withheld round, a stalled gateway, or a junk answer in a resolver cache. The planner offered three options: report only, an opt-in HTTP fallback, or a retry across several resolvers.

## Decision

The client library reports a typed error and stops. The caller decides what to do next. The caller can create another `Client` with another resolver.

## Consequences

- The client library has no HTTP dependency, and it never leaves the DNS transport without the caller's knowledge.
- A junk answer in one resolver cache stops reads through that resolver until the TTL ends.
- An application that needs high availability writes its own fallback.
