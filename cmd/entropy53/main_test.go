package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlagPrintsNonEmptyVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatalf("run returned %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Fatal("--version printed an empty string")
	}
}

func TestVersionFlagPrintsDevWithoutBuildVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatalf("run returned %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "dev" {
		t.Fatalf("--version printed %q, want %q", got, "dev")
	}
}
