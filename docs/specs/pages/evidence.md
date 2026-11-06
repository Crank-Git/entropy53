---
id: evidence
title: Evidence and sources
---

# Evidence and sources

This page records what the planner checked on 2026-11-06, and how. A claim that is not on this page is an assumption, and `spec.md` lists it as such.

## Chain parameters

Retrieved from `https://api.drand.sh/<chain hash>/info`. Values are verbatim.

### quicknet

- Chain hash: `52db9ba70e0cc0f6eaf7803dd07447a1f5477735fd3f661792ba94600c84e971`
- Group hash: `f477d5c89f21a17c863a7f937c6a6d15859414d2be09cd448d4279af331c5d3e`
- Scheme: `bls-unchained-g1-rfc9380`
- Period: 3
- Genesis: 1692803367
- Public key: `83cf0f2896adee7eb8b5f01fcad3912212c437e0073e911fb90022d3e760183c8c4b450b6a0a6c3ac6a5776a2d1064510d1fec758c921cc22b0e17e63aaf4bcb5ed66304de9cf809bd274ca73bab4af5a6e9c76a4bc09e76eae8991ef5ece45a`

### default

- Chain hash: `8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce`
- Group hash: `176f93498eac9ca337150b46d21dd58673ea4e3581185f869672e59fa4cb390a`
- Scheme: `pedersen-bls-chained`
- Period: 30
- Genesis: 1595431050
- Public key: `868f005eb8e6e4ca0a47c8a77ceaa5309a47978a7c71bc5cce96366b5d7a569937c529eeda66c7293784a9402801af31`

### evmnet

- Chain hash: `04f1e9062b8a81f848fded9c12306733282b2727ecced50032187751166ec8c3`
- Group hash: `cd7ad2f0e0cce5d8c288f2dd016ffe7bc8dc88dbb229b3da2b6ad736490dfed6`
- Scheme: `bls-bn254-unchained-on-g1`
- Period: 3
- Genesis: 1727521075
- Public key: `07e1d1d335df83fa98462005690372c643340060d205306a9aa8106b6bd0b3820557ec32c2ad488e4d4f6008f89a346f18492092ccc0d594610de2732c8b808f0095685ae3a85ba243747b1b2f426049010f6b73a0cf1d389351d5aaaa1047f6297d3a4f9749b33eb2d904c9d9ebf17224150ddd7abd7567a9bec6c74480ee0b`

## Verification spike

A throwaway Go program used `github.com/drand/drand/v2/crypto` v2.1.8. It fetched round 1000 of each chain from `api.drand.sh`. The program is not part of this repository. Output, verbatim:

```
quicknet scheme=bls-unchained-g1-rfc9380 siglen=48 prevlen=0 verify=<nil> wrong-round-rejected=true randomness-match=true
default scheme=pedersen-bls-chained siglen=96 prevlen=96 verify=<nil> wrong-round-rejected=true randomness-match=true
evmnet scheme=bls-bn254-unchained-on-g1 siglen=64 prevlen=0 verify=<nil> wrong-round-rejected=true randomness-match=true
```

This result confirms four claims:

- One library verifies all three schemes.
- The signature lengths are 48, 96 and 64 bytes.
- A correct signature fails when the round number is wrong.
- `randomness` equals the output of `crypto.RandomnessFromSignature`.

The spike imported `crypto` and `common`. The two packages pull 148 packages, which include grpc and protobuf.

## Upstream behavior

Observed on live endpoints.

| Check | Result |
|---|---|
| `GET api.drand.sh/<quicknet>/public/99999999` | HTTP 425, body `Requested future beacon` |
| `GET api.drand.sh/v2/beacons/quicknet/rounds/99999999` | HTTP 425, body `Requested future beacon` |
| `GET api.drand.sh/<quicknet>/public/0` | HTTP 200 with the latest round |
| `GET api.drand.sh/<quicknet>/public/1` | HTTP 200 with round 1 |
| `GET api.drand.sh/v2/beacons` | `["evmnet","quicknet","default"]` |
| `GET drand.cloudflare.com/<quicknet>/public/latest` | `cache-control: max-age:2, public` |
| `api2`, `api3`, `drand.cloudflare.com`, `<each chain>/public/1000` | HTTP 200 for all nine combinations |

The live `public/latest` round and the formula agree. Round 33725163 was the latest at one moment, and the formula gave 33725178 for a time about 45 seconds later.

## Round 1 of default

Read by hand on 2026-11-06 from `https://api.drand.sh/8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce/public/1`. No test does this.

- The `signature` field has 96 bytes.
- The `previous_signature` field has 32 bytes. Its value equals the group hash of `default`.
- `Chain.Verify` with the registry public key returns no error for this round.

The record body for `default` holds a previous signature of 96 bytes, so it cannot hold this round. The maintainer ruled on #46 that the gateway does not serve round 1 of `default`.

## Size arithmetic

Each body has a 1-byte version and an 8-byte round number.

| Chain | Signature | Previous | Body | Base64 |
|---|---|---|---|---|
| `quicknet` | 48 | 0 | 57 | 76 |
| `evmnet` | 64 | 0 | 73 | 100 |
| `default` | 96 | 96 | 201 | 268 |

The brief states "~76 base64 chars" for quicknet and "~310 chars" for the chained case. The value 76 stays the same. The chained value was 312 with `randomness` in the body. This spec removes `randomness`, and adds a version byte, for 268.

## Documents

| Topic | Source | Retrieved |
|---|---|---|
| drand HTTP API, relays, chain hashes | https://docs.drand.love/developer/ and https://docs.drand.love/developer/http-api | 2026-11-06 |
| drand crypto package | https://pkg.go.dev/github.com/drand/drand/v2/crypto (v2.1.8) | 2026-11-06 |
| drand go-clients license and features | https://github.com/drand/go-clients | 2026-11-06 |
| RFC 9380 faults in third-party verifiers | https://docs.drand.love/blog/2023/10/16/quicknet-is-live | 2026-11-06 |
| Go DNS library, status of v1 and v2 | https://pkg.go.dev/github.com/miekg/dns (v1.1.73) and `go doc` | 2026-11-06 |
| TXT strings, 512-octet limit | https://www.rfc-editor.org/rfc/rfc1035.txt | 2026-11-06 |
| Negative answers and SOA MINIMUM | https://www.rfc-editor.org/rfc/rfc2308.txt | 2026-11-06 |
| NXDOMAIN and names below it | https://www.rfc-editor.org/rfc/rfc8020.txt | 2026-11-06 |
| QNAME minimization | https://www.rfc-editor.org/rfc/rfc9156.txt | 2026-11-06 |
| TCP on authoritative servers | https://www.rfc-editor.org/rfc/rfc7766.txt | 2026-11-06 |
| ANY answers | https://www.rfc-editor.org/rfc/rfc8482.txt | 2026-11-06 |
| Unbound defaults | https://unbound.docs.nlnetlabs.nl/en/latest/manpages/unbound.conf.html | 2026-11-06 |
| golangci-lint linters | https://golangci-lint.run/docs/linters/ | 2026-11-06 |
| kramdown-rfc | https://github.com/cabo/kramdown-rfc | 2026-11-06 |

## What this page does not confirm

- BIND defaults. Epic 5 reads the BIND manual.
- A public statement of upstream rate limits. The planner found none.
- HTTP 425 and `public/0` in the drand docs. These two come from observation only, so a change to either must not break the gateway.
- The absence of any other DNS delivery of drand rounds. One search found none, and a search cannot prove absence.
