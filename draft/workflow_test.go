package draft

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The draft tests must run with no outside tool, so no test file may start a process or open a network connection.
func TestDraftTestsImportNoExec(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("find the test files: %v", err)
	}
	banned := map[string]bool{"os/exec": true, "net": true, "net/http": true}
	fset := token.NewFileSet()
	for _, f := range files {
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range parsed.Imports {
			if path := strings.Trim(imp.Path.Value, `"`); banned[path] {
				t.Errorf("%s imports %s", f, path)
			}
		}
	}
}

// The test reads the file as text, because the module has no direct YAML dependency.
func readCI(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read the workflow: %v", err)
	}
	return string(data)
}

func TestCIJobBuildsTextAndHTML(t *testing.T) {
	text := readCI(t)
	for _, want := range []string{"\n  draft:\n", "kramdown-rfc draft/draft-crank-drand-dns.md", "--text", "--html", "actions/upload-artifact@"} {
		if !strings.Contains(text, want) {
			t.Errorf("workflow lacks %q", want)
		}
	}
}

func TestCIJobPinsBothToolsToExactVersions(t *testing.T) {
	text := readCI(t)
	for name, pattern := range map[string]string{
		"kramdown-rfc": `gem install kramdown-rfc -v \d+\.\d+\.\d+ `,
		"xml2rfc":      `xml2rfc==\d+\.\d+\.\d+`,
	} {
		if !regexp.MustCompile(pattern).MatchString(text) {
			t.Errorf("workflow does not pin %s to an exact version", name)
		}
	}
}
