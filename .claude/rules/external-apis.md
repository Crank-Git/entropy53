---
paths:
  - "**/*.go"
  - "test/interop/**"
  - "draft/**"
  - ".github/workflows/**"
  - "docs/specs/**/*.md"
---

# External APIs — read the docs, never assume

Before you write a call against any item below, confirm the operation name, the required parameters, the response shape and the error cases. Use the tool's own interface first (`go doc`, `<tool> --help`), then its documentation. Cite the URL or the command, and the version, in the PR body.

| Item | Version pinned | Documentation |
|---|---|---|
| drand HTTP API | v1 paths | https://docs.drand.love/developer/http-api |
| `github.com/drand/drand/v2/crypto` | v2.1.8 | https://pkg.go.dev/github.com/drand/drand/v2/crypto |
| `github.com/miekg/dns` | v1.1.73 | https://pkg.go.dev/github.com/miekg/dns |
| Unbound | pinned by image digest | https://unbound.docs.nlnetlabs.nl/en/latest/manpages/unbound.conf.html |
| BIND | pinned by image digest | BIND Administrator Reference Manual at that version |
| golangci-lint | v2 | https://golangci-lint.run/docs/linters/ |
| kramdown-rfc and xml2rfc | pinned in CI | https://github.com/cabo/kramdown-rfc |
| GitHub Actions and `gh` | current | https://docs.github.com/actions, `gh release create --help` |

`docs/specs/pages/evidence.md` records what the planner verified and what it did not. HTTP 425 and `public/0` come from observation only, so code must not depend on either.

If the documentation contradicts the spec, stop and report `needs-feedback`.

Anything that creates, deletes or changes an outside resource needs user confirmation first. That includes a DNS delegation, a repository setting, a release tag and an IETF submission. Workers never submit the Internet-Draft.
