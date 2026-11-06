package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlagPrintsNonEmptyVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("--version printed an empty string")
	}
}

func TestVersionFlagPrintsDevWithoutBuildVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != "dev" {
		t.Fatalf("--version printed %q, want %q", got, "dev")
	}
}

func TestHelpPrintsUsageOnceAndExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if got := strings.Count(errOut.String(), "Usage of"); got != 1 {
		t.Fatalf("usage printed %d times, want 1: %q", got, errOut.String())
	}
	if strings.Contains(errOut.String(), "help requested") {
		t.Fatalf("output contains the help-requested line: %q", errOut.String())
	}
}

func TestBadFlagPrintsErrorOnceAndExitsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if got := strings.Count(errOut.String(), "flag provided but not defined"); got != 1 {
		t.Fatalf("error printed %d times, want 1: %q", got, errOut.String())
	}
}
