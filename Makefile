.PHONY: refresh-vectors

# Issue #15 adds the fetch procedure. Until then the target fails so that nobody mistakes the empty file for fresh data.
refresh-vectors:
	@echo "refresh-vectors: not implemented. Issue #15 adds the fetch procedure." >&2
	@exit 1
