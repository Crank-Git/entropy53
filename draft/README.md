# Internet-Draft source

This directory holds the Internet-Draft source.
The build output (`*.xml`, `*.txt`, `*.html`) is not committed.

## Build

CI builds the draft in the `draft` job of `.github/workflows/ci.yml`. The job keeps the text file and the HTML file as an artifact.
The tool versions are pinned in that file: kramdown-rfc 1.7.43 and xml2rfc 3.35.0.
To build the draft on your machine, run these commands from the repository root.

```
kramdown-rfc draft/draft-crank-drand-dns.md > draft/draft-crank-drand-dns.xml
xml2rfc draft/draft-crank-drand-dns.xml --text --html --path draft
```

## Vector check

`go test ./draft/...` compares the vectors in the appendix with `testdata/vectors.json`.
The test needs no Ruby tool, no Python tool, no Docker and no network.
