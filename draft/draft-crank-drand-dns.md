---
title: "Delivery of drand Rounds over DNS"
abbrev: "drand rounds over DNS"
docname: draft-crank-drand-dns-00
category: info
ipr: trust200902
area: Internet
workgroup: Network Working Group
keyword:
  - drand
  - randomness beacon
  - DNS
stand_alone: yes
v: 3
pi: [toc, sortrefs, symrefs]
author:
  - name: Christian Crank
    email: info@nocturnaltendenciesllc.com
normative:
  RFC1035:
  RFC2308:
  RFC4648:
  RFC6891:
  RFC7766:
  RFC8020:
  RFC8482:
  RFC9380:
  I-D.irtf-cfrg-bls-signature-05:
  DRAND-CRYPTO:
    title: "drand v2.1.8, package crypto, file schemes.go"
    target: https://github.com/drand/drand/blob/v2.1.8/crypto/schemes.go
    date: false
    author:
      - org: drand
  DRAND-KYBER:
    title: "drand/kyber v1.3.3, package pairing/bn254, file point.go"
    target: https://github.com/drand/kyber/blob/v1.3.3/pairing/bn254/point.go
    date: false
    author:
      - org: drand
informative:
  RFC9156:
  DRAND-HTTP:
    title: "drand HTTP API"
    target: https://docs.drand.love/developer/http-api
    date: false
    author:
      - org: drand
--- abstract

This document specifies how a gateway serves rounds of a drand randomness beacon in DNS TXT records, and how a client reads and verifies them.
A client checks the BLS signature of each round against a public key that the client already holds.
The gateway, the resolver and the network are therefore not trusted for integrity.
This document specifies the name shapes, the record body and the base64 text.
It also specifies the answer rules, the TTL rules and the client procedure, for the chains `quicknet`, `default` and `evmnet`.

--- middle

# Introduction

drand produces a numbered, signed random value on a fixed period.
A drand network is called a chain.
drand distributes rounds over HTTP and libp2p.
This document adds a DNS delivery, so that a client can read a round through an unmodified resolver.

The main use is a pinned round.
A person selects a future round number before the round time, and announces it for a draw.
Everyone reads that round later.
The value of a published round never changes and verifies itself, so a resolver cache can only help.
The `latest` name exists for convenience.
It adds no cryptographic property.

A gateway is an authoritative DNS server for one delegated zone.
A client queries a resolver for a TXT record, decodes the record, and verifies the signature.
A client that follows this document does not trust the gateway.
The gateway can withhold or delay a round, but it cannot forge one.

## Requirements Language

{::boilerplate bcp14-tagged}

## Terminology

chain:
: One drand network with its own public key, period and scheme.

beacon ID:
: The short drand name of a chain, for example `quicknet`.

round number:
: The positive integer that identifies a round.

round time:
: The time at which a chain produces a round: genesis time + (round number - 1) * period.

current round:
: The highest round number with a round time at or before the clock time: floor((now - genesis time) / period) + 1.

pin:
: To select a future round number before its round time.

zone:
: The DNS domain that a parent delegates to a gateway, for example `beacon.example`.

gateway:
: The authoritative DNS server for the zone.

round name:
: The DNS name `round.<n>.<chain>.<zone>`.

latest name:
: The DNS name `latest.<chain>.<zone>`.

round record:
: The single TXT resource record at a round name or a latest name.

record body:
: The bytes inside the base64 text of a round record.

NODATA:
: A response with RCODE NOERROR, no answer record and an SOA record in the authority section ({{RFC2308}}).

negative TTL:
: The smaller of the SOA record TTL and the SOA MINIMUM field in a negative response ({{Section 3 of RFC2308}}).

Text in the form `<n>` and `<chain>` stands for one DNS label.
In every example, the zone is `beacon.example`.
The first example in each section uses `quicknet`, because a pinned round is the main use.

# Name Shapes {#names}

A gateway serves one zone.
Every name below is relative to that zone.
`<chain>` is the beacon ID of a chain that the gateway serves.
`<n>` is a round number.

| Name | TXT query | Other types |
|---|---|---|
| apex | NODATA | SOA and NS answered, others NODATA |
| `<chain>` | NODATA | NODATA |
| `<n>.<chain>` | NODATA | NODATA |
| `round.<n>.<chain>` | round rules, see {{answer-rules}} | NODATA |
| `latest.<chain>` | latest rules, see {{answer-rules}} | NODATA |
| any other name | NXDOMAIN | NXDOMAIN |

For example, the round name of round 1000 of `quicknet` is `round.1000.quicknet.beacon.example`.
The latest name is `latest.quicknet.beacon.example`.

A gateway MUST compare names without regard to letter case.
A gateway MUST return the question name in the response with the letter case of the query.

The label `<n>` is valid only in canonical decimal form.
It has the digits 0 to 9 only, no sign, and no leading zero.
Its value is from 1 to 2^53, inclusive.
Two names therefore never hold one round.
A round number above 2^53 is invalid, because larger numbers lose precision in the JSON numbers of some clients.
Round 0 is invalid, because the drand HTTP path for round 0 returns the latest round and not round 0 ({{DRAND-HTTP}}).

A gateway MUST return NXDOMAIN for every name inside the zone that the table does not list.
This includes `round.01000.quicknet`, `round.0.quicknet`, `round.abc.quicknet` and every name under a chain that the gateway does not serve.
A gateway MUST return REFUSED for a name outside the zone.

The names `<n>.<chain>` and `<chain>` have no record, but they have names below them.
Such a name is an empty non-terminal.
A gateway MUST return NODATA, and MUST NOT return NXDOMAIN, for an empty non-terminal.
The reason is QNAME minimization.
A resolver that minimizes the query name asks for `<n>.<chain>.<zone>` before it asks for the round name ({{RFC9156}}).
NXDOMAIN at the shorter name would end the resolution, and it would deny every name below it ({{RFC8020}}).
A NOERROR response lets the resolver continue.

# Record Body {#record-body}

A round record is a TXT record.
Its text is the standard base64 encoding of a record body.
The record body carries the round number and the signature of one round.
It does not carry `randomness`.

## Layout

The record body is a byte string with this layout:

| Offset | Size | Field | Notes |
|---|---|---|---|
| 0 | 1 | version | 0x01 in this version. |
| 1 | 8 | round number | Unsigned integer, big-endian. |
| 9 | L | signature | L depends on the chain. |
| 9+L | P | previous signature | P depends on the chain. P is 0 for an unchained scheme. |

L and P are fixed for each chain ({{chain-parameters}}).
The body length is therefore 9 + L + P bytes exactly.
The body has no chain field.
The DNS name states the chain, so a decoder takes the chain as an input.
A signature that does not match the chain fails verification.
The body repeats the round number, because a latest name has no number in it.
The repeated number also binds a round name to its answer, so a gateway cannot return round 500 at the name of round 1000.

| Chain | L | P | Body bytes | Base64 characters | TXT strings |
|---|---|---|---|---|---|
| `quicknet` | 48 | 0 | 57 | 76 | 1 |
| `default` | 96 | 96 | 201 | 268 | 2 |
| `evmnet` | 64 | 0 | 73 | 100 | 1 |

A decoder first decodes the base64 text of the record ({{base64-text}}).
The checks below apply to the decoded body.
A decoder MUST reject a body whose length differs from the exact length for the chain.
A decoder MUST check the length before it reads any field.
A decoder MUST reject a version other than 0x01.
A decoder MUST reject a round number below 1 or above 2^53.
The version check and the round number check come after the length check, in either order.
The order of those two checks changes only which failure a decoder reports.

## Base64 Text {#base64-text}

The text of the record is the base64 encoding of the record body, with the standard alphabet and with padding ({{Section 4 of RFC4648}}).
The text has no white space and no line break.

A TXT record holds character-strings of at most 255 octets each ({{Section 3.3 of RFC1035}}).
An encoder MUST split a text of more than 255 characters into character-strings of 255 characters, and a last string with the rest.
An encoder MUST NOT emit a zero-length character-string.
The base64 text of `quicknet` and `evmnet` fits in one string.
The text of `default` needs two strings.

A decoder MUST concatenate all character-strings of one TXT record before it decodes the base64 text.
A decoder MUST NOT rely on a split at octet 255, and MUST accept a split at any position.
A decoder MUST ignore a zero-length character-string.
A record with no character-string, or with only zero-length character-strings, has an empty text, and its body fails the length check of {{record-body}}.
A decoder MUST reject the concatenated text in each of these cases:

* The text has a character outside the standard alphabet, and the padding character `=` is the only extra character allowed.
  This includes a space, a tab, a carriage return and a line feed.
* The text has padding that is missing, too long, or not at the end.
  Padding is too long when it has more than two `=` characters.
  A text whose length is not a multiple of 4 has padding that is missing or too long.
* The last base64 character has non-zero bits that the decoded bytes do not use.

Example.
The round record for round 1000 of `quicknet` is this single character-string:

~~~
AQAAAAAAAAPotEZ5uaWa8uyHaxprGtUuqbFhX8OYKxlXY1D5
  NEfLESXjQrc6jdK6y+R+S2tj7V45
~~~

The second line of the example continues the first line, and the line break is not part of the string.
The first byte is 0x01.
The next eight bytes are 0x00000000000003E8, which is 1000.
The last 48 bytes are the signature.

## Exception: Round 1 of default {#default-round-one}

Round 1 of `default` has a previous signature of 32 bytes, and not 96 bytes.
That round verifies in drand, but the layout above cannot hold it.
The layout stays as it is, and this version does not serve that round.
For a TXT query or an ANY query at the round name of round 1 of `default`, a gateway MUST return SERVFAIL.
For a query of another type at that name, a gateway MUST return NODATA with a negative TTL of 60.
The label `1` stays valid, so a gateway MUST NOT return NXDOMAIN.
A record body of 137 bytes for round 1 fails the length check of {{record-body}}.
A record body of 201 bytes can claim round 1.
It passes the length check, but it cannot verify, because the previous signature of that round has 32 bytes.
The signature check of {{check-record}} rejects such a body.

## Version

The version byte is 0x01.
A change to the layout MUST change the version byte.
The vectors of an earlier version stay as a separate vector set.
A client MUST reject a version that it does not know.

# Answer Rules {#answer-rules}

This section specifies each answer of a gateway and the TTL of each answer.
Every TTL rule has a reason, because a wrong negative answer stays in a resolver cache after the round exists.
The answers apply to a query of class IN.

## Round Names

A gateway MUST NOT keep, and MUST NOT serve, a round that it has not verified against the public key of the chain ({{chain-parameters}}).
A bad upstream then cannot put junk into a resolver cache through the gateway.

For a TXT query at the round name of a published round, for example `round.1000.quicknet.beacon.example`:

* The gateway MUST return exactly one TXT record, with the round record of that round.
* The TTL of that record MUST be 86400 seconds.
  The round never changes, so a long TTL is safe.
  Unbound limits a TTL to 86400 seconds by default, so a higher value gives no benefit there.

For a TXT query at the round name of a future round, which is a round number above the current round:

* A gateway MUST return NODATA.
* A gateway MUST NOT return NXDOMAIN for a future round.
  A future round number will exist.
  NXDOMAIN states that the name does not exist, and it also covers every name below it ({{RFC8020}}).
  NODATA applies to the TXT type at that exact name only ({{Section 2.2 of RFC2308}}).
* The negative TTL MUST be the number of seconds from now to the round time, rounded up and limited to the range 1 to 300.
  The TTL then never extends past the round time, so a client that polls early receives the round when it appears.
  The limit of 300 seconds bounds the cost of a clock error.
* A gateway SHOULD send no request to any upstream for a future round.
  A query for a future round then costs arithmetic only.

If the clock of the gateway is before the genesis time of the chain, the gateway has no current round.
A gateway MUST then return SERVFAIL for a TXT query or an ANY query at every round name of that chain.

For a TXT query at the round name of a round that is due but that the gateway cannot yet obtain:

* A gateway MUST return NODATA with a negative TTL of 1 second.
  The round can appear at any moment.
* If the gateway tried to obtain the round and failed for a reason other than "not published", it MUST return SERVFAIL.

## Latest Names

A gateway MAY serve the latest name.
The latest name gives no security gain.
A gateway can select among recent rounds at a latest name, so a draw MUST use a pinned round.

A gateway that serves the latest name:

* MUST return the latest verified round of the chain at `latest.<chain>`.
* MUST set the TTL to the number of seconds from now to the round time of the next round, rounded up and limited to the range 1 to the period of the chain.
  The value changes at that time.
* MUST return NODATA with a negative TTL of 1 second when it holds no round of that chain yet.

A gateway that does not serve the latest name MUST answer it as an unlisted name ({{names}}).

## Negative Answers

Every NODATA response MUST carry the SOA record of the zone in the authority section.
The SOA TTL and the SOA MINIMUM field MUST both equal the negative TTL of that response.
A resolver takes the smaller of the two ({{Section 3 of RFC2308}}), so setting both removes doubt.

| Answer | TTL | Reason |
|---|---|---|
| TXT at a round name | 86400 | The round is immutable. |
| TXT at a latest name | 1 to period | The value changes at the next round time. |
| NODATA, future round | 1 to 300 | The value must appear soon after publication. |
| NODATA, due round | 1 | The round can appear at any moment. |
| NODATA, no latest round yet | 1 | Same reason. |
| NODATA, other type at a TXT name, or empty non-terminal | 60 | Applies per type, and never hides a TXT record. |
| NXDOMAIN | 3600 | Only an invalid name reaches this answer. |

An NXDOMAIN response MUST carry the SOA record in the authority section, with a TTL and MINIMUM of 3600.

A query of type ANY at a round name or a latest name MUST be answered as a query of type TXT ({{Section 4.1 of RFC8482}}).
A query for a type other than TXT and ANY at a round name or a latest name MUST return NODATA with a negative TTL of 60.

## Zone Apex

A gateway MUST return the SOA record and the NS records of the zone at the apex, with a TTL of 3600 seconds.
In an answer to an SOA query at the apex, the MINIMUM field is 60.
A NODATA response for any other type at the apex has a negative TTL of 60.
The SOA MNAME is the first configured NS host name.
The NS host names SHOULD be outside the zone, so that the zone needs no address records and no glue.

## Protocol

A gateway MUST answer queries over UDP and over TCP ({{RFC7766}}).
A gateway MUST set the AA bit on every response to a question about a name inside the zone.
A gateway MUST clear the AA bit on every other response.
These are the REFUSED responses, the FORMERR and NOTIMP responses, the BADVERS response, and the TC response of {{response-rate}}.
A gateway MUST clear the RA bit.
A gateway MUST return the OPT record when the query has one ({{RFC6891}}).
The only exception is the TC response of {{response-rate}}, which holds no OPT record.
If the OPT record of a query has an EDNS version above 0, the gateway MUST return the extended RCODE BADVERS ({{Section 6.1.3 of RFC6891}}).
That response holds an OPT record of version 0 and no answer record, and the gateway MUST NOT obtain a round for it.
The gateway SHOULD announce a UDP payload size of 1232 octets, so that a UDP response does not fragment on a common path.
A gateway MUST ignore the DO bit, because the zone has no DNSSEC.

A gateway MUST set the TC bit when a UDP response exceeds the size that the client permits.
That size is 512 octets when the query has no OPT record.
A response for `default` is about 340 octets with a short zone name, so it fits in 512 octets.
A long zone name can exceed that size, and then the TC bit and a retry over TCP handle the case.

A gateway MUST return FORMERR for a query that has other than one question.
A gateway MUST return NOTIMP for an opcode other than QUERY.
A gateway MUST return REFUSED for a class other than IN.
A gateway MUST return REFUSED for a query of type AXFR or IXFR, and MUST NOT disclose zone content.

## Response Rate {#response-rate}

A reflection attacker can send queries with a spoofed source address.
A UDP response is several times larger than the query, so a gateway SHOULD limit its UDP responses for each source prefix.
A prefix SHOULD be an IPv4 /24 or an IPv6 /56.
Over the limit, a gateway SHOULD send every second response with only the header, the question and the TC bit, and SHOULD drop the others.
That response has no OPT record and a clear AA bit.
The TC bit makes a real client retry over TCP.
A gateway SHOULD apply no such limit to TCP, because a TCP source address is not spoofable.

# Client Procedure {#client-procedure}

A client reads a round from one resolver.
A client MUST hold the chain parameters of {{chain-parameters}}, and in particular the public key, before it starts.
A client MUST NOT obtain the public key from a gateway or from any DNS answer.

## Read a Pinned Round

To read round `n` of a chain, for example round 1000 of `quicknet`, a client does these steps in order:

1. The client MUST check that `n` is from 1 to 2^53.
   If not, it MUST stop and send no query.
2. The client queries the resolver for the TXT record at `round.<n>.<chain>.<zone>`.
   The client SHOULD send an OPT record that announces a UDP payload size of 1232 octets.
3. If the response has the TC bit, the client MUST repeat the query over TCP.
4. If the RCODE is neither NOERROR nor NXDOMAIN, the client MUST report a transport failure.
   A timeout and a network failure are also transport failures.
5. If the answer section is empty, the response is negative.
   The client then follows {{negative-response}}.
6. Otherwise the client tests each TXT record in the answer section with the steps of {{check-record}}.
   The client MUST ignore a record of another type.
   The client MUST return the first record that passes, and MUST ignore a record that fails.
7. If no record passes, the client MUST report that no record verifies.
   This includes an answer section with no TXT record.
   A gateway cannot cause a wrong value this way, but it can cause this error.

## Check One Record {#check-record}

For each TXT record, a client does these steps:

1. Concatenate all character-strings of the record, and decode the base64 text ({{record-body}}).
   If the decoding fails, the record fails.
2. Check the length and the version of the record body, and check that the round number is from 1 to 2^53 ({{record-body}}).
   If a check fails, the record fails.
3. When the client asked for a round number, compare the round number in the body with the requested number.
   If they differ, the record fails.
4. Verify the signature, as {{verification}} specifies.
   If the signature does not verify, the record fails.
5. Compute `randomness` as the SHA-256 hash of the signature.

A client MAY use the fields of the record body to run the checks above.
A client MUST NOT return a value from the record, or act on one, until the signature verifies.
A client MUST compute `randomness` from the signature.
A client MUST compute `randomness` as above and MUST NOT read `randomness` from a gateway or from an upstream.
A client MUST NOT return a round that failed verification.

## Verification {#verification}

A client verifies a signature with the scheme of the chain and the public key from the registry of {{chain-parameters}}.
The scheme is a BLS signature scheme ({{I-D.irtf-cfrg-bls-signature-05}}) with a hash to the signature group ({{RFC9380}}).
The drand code cites version 05 of that document for the form of the DST {{DRAND-CRYPTO}}.
Each chain defines the groups, the domain separation tag (DST) and the message below.
The message is a function of the round number and, for a chained scheme, of the previous signature.
"Round" in a message means the round number as an unsigned 64-bit big-endian integer.

For `quicknet`, the scheme is `bls-unchained-g1-rfc9380`:

* The signature is a point of G1 on the curve BLS12-381 in compressed form, 48 bytes.
* The public key is a point of G2 in compressed form, 96 bytes.
* The message is SHA-256(round).
* The DST is `BLS_SIG_BLS12381G1_XMD:SHA-256_SSWU_RO_NUL_`.

For `default`, the scheme is `pedersen-bls-chained`:

* The signature is a point of G2 on the curve BLS12-381 in compressed form, 96 bytes.
* The public key is a point of G1 in compressed form, 48 bytes.
* The message is SHA-256 of the previous signature followed by the round.
* The DST is `BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_NUL_`.

For `evmnet`, the scheme is `bls-bn254-unchained-on-g1`:

* The signature is a point of G1 on the curve BN254, in uncompressed form.
  It has 64 bytes: the x coordinate and then the y coordinate, each 32 bytes big-endian.
* The public key is a point of G2 on BN254 in uncompressed form, 128 bytes: four coordinates of 32 bytes each, big-endian.
  The order is x imaginary part, x real part, y imaginary part, y real part, which is the order of the EVM precompiles.
* The message is Keccak-256(round), with the original Keccak padding and not the padding of SHA3-256.
* The DST is `BLS_SIG_BN254G1_XMD:KECCAK-256_SVDW_RO_NUL_`.
  No IETF document defines this hash-to-curve suite, so an implementer needs the drand implementation as a reference.
* The hash H derives two field elements with expand_message_xmd ({{Section 5.3.1 of RFC9380}}), with Keccak-256 as the hash function.
  It maps each element to a point with the Shallue-van de Woestijne method ({{Section 6.6.1 of RFC9380}}) and adds the two points.
  The reference code is in {{DRAND-KYBER}}.

The signature is valid when the pairing check of the BLS scheme holds.
The check is e(signature, generator of the key group) = e(H(message), public key).
H is the hash to the signature group under the DST.
These values come from the drand implementation {{DRAND-CRYPTO}}.
A client MUST NOT write its own pairing code when a reviewed library for the scheme exists.

## Negative Response {#negative-response}

A negative response to a query for round `n` is an NXDOMAIN or a NODATA response.
The client reports one of two outcomes.
The clock of the client selects the outcome only.
It has no effect on whether a round passes.

* The grace time of a chain is the larger of twice its period and 10 seconds.
* If the clock is before the round time of `n` plus the grace time, the client reports "not published".
* Otherwise the client reports "withheld".

A caller of a pinned round can wait and ask again after "not published".
A caller sees "withheld" as a loss of availability.
Another gateway or another resolver can answer.

## Read the Latest Round

A client MAY query the latest name.
It does steps 2 to 4 of the pinned procedure for `latest.<chain>.<zone>`.
A negative response is reported as "withheld", and the client reads no clock for it.
The client tests each record as in {{check-record}}, with no requested number.

A latest round is stale when the current round is more than 2 above its round number.
A client MUST report a stale result to its caller together with the verified round, and MUST NOT hide the staleness.
A round number above the current round passes, because a valid signature proves that the round exists.
A draw MUST NOT use the latest name.

# Chain Parameters {#chain-parameters}

A client holds these values.
They come from the drand `/info` endpoint of each chain.
A client MUST verify a round with the public key of the chain that it selected.
The client MUST obtain that key from a source that it trusts, and MUST NOT obtain it from a gateway or from any DNS answer.
A gateway MUST verify with the public key below.

A hexadecimal value can span several lines.
White space inside a value is not part of the value.

## `quicknet`

* Chain hash: 52db9ba70e0cc0f6eaf7803dd07447a1f5477735fd3f661792ba94600c84e971
* Scheme: bls-unchained-g1-rfc9380
* Public key: 83cf0f2896adee7eb8b5f01fcad3912212c437e0073e911f
  b90022d3e760183c8c4b450b6a0a6c3ac6a5776a2d106451
  0d1fec758c921cc22b0e17e63aaf4bcb5ed66304de9cf809
  bd274ca73bab4af5a6e9c76a4bc09e76eae8991ef5ece45a
* Genesis: 1692803367
* Period: 3 seconds
* Signature length: 48 bytes
* Previous signature length: 0 bytes

## `default`

* Chain hash: 8990e7a9aaed2ffed73dbd7092123d6f289930540d7651336225dc172e51b2ce
* Scheme: pedersen-bls-chained
* Public key: 868f005eb8e6e4ca0a47c8a77ceaa5309a47978a7c71bc5c
  ce96366b5d7a569937c529eeda66c7293784a9402801af31
* Genesis: 1595431050
* Period: 30 seconds
* Signature length: 96 bytes
* Previous signature length: 96 bytes

## `evmnet`

* Chain hash: 04f1e9062b8a81f848fded9c12306733282b2727ecced50032187751166ec8c3
* Scheme: bls-bn254-unchained-on-g1
* Public key: 07e1d1d335df83fa98462005690372c643340060d205306a
  9aa8106b6bd0b3820557ec32c2ad488e4d4f6008f89a346f
  18492092ccc0d594610de2732c8b808f0095685ae3a85ba2
  43747b1b2f426049010f6b73a0cf1d389351d5aaaa1047f6
  297d3a4f9749b33eb2d904c9d9ebf17224150ddd7abd7567
  a9bec6c74480ee0b
* Genesis: 1727521075
* Period: 3 seconds
* Signature length: 64 bytes
* Previous signature length: 0 bytes

## Historical Note

A chain can end.
A retired chain stays in this document in a historical note.
The published rounds of a retired chain stay valid, so the note keeps the parameters that verify them.
A gateway stops serving a retired chain after a registry change, and a name under it then returns NXDOMAIN.
No chain is retired in this version.

# Security Considerations {#security}

## Security Property

A client accepts a round only if the BLS signature verifies against a public key that the client holds ({{client-procedure}}).
The gateway, the resolver and the network are therefore trusted for availability only, and not for integrity.
Two parties are trusted: the drand threshold of nodes that signs each round, and the registry of public keys in the client.
A client that follows this document cannot accept a forged round from any other party.

## Forged Rounds

A gateway can return a forged signature, or a valid round at the wrong round name.
A forged signature fails verification.
A valid round with another round number fails the comparison of the round number in the record body with the requested number ({{check-record}}).
In both cases the client reports that no record verifies, and it returns no round.

## Withheld Rounds

A gateway, a resolver or any party on the path can withhold a round.
A client sees a negative answer, a SERVFAIL, or a timeout.
No signature can prove that a round was withheld, because a missing answer carries no signature.
A client therefore reports "withheld" when a negative answer arrives after the round time plus the grace time ({{negative-response}}).
A client reports a transport failure for a SERVFAIL or a timeout.
These outcomes are a loss of availability and nothing more.
The caller can ask another resolver or another gateway.
This document gives no way to force an answer.

## Selection among Latest Rounds

A gateway can select among recent rounds at a latest name.
Every round that the gateway returns is a real round with a valid signature.
A client returns the round, and it also reports a stale result when the round is stale ({{client-procedure}}).
A gateway can therefore choose the value that a client sees without a stale result, within the last 3 rounds.
A pinned round has exactly one valid value, so a gateway has no such choice.
A draw MUST use a pinned round and MUST NOT use a latest name.
The latest name has no cryptographic property that a round name lacks.

## Pinned Rounds in a Draw

A draw is fair only when the organizer pins the round number before the round time.
An organizer that takes the round number after the round time can already read that round, and can select a favorable number.
To pin a round for a draw, an organizer does these steps in order:

1. Select the draw time.
2. Compute the lowest round number with a round time at or after the draw time.
3. Announce that round number before its round time.
4. Read the round after its round time.

## Spoofed Records in a Resolver Cache

An off-path attacker can try to place a false record in the cache of a resolver.
A false record fails verification, so the client rejects it and reports that no record verifies.
The false record can stay in the cache until its TTL ends.
In that time the client cannot obtain the true round from that resolver.
The same applies to a false negative answer: the client reports a negative outcome until its TTL ends.
The caller can select another resolver.
This document does not require DNSSEC, because the signature of the round already gives integrity.
DNSSEC would add availability protection only.

A bad upstream can send junk to a gateway.
A gateway verifies a round before it keeps or serves the round, so junk never reaches a resolver cache through the gateway ({{answer-rules}}).

## Client Clock

A client reads a clock for two purposes.
The clock selects between "not published" and "withheld" in a negative response, and it decides when a latest round is stale.
The clock has no effect on whether a signature verifies.
An attacker that moves the clock of a client can therefore turn "withheld" into "not published", or hide a stale latest round.
The attacker cannot make a client accept a false round.
A client that needs a correct outcome SHOULD keep an accurate clock.
A client MUST NOT read a clock when it verifies a signature.

## Early Reads

A round is public at its round time, from every drand relay.
A gateway has no advantage over any other reader.
A gateway cannot read a round before its round time.

## Reflection and Floods

A reflection attacker can send queries to a gateway with a spoofed source address.
A UDP answer is larger than the query, so the gateway amplifies the traffic to the victim.
The answer for `latest.default.b.example` with a query of 42 bytes and no OPT record is 324 bytes, a gain of about 7.7.
The query for `latest.quicknet.b.example` has 43 bytes and the answer has 132 bytes, a gain of about 3.07.
The query for `latest.evmnet.b.example` has 41 bytes and the answer has 154 bytes, a gain of about 3.76.
The measured gain is therefore from 3.07 to 7.7 for these three names.
A gateway SHOULD limit its UDP responses for each source prefix, as {{answer-rules}} specifies.
The TC bit makes a real client retry over TCP, where no spoofing is possible.

A query flood for random old round numbers needs an on-demand fetch from an upstream.
A gateway SHOULD keep a budget for such fetches and answer the excess with SERVFAIL, so that the store and the upstreams stay within limits.
A query flood for random future round numbers costs arithmetic only, because the gateway sends no upstream request for them.

## Query Privacy

The name of a query holds the chain and the round number.
A resolver, a gateway and any observer on the path can read it.
They learn which rounds a client reads, and so which draw a client follows.
This document gives no protection against that observation.
A client that needs privacy can send its queries through a channel that hides them, such as a resolver with encrypted transport.
Such a channel is outside this document.

## Trust in drand

Every client of every chain accepts any value that the drand threshold signs.
If a threshold of nodes is compromised, they can sign any value and all clients accept it.
This is an assumption of drand, and this document does not mitigate it.

## What This Document Does Not Claim

This document does not claim any of these:

* A round is available.
  Any party on the path can withhold it.
* A resolver respects a TTL.
* The reader of a round stays hidden.
* The design resists an attack on drand itself.

# IANA Considerations {#iana}

This document has no IANA actions.

--- back

# Test Vectors {#vectors}

This appendix holds one vector for each chain.
Each vector is a real round, with the record that a gateway serves for it.
A vector lets an implementer test a decoder and a gateway.

## Format of a Vector {#vector-format}

A vector is a fenced block with the info string `vector`.
A rendered text shows no fence.
In a rendered text, a vector starts at its `beacon_id` line and ends with the last continuation line of its `randomness` value.
A page header, a page footer or a blank line inside a vector is not part of the vector.
A block holds lines in the form `key: value`.
The keys come in this order:

| Key | Meaning |
|---|---|
| `beacon_id` | The beacon ID of the chain. |
| `round` | The round number, in decimal. |
| `signature` | The signature, in hexadecimal. |
| `previous_signature` | The previous signature, in hexadecimal. The value is empty for a chain with no previous signature. |
| `txt` | One character-string of the round record, in base64 text. |
| `randomness` | The SHA-256 hash of the signature, in hexadecimal. |

A line that starts with white space continues the value of the line above it.
A continuation line is indented by two more spaces than a key line.
A line break and the white space that starts a continuation line are not part of a value.
To read a value, join the first line and every continuation line, with no separator.
A key with nothing after the colon has an empty value, except `txt`, whose value is never empty.

The key `txt` occurs once for each character-string, in the order of the record.
A decoder concatenates the values of all `txt` keys of one vector, as {{record-body}} specifies.
The vector of `default` has two character-strings, of 255 and 13 characters, so it has two `txt` lines.

A test suite MAY hold the same fields in another container, for example a JSON object with the `txt` values as an array of strings.
The values in the container equal the values in this appendix.

All three vectors are for round 1000.

## `quicknet` {#vec-quicknet}

~~~ vector
beacon_id: quicknet
round: 1000
signature: b44679b9a59af2ec876b1a6b1ad52ea9b1615fc3982b1957
  6350f93447cb1125e342b73a8dd2bacbe47e4b6b63ed5e39
previous_signature:
txt: AQAAAAAAAAPotEZ5uaWa8uyHaxprGtUuqbFhX8OYKxlXY1D5
  NEfLESXjQrc6jdK6y+R+S2tj7V45
randomness: fe290beca10872ef2fb164d2aa4442de4566183ec51c56ff
  3cd603d930e54fdd
~~~

## `default` {#vec-default}

~~~ vector
beacon_id: default
round: 1000
signature: 99bf96de133c3d3937293cfca10c8152b18ab2d034ccecf1
  15658db324d2edc00a16a2044cd04a8a38e2a307e5ecff35
  11315be8d282079faf24098f283e0ed2c199663b334d2e84
  c55c032fe469b212c5c2087ebb83a5b25155c3283f5b79ac
previous_signature: af0d93299a363735fe847f5ea241442c65843dc1bd3a7b79
  646b3b10072e908bf034d35cd69d378e3341f139100cd4cd
  03030399864ef8803a5a4f5e64fccc20bbae36d1ca22a6dd
  c43d2630c41105e90598fab11e5c7456df3925d4b577b113
txt: AQAAAAAAAAPomb+W3hM8PTk3KTz8oQyBUrGKstA0zOzxFWWN
  syTS7cAKFqIETNBKijjiowfl7P81ETFb6NKCB5+vJAmPKD4O
  0sGZZjszTS6ExVwDL+RpshLFwgh+u4OlslFVwyg/W3msrw2T
  KZo2NzX+hH9eokFELGWEPcG9Ont5ZGs7EAcukIvwNNNc1p03
  jjNB8TkQDNTNAwMDmYZO+IA6Wk9eZPzMILuuNtHKIqbdxD0m
  MMQRBekFmPqxHlx
txt: 0Vt85JdS1d7ET
randomness: a40d3e0e7e3c71f28b7da2fd339f47f0bcf10910309f5253
  d7c323ec8cea3212
~~~

## `evmnet` {#vec-evmnet}

~~~ vector
beacon_id: evmnet
round: 1000
signature: 06fd5996329504d3a56b482d9222bf7205857d0a9559ddd2
  16ca31a286f6a8cc0a120f021aac2f13553fb164f62bc3a5
  ca32c76dea88a777b39bcf3cac5fdbd6
previous_signature:
txt: AQAAAAAAAAPoBv1ZljKVBNOla0gtkiK/cgWFfQqVWd3SFsox
  oob2qMwKEg8CGqwvE1U/sWT2K8OlyjLHbeqIp3ezm888rF/b
  1g==
randomness: 0e6745667465a6f9dce5d5f994656955080be14c469ff17f
  c4fc588c925a8504
~~~
