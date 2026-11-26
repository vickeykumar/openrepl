//go:build linux

package livepreview

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A workspace that has a network namespace of its own (a privileged host): DialIn joins it and reaches a program there,
// and the ports of that namespace are read from /proc/<pid>/net/tcp. Needs the right to make a namespace (root with
// CAP_SYS_ADMIN); where the test cannot make one it says so and stops.
func TestAProgramInAnotherNetworkNamespaceIsReachedAndListed(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to make a network namespace")
	}
	script := `ifconfig lo up 2>/dev/null || ip link set lo up 2>/dev/null; python3 -m http.server 7777 --bind 127.0.0.1 >/dev/null 2>&1 & echo ready; wait`
	cmd := exec.Command("unshare", "-n", "sh", "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start unshare: %v", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	line, _ := bufio.NewReader(out).ReadString('\n')
	if !strings.Contains(line, "ready") {
		t.Skip("the namespace could not be made here")
	}
	pid := cmd.Process.Pid
	if SameNetwork(pid) {
		t.Skip("the process did not get a namespace of its own")
	}
	deadline := time.Now().Add(5 * time.Second)
	var ports []int
	for time.Now().Before(deadline) {
		ports = ParseTCP(ReadProc(fmt.Sprintf("/proc/%d/net/tcp", pid)))
		if len(ports) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(ports) != 1 || ports[0] != 7777 {
		t.Fatalf("the ports of the namespace: %v", ports)
	}
	// a connection from here, made inside that namespace
	conn, err := DialIn(fmt.Sprintf("/proc/%d/ns/net", pid), 7777, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET / HTTP/1.0\r\nHost: x\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	// the server itself did not stay in the namespace: its own network is what it was (a closed port here is refused)
	if _, err := DialIn(fmt.Sprintf("/proc/%d/ns/net", pid), 7778, time.Second); err == nil {
		t.Error("a port with nothing behind it was reached")
	}
}

func TestANamespaceThatIsNotThereIsAnError(t *testing.T) {
	if _, err := DialIn("/proc/999999999/ns/net", 5000, time.Second); err == nil {
		t.Error("a missing namespace was joined")
	}
	if SameNetwork(999999999) {
		t.Error("a missing process is in this network")
	}
}

// Docker's default seccomp profile gives every process of a container, the server's too, one filter: an admin's REPL (no filter of its own) is still unsandboxed,
// and a REPL under the emulation (one more filter) is not.
func TestUnsandboxedCountsTheFiltersTheServerHasNot(t *testing.T) {
	status := func(mode, filters string) string {
		return "Name:\tx\nSeccomp:\t" + mode + "\nSeccomp_filters:\t" + filters + "\n"
	}
	for _, c := range []struct {
		name      string
		pid, self string
		want      bool
	}{
		{"a host with no filters at all", status("0", "0"), status("0", "0"), true},
		{"a docker container: the REPL has the container's filter only", status("2", "1"), status("2", "1"), true},
		{"a docker container: the emulation added one", status("2", "2"), status("2", "1"), false},
		{"no container filter, the emulation's alone", status("2", "1"), status("0", "0"), false},
		{"an old kernel with no count: no filter", "Seccomp:\t0\n", "Seccomp:\t0\n", true},
		{"an old kernel with no count: a filter", "Seccomp:\t2\n", "Seccomp:\t2\n", false},
		{"nothing readable", "", "", false},
	} {
		if got := unsandboxedStatus(c.pid, c.self); got != c.want {
			t.Errorf("%s: unsandboxedStatus = %v, want %v", c.name, got, c.want)
		}
	}
}
