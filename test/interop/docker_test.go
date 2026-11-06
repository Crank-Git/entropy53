//go:build interop

package interop

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// containerLabel marks every container that the tests start. Its value is the run ID.
	containerLabel = "entropy53.interop"
	// alpineImage has getent. The Unbound image has no shell.
	alpineImage = "alpine:3.22@sha256:bfe7ceb9f09ec0566c48fb93356e45a47a00be5a1dd14fa5b909e7709c66b034"
	// dockerTimeout limits one docker command.
	dockerTimeout = 60 * time.Second
	// pullTimeout limits the pull of one image. A pull downloads layers, so it takes longer than other commands.
	pullTimeout = 5 * time.Minute
	// staleAge is the age after which TestMain removes a container that an earlier run left.
	// A concurrent run finishes within the job timeout, which is shorter, so the limit never removes its containers.
	staleAge = 30 * time.Minute
)

var (
	// runID separates the containers of this run from the containers of another run on the same machine.
	runID = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	// dockerReady is set by TestMain. A test that needs Docker skips when it is false.
	dockerReady bool
	// containerSeq numbers the containers of this run.
	containerSeq atomic.Int64
)

// gate is the decision of dockerGate.
type gate int

const (
	// gateRun means Docker works and the tests run.
	gateRun gate = iota
	// gateSkip means Docker is missing on a developer machine, so the tests that need it skip.
	gateSkip
	// gateFail means Docker is missing in CI, so the run fails.
	gateFail
)

// dockerGate decides what a run does when Docker is, or is not, available.
// The CI environment variable is non-empty in CI.
func dockerGate(available bool, ci string) gate {
	switch {
	case available:
		return gateRun
	case ci != "":
		return gateFail
	default:
		return gateSkip
	}
}

// docker runs the docker CLI and returns its standard output.
// Standard error goes only into the error message, because a caller parses the output
// and a pull message on standard error would break the parse.
func docker(args ...string) (string, error) {
	return dockerTimed(dockerTimeout, args...)
}

// dockerTimed is docker with its own time limit.
func dockerTimed(limit time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return text, nil
}

// pinnedImages lists every image that the tests start, each with its digest.
func pinnedImages() []string {
	images := []string{alpineImage}
	for _, r := range resolvers {
		images = append(images, r.image)
	}
	return images
}

// pullImages pulls every pinned image before the first test, so that no container start includes a pull.
func pullImages() error {
	for _, image := range pinnedImages() {
		if _, err := dockerTimed(pullTimeout, "pull", "--quiet", image); err != nil {
			return err
		}
	}
	return nil
}

// staleContainers returns the names of the containers that are older than maxAge.
// Each line holds a name, a space and the creation time in the format of docker ps ("2006-01-02 15:04:05 -0700 MST").
// A line that does not parse is skipped, so that the function never removes a container it cannot date.
func staleContainers(lines string, now time.Time, maxAge time.Duration) []string {
	var stale []string
	for _, line := range strings.Split(lines, "\n") {
		name, created, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700 MST", strings.TrimSpace(created))
		if err != nil {
			continue
		}
		if now.Sub(t) > maxAge {
			stale = append(stale, name)
		}
	}
	return stale
}

// removeStaleContainers removes the labeled containers that an interrupted run left.
func removeStaleContainers() error {
	out, err := docker("ps", "-a", "--filter", "label="+containerLabel, "--format", "{{.Names}} {{.CreatedAt}}")
	if err != nil {
		return err
	}
	for _, name := range staleContainers(out, time.Now(), staleAge) {
		if err := removeContainer(name); err != nil {
			return err
		}
	}
	return nil
}

// needDocker skips the test when Docker is missing on a developer machine.
func needDocker(t *testing.T) {
	t.Helper()
	if !dockerReady {
		t.Skip("Docker is not available. Start Docker to run the interop tests.")
	}
}

// removeContainer removes a container, running or not, with its anonymous volumes.
func removeContainer(name string) error {
	_, err := docker("rm", "-f", "-v", name)
	if err != nil && !strings.Contains(err.Error(), "No such container") {
		return err
	}
	return nil
}

// startContainer creates the container, copies the files into it and starts it.
// files maps a path in the container to its content. args follow the image name.
// The cleanup removes the container when the test ends, and also after a failure.
func startContainer(t *testing.T, image string, flags []string, files map[string]string, args ...string) string {
	t.Helper()
	name := fmt.Sprintf("e53-interop-%s-%d", runID, containerSeq.Add(1))
	t.Cleanup(func() {
		if err := removeContainer(name); err != nil {
			t.Errorf("remove container: %v", err)
		}
	})
	create := append([]string{"create", "--name", name, "--label", containerLabel + "=" + runID,
		"--add-host=host.docker.internal:host-gateway"}, flags...)
	create = append(append(create, image), args...)
	if _, err := docker(create...); err != nil {
		t.Fatalf("create container: %v", err)
	}
	dir := t.TempDir()
	i := 0
	for path, content := range files {
		i++
		src := filepath.Join(dir, strconv.Itoa(i))
		if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		if _, err := docker("cp", src, name+":"+path); err != nil {
			t.Fatalf("copy file: %v", err)
		}
	}
	if _, err := docker("start", name); err != nil {
		t.Fatalf("start container: %v", err)
	}
	return name
}

// labeledContainers returns the names of the containers of this run, running or not.
func labeledContainers() ([]string, error) {
	out, err := docker("ps", "-a", "--filter", "label="+containerLabel+"="+runID, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// waitFor polls cond every 250 ms until it returns true or timeout passes.
// On a timeout it fails the test with the last detail and the logs of the container.
func waitFor(t *testing.T, what, container string, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var detail string
	for {
		var ok bool
		if ok, detail = cond(); ok {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("timeout after %s waiting for %s: %s\n%s", timeout, what, detail, containerLogs(container))
		}
	}
}

// containerLogs returns the logs of one container, or of every container of this run when name is empty.
func containerLogs(name string) string {
	names := []string{name}
	if name == "" {
		var err error
		if names, err = labeledContainers(); err != nil {
			return fmt.Sprintf("list containers: %v", err)
		}
	}
	var b strings.Builder
	for _, n := range names {
		// docker logs replays the container's stderr on its own stderr, so this call reads both streams.
		combined, err := exec.Command("docker", "logs", n).CombinedOutput()
		logs := strings.TrimSpace(string(combined))
		if err != nil {
			logs = err.Error()
		}
		fmt.Fprintf(&b, "docker logs %s:\n%s\n", n, logs)
	}
	return b.String()
}

// freePort returns a port that is free for both TCP and UDP on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	tcp, udp, err := listenPair("127.0.0.1")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := tcp.Addr().(*net.TCPAddr).Port
	_ = tcp.Close()
	_ = udp.Close()
	return port
}

// listenPair binds TCP and UDP on one port of ip. The OS picks the port.
// Another process can hold the UDP port that the OS gave to TCP, so listenPair tries again.
func listenPair(ip string) (net.Listener, net.PacketConn, error) {
	var lastErr error
	for range 5 {
		tcp, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
		if err != nil {
			return nil, nil, err
		}
		udp, err := net.ListenPacket("udp", tcp.Addr().String())
		if err == nil {
			return tcp, udp, nil
		}
		_ = tcp.Close()
		lastErr = err
	}
	return nil, nil, lastErr
}

var (
	hostOnce sync.Once
	hostIP   string
	hostErr  error
)

// containerHostIP returns the IPv4 address that a container uses for host.docker.internal.
// The Unbound option stub-addr needs an IP, and the container has no shell, so an alpine container reads it.
func containerHostIP() (string, error) {
	hostOnce.Do(func() {
		out, err := docker("run", "--rm", "--label", containerLabel+"="+runID,
			"--add-host=host.docker.internal:host-gateway", alpineImage, "getent", "ahostsv4", "host.docker.internal")
		if err != nil {
			hostErr = err
			return
		}
		fields := strings.Fields(out)
		if len(fields) == 0 || net.ParseIP(fields[0]) == nil {
			hostErr = fmt.Errorf("getent printed no IPv4 address: %q", out)
			return
		}
		hostIP = fields[0]
	})
	return hostIP, hostErr
}

// listenIP returns the address that the query counter binds.
// A Linux host owns the container gateway IP, so the counter binds it.
// Docker Desktop forwards the container traffic to the loopback address.
func listenIP(containerHost string) (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("list interface addresses: %w", err)
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.String() == containerHost {
			return containerHost, nil
		}
	}
	return "127.0.0.1", nil
}
