# entropy53

entropy53 serves drand rounds over DNS.
A gateway answers TXT queries on a delegated zone.
A client verifies each round with a BLS signature against a public key in the registry, so the gateway is not trusted.

## Build

Build both binaries with `go build ./...`.
Each binary prints the build version with `--version`.
A build without a version prints `dev`.
To set the version, build with:

```
go build -ldflags "-X github.com/Crank-Git/entropy53/internal/version.Version=v1.0.0" ./cmd/entropy53d
```

## License

The license is `Apache-2.0 OR MIT`.
The texts are in `LICENSE-APACHE` and `LICENSE-MIT`.
