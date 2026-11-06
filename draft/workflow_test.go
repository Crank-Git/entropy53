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

var nextJobRE = regexp.MustCompile(`\n  \S[^\n]*:\n`)

// draftJob returns the text of the draft job with the full-line comments removed,
// so that a word inside a comment cannot satisfy a check.
func draftJob(t *testing.T) string {
	t.Helper()
	_, rest, ok := strings.Cut(readCI(t), "\n  draft:\n")
	if !ok {
		t.Fatal("workflow has no draft job")
	}
	if loc := nextJobRE.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]+1]
	}
	var kept []string
	for _, line := range strings.Split(rest, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// draftSteps returns the text of each step of the draft job.
func draftSteps(t *testing.T) []string {
	t.Helper()
	var steps []string
	for _, line := range strings.Split(draftJob(t), "\n") {
		if strings.HasPrefix(line, "      - ") {
			steps = append(steps, line)
		} else if len(steps) > 0 && strings.HasPrefix(line, "        ") {
			steps[len(steps)-1] += "\n" + line
		}
	}
	return steps
}

// runCommands returns the command of each step that has a run key, in order, with the index of its step.
func runCommands(t *testing.T) (cmds []string, index []int) {
	t.Helper()
	for i, s := range draftSteps(t) {
		if cmd, ok := strings.CutPrefix(s, "      - run: "); ok {
			cmds, index = append(cmds, strings.Trim(cmd, `"`)), append(index, i)
		}
	}
	return cmds, index
}

// findRun returns the index in the step list of the first run command that starts with prefix, or -1.
func findRun(t *testing.T, prefix string) (cmd string, step int) {
	t.Helper()
	cmds, index := runCommands(t)
	for i, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			return c, index[i]
		}
	}
	return "", -1
}

func TestCIJobBuildsTextAndHTML(t *testing.T) {
	if cmd, _ := findRun(t, "kramdown-rfc draft/draft-crank-drand-dns.md"); cmd == "" {
		t.Error("draft job has no run command for kramdown-rfc")
	}
	cmd, _ := findRun(t, "xml2rfc ")
	for _, want := range []string{"--text", "--html"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("the xml2rfc command %q lacks %s", cmd, want)
		}
	}
	if !strings.Contains(draftJob(t), "actions/upload-artifact@") {
		t.Error("draft job does not upload the artifact")
	}
}

func TestCIJobFailsOnBrokenReference(t *testing.T) {
	_, build := findRun(t, "xml2rfc ")
	check, at := findRun(t, "! grep ")
	if at < 0 {
		t.Fatal("draft job has no run command that fails on a match")
	}
	if at < build {
		t.Error("the broken reference check runs before xml2rfc writes the text")
	}
	for _, want := range []string{"*** BROKEN REFERENCE ***", "draft-crank-drand-dns.xml", "draft-crank-drand-dns.txt"} {
		if !strings.Contains(check, want) {
			t.Errorf("the check %q lacks %q", check, want)
		}
	}
}

func TestCIJobHasATimeoutAndOtherJobsHaveNone(t *testing.T) {
	if !strings.Contains(draftJob(t), "\n    timeout-minutes: 10\n") {
		t.Error("draft job lacks timeout-minutes: 10")
	}
	if got := strings.Count(readCI(t), "timeout-minutes:"); got != 1 {
		t.Errorf("workflow holds %d timeout-minutes keys, want 1", got)
	}
}

func TestCIJobUploadFailsWhenNoFileExists(t *testing.T) {
	for _, s := range draftSteps(t) {
		if strings.Contains(s, "actions/upload-artifact@") {
			if !strings.Contains(s, "\n          if-no-files-found: error") {
				t.Errorf("the upload step lacks if-no-files-found: error:\n%s", s)
			}
			return
		}
	}
	t.Error("draft job has no upload step")
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
