//go:build interop

package interop

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMain checks Docker, pulls the pinned images, removes stale containers and then runs the tests.
// It starts no container.
// After the tests it fails the run when a container of this run remains.
func TestMain(m *testing.M) {
	_, err := docker("version", "--format", "{{.Server.Version}}")
	switch dockerGate(err == nil, os.Getenv("CI")) {
	case gateRun:
		dockerReady = true
		// A container start must never include a pull, so the pull happens here with its own time limit.
		if err := pullImages(); err != nil {
			fmt.Fprintf(os.Stderr, "interop: pull the pinned images: %v\n", err)
			os.Exit(1)
		}
		// An interrupted run leaves its containers, and its run ID never recurs.
		if err := removeStaleContainers(); err != nil {
			fmt.Fprintf(os.Stderr, "interop: remove stale containers: %v\n", err)
			os.Exit(1)
		}
	case gateFail:
		fmt.Fprintf(os.Stderr, "interop: Docker is not available in CI: %v\n", err)
		os.Exit(1)
	case gateSkip:
		fmt.Fprintf(os.Stderr, "interop: Docker is not available, so the tests that need it skip: %v\n", err)
	}
	code := m.Run()
	if dockerReady {
		left, err := labeledContainers()
		if err != nil {
			fmt.Fprintf(os.Stderr, "interop: list containers: %v\n", err)
			code = 1
		}
		if len(left) > 0 {
			fmt.Fprintf(os.Stderr, "interop: containers remain after the run: %s\n", strings.Join(left, ", "))
			for _, name := range left {
				_ = removeContainer(name)
			}
			code = 1
		}
	}
	os.Exit(code)
}

func TestMissingDockerSkipsOnDeveloperMachineAndFailsInCI(t *testing.T) {
	cases := []struct {
		name      string
		available bool
		ci        string
		want      gate
	}{
		{"runs with Docker on a developer machine", true, "", gateRun},
		{"runs with Docker in CI", true, "true", gateRun},
		{"skips without Docker on a developer machine", false, "", gateSkip},
		{"fails without Docker in CI", false, "true", gateFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dockerGate(c.available, c.ci); got != c.want {
				t.Errorf("dockerGate(%v, %q) = %d, want %d", c.available, c.ci, got, c.want)
			}
		})
	}
}

func TestStaleContainersSelectsOnlyContainersOlderThanTheLimit(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	lines := "old 2026-11-06 11:20:00 +0000 UTC\n" +
		"young 2026-11-06 11:45:00 +0000 UTC\n" +
		"edge 2026-11-06 11:30:00 +0000 UTC\n" +
		"zoned 2026-11-06 03:00:00 -0700 MST\n" +
		"broken not-a-time\n\n"
	got := staleContainers(lines, now, 30*time.Minute)
	want := []string{"old", "zoned"}
	if !slices.Equal(got, want) {
		t.Errorf("staleContainers = %v, want %v", got, want)
	}
}

func TestContainerRemovedAfterFailure(t *testing.T) {
	needDocker(t)
	var name string
	// SkipNow ends the subtest the way FailNow does, and the parent test still passes.
	t.Run("ends early", func(t *testing.T) {
		name = startContainer(t, alpineImage, nil, nil, "sleep", "300")
		t.SkipNow()
	})
	left, err := labeledContainers()
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	for _, l := range left {
		if l == name {
			t.Errorf("container %s remains after its test ended early", name)
		}
	}
}
