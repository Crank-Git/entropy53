---
name: refresh-vectors
description: Rewrite testdata/vectors.json from real upstream rounds. Use when a chain is added or when the record format version changes.
allowed-tools: Bash, Read
---

# Refresh the vectors

A round is immutable, so the same round number always gives the same result. A refresh is needed only for a new chain, a new vector or a new format version.
A new vector or a new chain needs a code edit in `scripts/refreshvectors/main.go` first. A refresh rewrites the whole file.

1. Run the refresh. This step contacts a real upstream.

   ```
   make refresh-vectors
   ```

2. Review the change.

   ```
   git diff testdata/vectors.json
   ```

3. If a value for an existing round changed and the format version did not change, stop and report the result. That result means that a defect or an upstream fault exists.

4. Run the record tests.

   ```
   go test ./record/...
   ```

5. After issue #37 lands, update the test vector appendix in `draft/draft-crank-drand-dns.md`, then run the comparison.

   ```
   go test ./draft/...
   ```
