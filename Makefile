.PHONY: refresh-vectors

# This target contacts a real upstream. No test runs it.
refresh-vectors:
	go run ./scripts/refreshvectors
