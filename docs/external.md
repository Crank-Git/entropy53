# External facts

This file holds facts that live outside the code. It holds names and pointers only. It never holds a secret.

## Environment variables

The project has none. The gateway uses command-line flags. See `docs/specs/spec.md` § Environments & config.

## drand upstreams

- Docs: https://docs.drand.love/developer/
- Default upstreams: `api.drand.sh`, `api2.drand.sh`, `api3.drand.sh`, `drand.cloudflare.com`.
- The chain parameters and their retrieval date are in `docs/specs/pages/evidence.md`.

## GitHub

- Repository: `Crank-Git/entropy53`, public. TODO(Epic 0): Create the repository.
- CI and releases: GitHub Actions. The workflows use the automatic `GITHUB_TOKEN`.

## Zone delegation

An operator needs a zone with NS records at the parent. The project has no test zone. See `docs/operator-guide.md`. TODO(Epic 5): Write the guide.

## Internet-Draft

- Tools: `kramdown-rfc` and `xml2rfc`. See `docs/specs/features/07-internet-draft.md`.
- The maintainer alone submits to the IETF datatracker.

## Support

Users file support requests as GitHub issues on `Crank-Git/entropy53`.
