# Operator guide

This guide shows an operator how to run a gateway for a zone. The examples use the zone `beacon.example`, the NS hosts `ns1.example.net` and `ns2.example.net`, and the SOA RNAME `hostmaster.example.net.`. Replace each with your own value.

## Install the binaries

Each release holds the binaries `entropy53d` and `entropy53` for `linux/amd64`, `linux/arm64`, `darwin/amd64` and `darwin/arm64`. The file `SHA256SUMS` lists their checksums.

Download one binary of each kind and `SHA256SUMS` from the release page. Replace `v0.1.0` with the release that you install.

```sh
curl -fLO https://github.com/Crank-Git/entropy53/releases/download/v0.1.0/entropy53d-linux-amd64
curl -fLO https://github.com/Crank-Git/entropy53/releases/download/v0.1.0/entropy53-linux-amd64
curl -fLO https://github.com/Crank-Git/entropy53/releases/download/v0.1.0/SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

The last command prints `OK` for each file that you downloaded. If it prints `FAILED`, delete the file. Then download it again.

The file names carry the platform. Rename the files, make them executable and put the directory on your `PATH`. The later commands use the names `entropy53d` and `entropy53`.

```sh
mv entropy53d-linux-amd64 entropy53d
mv entropy53-linux-amd64 entropy53
chmod +x entropy53d entropy53
export PATH="$PWD:$PATH"
```

The `export` line lasts for the current shell. Add the directory to the `PATH` in your shell profile to keep it.

To build from source, use Go 1.27 in a clone of the repository:

```sh
go build -o entropy53d ./cmd/entropy53d
go build -o entropy53 ./cmd/entropy53
export PATH="$PWD:$PATH"
```

## First read on loopback

This section proves that the binaries work, before you change any DNS setting. It needs no delegated zone and no root access. The gateway fetches round 1000 of `quicknet` from the public upstreams.

Start the gateway on an unprivileged port in the first terminal. Wait for the log line `gateway listening`.

```sh
entropy53d --zone beacon.example --ns ns1.example.net --soa-rname hostmaster.example.net. --listen 127.0.0.1:15353
```

Query the gateway with `dig` in a second terminal. The answer holds one TXT record.

```sh
dig -p 15353 @127.0.0.1 TXT round.1000.quicknet.beacon.example
```

Read and verify the round with the CLI. The output ends with `verified:   yes`.

```sh
entropy53 get quicknet 1000 --zone beacon.example --resolver 127.0.0.1:15353
```

The randomness of this round is `fe290beca10872ef2fb164d2aa4442de4566183ec51c56ff3cd603d930e54fdd`. The CLI computes this value from the signature. It does not take the value from the gateway.

Stop the gateway with SIGTERM or with Ctrl-C. It exits with code 0.

## Flags of entropy53d

| Flag | Default | Meaning |
|---|---|---|
| `--version` | off | Print the build version and exit. |
| `--help` | off | Print the usage and exit. |
| `--zone` | none | The delegated zone. Required. |
| `--ns` | none | An NS host name of the zone, outside the zone. Required. Repeat the flag for each host. |
| `--soa-rname` | none | The SOA RNAME, for example `hostmaster.example.net.`. Required. |
| `--listen` | `:53` | The UDP and TCP listen address. |
| `--chains` | `quicknet,default,evmnet` | The chains to serve, separated by commas. |
| `--upstream` | the four public upstreams | An upstream base URL. Repeat the flag for each upstream. |
| `--udp-rate` | `2000` | The UDP answers per second for one source prefix. The minimum is 1. |
| `--upstream-rate` | `20` | The on-demand fetches per second for the gateway. The value `0` is accepted. Then the gateway makes no on-demand fetch, and a round that the store does not hold returns SERVFAIL. |

The default upstreams are `https://api.drand.sh`, `https://api2.drand.sh`, `https://api3.drand.sh` and `https://drand.cloudflare.com`. The gateway tries them in order and keeps the first round that verifies.

An `--ns` host must lie outside the zone. The gateway refuses to start with an NS host inside the zone.

## Exit codes of entropy53d

| Code | Event |
|---|---|
| 0 | `--version`, `--help`, SIGTERM or SIGINT. |
| 1 | A startup failure or a failure of a UDP or TCP listener of the gateway. The gateway writes one JSON log line to standard output. |
| 2 | A bad flag. The usage goes to standard error. |

## Bind port 53 without root

Port 53 is a privileged port. A process that binds it needs the capability `CAP_NET_BIND_SERVICE`, or the host must lower the port limit. The capability allows a bind to a port below 1024 ([capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html)). Run the gateway as an unprivileged user. Use one of these methods.

Method 1: grant the capability to the systemd service. The unit in the next section sets `AmbientCapabilities=CAP_NET_BIND_SERVICE`. An ambient capability lets a process that runs as an unprivileged user keep the capability ([systemd.exec(5)](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html)).

Method 2: grant the capability to the binary.

```sh
sudo setcap cap_net_bind_service=+ep /usr/local/bin/entropy53d
```

The kernel stores a file capability in the extended attribute `security.capability` ([capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html)). A new copy of the binary has no such attribute. Run `setcap` again after each update of the binary. The command is described in [setcap(8)](https://man7.org/linux/man-pages/man8/setcap.8.html).

Method 3: lower the privileged-port limit for the whole host. Every process then binds ports from 53.

```sh
sudo sysctl net.ipv4.ip_unprivileged_port_start=53
```

The default of this setting is 1024 ([kernel documentation](https://www.kernel.org/doc/html/latest/networking/ip-sysctl.html)). The command changes the running kernel only, so the setting returns to its default after a reboot. To keep it, write the line `net.ipv4.ip_unprivileged_port_start=53` to a file in `/etc/sysctl.d/` ([sysctl(8)](https://man7.org/linux/man-pages/man8/sysctl.8.html)).

If `systemd-resolved` runs on the host, its stub listener holds `127.0.0.53:53` ([systemd-resolved(8)](https://www.freedesktop.org/software/systemd/man/latest/systemd-resolved.service.html)). Then use `--listen` with the public address of the host, for example `--listen 192.0.2.10:53`.

What a test proved: in a container with `net.ipv4.ip_unprivileged_port_start=1024`, a non-root user got `bind: permission denied`. After `setcap cap_net_bind_service=+ep`, the gateway bound port 53 and answered an SOA query. This covers Method 2. No test ran the unit of the next section under a real systemd. A check with `systemd-analyze verify` found no error in the unit file, and the setting `AmbientCapabilities` rests on the systemd documentation. No test ran Method 3.

## Run the gateway with systemd

Create the user and install the binary.

```sh
sudo useradd --system --shell /usr/sbin/nologin entropy53
sudo install -m 755 entropy53d /usr/local/bin/entropy53d
```

Write `/etc/systemd/system/entropy53d.service`:

```ini
[Unit]
Description=entropy53 gateway
After=network-online.target
Wants=network-online.target

[Service]
User=entropy53
ExecStart=/usr/local/bin/entropy53d --zone beacon.example --ns ns1.example.net --ns ns2.example.net --soa-rname hostmaster.example.net. --listen :53
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Start the service and read its log.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now entropy53d
journalctl -u entropy53d
```

The gateway needs outbound HTTPS to the upstreams and inbound UDP and TCP on port 53. It keeps all rounds in memory, so it needs no data directory.

## Delegate the zone

Delegation makes the parent zone send queries for your zone to your gateway. The steps in this section change DNS records at a registrar or at a parent zone. Make these changes only with the approval of the owner of the parent zone.

1. Choose NS host names outside the zone, for example `ns1.example.net`. A host inside `beacon.example` is not allowed, because the zone cannot answer a query that locates its own servers.
2. In the zone of each NS host name, create an A record, an AAAA record or both. The address is the address of the machine that runs the gateway.
3. Add one NS record for each host to the parent zone `example`:

   ```
   beacon.example.    IN  NS  ns1.example.net.
   beacon.example.    IN  NS  ns2.example.net.
   ```

4. Start the gateway with one `--ns` flag for each NS record. The gateway publishes these hosts as the NS set of the zone apex.
5. Wait for the TTL of the old NS records at the parent to end.

RFC 2606 reserves the names `beacon.example` and `example.net` for documentation. They never resolve on the public internet. Use your own names.

## Check the gateway

Query the gateway directly at its address. Use `@` and the address of the gateway.

```sh
dig @192.0.2.10 SOA beacon.example
dig @192.0.2.10 NS beacon.example
dig @192.0.2.10 TXT round.1000.quicknet.beacon.example
```

Each answer has the `aa` flag. The SOA answer and the NS answer hold the values from your flags.

Then query through a resolver. The resolver follows the delegation, so this check proves the records at the parent.

```sh
dig TXT round.1000.quicknet.beacon.example
entropy53 get quicknet 1000 --zone beacon.example
```

Without `--resolver`, the CLI uses the system resolver. Add `--resolver <host:port>` to select another resolver.

Exit codes of `entropy53 get`:

| Code | Event |
|---|---|
| 0 | The CLI verified the round and printed it. |
| 2 | A usage error. |
| 3 | The round is not published. Its round time is in the future. |
| 4 | The round is withheld. Its round time plus the grace time has passed. |
| 5 | No record verifies. The gateway or the resolver returned a bad record. |
| 6 | The `latest` round is stale. The CLI still prints it. |
| 7 | A transport failure, for example no answer or SERVFAIL. |

## Log lines

The gateway writes one JSON object per line to standard output. Each object has the fields `time`, `level` and `msg`, and then the fields in the tables below. An `error` field holds the text of the failure.

Startup and shutdown:

| Message | Level | Meaning |
|---|---|---|
| `invalid flag value` | ERROR | A flag value is not valid, for example `--udp-rate 0` or an unknown chain in `--chains`. The exit code is 1. |
| `invalid zone settings` | ERROR | The zone, the NS hosts or the SOA RNAME is missing or malformed, or an NS host lies inside the zone. The exit code is 1. |
| `cannot start the gateway` | ERROR | The gateway cannot bind the listen address. The exit code is 1. |
| `gateway listening` | INFO | The gateway accepts queries. The field `addr` holds the bound address and the field `zone` holds the zone. |
| `the gateway stopped with an error` | ERROR | A UDP or TCP listener failed while the gateway ran. The exit code is 1. |
| `DNS servers did not finish within the close limit` | WARN | After a stop signal, the listed listeners did not finish in time. The gateway exits anyway. |

Answers:

| Message | Level | Meaning |
|---|---|---|
| `write of a DNS answer failed` | WARN | The gateway could not send an answer to the field `remote`. One lost answer is normal. A long run of them means a network fault. |
| `latest record encode failed` | ERROR | The gateway could not build the record for a `latest` name. The client gets SERVFAIL. This message is a bug. File an issue. |
| `round record encode failed` | ERROR | The gateway could not build the record for a round name. The client gets SERVFAIL. This message is a bug. File an issue. |

Upstreams and follower:

| Message | Level | Meaning |
|---|---|---|
| `upstream failed` | WARN | One upstream gave an error, a bad HTTP status or no answer. The gateway tries the next upstream. |
| `round discarded` | WARN | An upstream gave a round that failed a check: wrong round number, wrong length of the previous signature or a bad signature. The gateway tries the next upstream. |
| `follower cannot compute the next round` | WARN | The follower could not compute the next round time of the chain. It waits one period and tries again. |
| `follower behind` | WARN | The follower could not fetch the current round of a chain after one retry. It logs this message once, until it recovers. |
| `follower recovered` | INFO | The follower fetched the current round again after a `follower behind` message. |

A single `upstream failed` line is normal, because an upstream can fail for a moment. If every upstream fails, `follower behind` follows. Then check the outbound HTTPS access of the host.

## Known limits

The gateway has these limits. Read them before you rely on a gateway for a draw.

- Round 1 of the `default` chain does not work. The previous signature of that round has 32 bytes, and the record layout of the chain holds 96 bytes. The gateway logs `round discarded`, answers SERVFAIL, and `entropy53 get default 1` exits with code 7. Issue #46 tracks the decision.
- A resolver can delay a round that you pin before its round time. The gateway answers NODATA before the round time. A resolver with `cache-min-negative-ttl` set above the TTL of that answer keeps the NODATA for that time. We measured Unbound 1.26.2 with `cache-min-negative-ttl: 30`. A first query 4 seconds before the round time got NODATA. The record arrived 26 seconds after the round time, which is 30 seconds after the first query. The default Unbound setting is 0 and adds no delay.

## Limits of the threat model

The client library verifies each round against a public key in the registry. The project therefore does not trust the gateway, the resolver or the network for integrity. It relies on them only for availability. The full table is in [the threat model](specs/pages/threat-model.md).

The project does not claim these properties:

- A round is available. Any party on the path can withhold a round.
- A resolver respects a TTL.
- The project hides which rounds a client reads. A resolver or an observer sees the query names.
- The project protects against an attack on drand itself. If the drand threshold of nodes signs a false value, all clients accept it.

A gateway can select among recent rounds at a `latest` name, and a client accepts any non-stale round there. Therefore a draw must pin a round:

1. Select the draw time.
2. Run `entropy53 round-at` to get a round number.
3. Announce the round number before its round time.
4. Read the round with `entropy53 get` after its round time.

A draw that takes the round number after the round time gives no fairness. A draw that uses `latest` gives no fairness.

DNSSEC is out of scope for version 1. An off-path attacker can put a false record in a resolver cache. The client library then returns `ErrInvalid` until the TTL ends. The caller can select another resolver.
