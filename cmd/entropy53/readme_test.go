package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// readmeSections splits README.md at its level-2 headings.
// The key is the heading text and the value is the section body.
// The first heading is also returned by name, because the order matters.
func readmeSections(t *testing.T) (first string, sections map[string]string) {
	t.Helper()
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	sections = map[string]string{}
	var name string
	for _, line := range strings.Split(string(data), "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			name = strings.TrimSpace(h)
			if first == "" {
				first = name
			}
			continue
		}
		if name != "" {
			sections[name] += line + "\n"
		}
	}
	return first, sections
}

// commandLine returns the first line in body that starts with prefix.
func commandLine(t *testing.T, body, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no line starts with %q", prefix)
	return ""
}

func TestReadmeFirstSectionShowsRoundAtAndGet(t *testing.T) {
	first, sections := readmeSections(t)
	if first != "Pin a round" {
		t.Fatalf("the first section is %q, want %q", first, "Pin a round")
	}
	body := sections[first]
	commandLine(t, body, "entropy53 round-at ")
	commandLine(t, body, "entropy53 get ")
}

func TestReadmeRoundAtExampleGivesTheGetRound(t *testing.T) {
	_, sections := readmeSections(t)
	body := sections["Pin a round"]
	roundAtArgs := strings.Fields(commandLine(t, body, "entropy53 round-at "))[1:]
	getArgs := strings.Fields(commandLine(t, body, "entropy53 get "))[1:]

	code, out, errOut := runCmd(beforeAll, roundAtArgs...)
	if code != 0 || errOut != "" {
		t.Fatalf("round-at exits %d with stderr %q", code, errOut)
	}
	round := strings.TrimSpace(out)
	if !regexp.MustCompile(`^[0-9]+$`).MatchString(round) {
		t.Fatalf("round-at prints %q, want a round number", out)
	}
	if getArgs[2] != round {
		t.Fatalf("the get line uses round %q, round-at prints %q", getArgs[2], round)
	}
}

func TestReadmeStatesLatestHasNoSecurityGain(t *testing.T) {
	_, sections := readmeSections(t)
	body := sections["Pin a round"]
	if !strings.Contains(body, "`latest`") || !strings.Contains(body, "convenience") || !strings.Contains(body, "no security gain") {
		t.Fatal("the first section must state that `latest` is for convenience with no security gain")
	}
}
