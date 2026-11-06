# entropy53

entropy53 serves drand rounds over DNS.
A gateway answers TXT queries on a delegated zone.
A client verifies each round with a BLS signature against a public key in the registry, so the gateway is not trusted.

## Pin a round

To run a draw, pin a round before its round time.

1. Select the draw time, with a zone offset.
2. Get the round number for that time:

```
entropy53 round-at quicknet 2026-12-01T18:00:00Z
```

The command prints `34448212`.

3. Announce the round number before its round time.
4. After the round time, read and verify the round:

```
entropy53 get quicknet 34448212 --zone beacon.example
```

The `latest` name is for convenience only, and it has no security gain.
A gateway can select a round at that name.
A draw must not use `latest`.
The rules for a draw are in [the threat model](docs/specs/pages/threat-model.md).

The operator guide is in [docs/operator-guide.md](docs/operator-guide.md).
The `default` chain has a limit for round 1, which is in [Known limits](docs/operator-guide.md#known-limits).

## Build

Build both binaries with `go build -o . ./cmd/...`.
Each binary prints the build version with `--version`.
A build without a version prints `dev`.
To set the version, build with:

```
go build -ldflags "-X github.com/Crank-Git/entropy53/internal/version.Version=v1.0.0" ./cmd/entropy53d
```

## License

The license is `Apache-2.0 OR MIT`.
The texts are in `LICENSE-APACHE` and `LICENSE-MIT`.
