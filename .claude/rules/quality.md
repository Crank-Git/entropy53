---
paths:
  - "**/*.go"
---

# Quality rules — reviewed per rule, pass or fail

- No error check that only returns the same error with no added context at a package boundary.
- No error that is logged and then ignored.
- No nil check against a state that the types or the constructor already exclude.
- No interface with a single implementation, unless a test needs the second one.
- No abstraction with a single caller. Inline it until a second caller exists.
- No flag or option for a value that nothing varies.
- No dead code and no commented-out code.
- No comment that restates the code.
- No `panic` in a DNS handler or in the client library.
- No test that contacts a real upstream, other than `make refresh-vectors`.
- No test that sleeps for a time that a fake clock can replace. Interop tests are exempt.
