//go:build interop

package interop

import (
	"os"
	"strings"
	"testing"
)

// The test reads the file as text, because the module has no direct YAML dependency.
func TestInteropWorkflowRunsOnPullRequestsIntoDevAndMain(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/interop.yml")
	if err != nil {
		t.Fatalf("read the workflow: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"pull_request:\n    branches: [dev, main]",
		"runs-on: ubuntu-latest",
		"timeout-minutes: 15",
		"run: go test -tags interop ./test/interop/... ./internal/gateway/...",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("workflow lacks %q", want)
		}
	}
	for _, unwanted := range []string{"schedule:", "push:", "workflow_dispatch:"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("workflow holds %q, want a run on pull requests into dev and main only", unwanted)
		}
	}
}
