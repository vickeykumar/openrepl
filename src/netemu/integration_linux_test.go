//go:build linux

package netemu

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These tests run the REPL side in a copy of the test binary: it installs the filter, as landlock-helper
// does, and runs a scenario in-process. The test is the server: it serves the notifications.

const childEnv = "NETEMU_TEST_CHILD"

func TestMain(m *testing.M) {
	if sc := os.Getenv(childEnv); sc != "" {
		if os.Getenv("NETEMU_STAGE") == "" {
			// stage 1, as landlock-helper does: the filter, then exec, so that it is on the process the REPL is
			if err := Install(3); err != nil {
				fmt.Println("INSTALL FAILED:", err)
				os.Exit(3)
			}
			err := syscall.Exec(os.Args[0], os.Args, append(os.Environ(), "NETEMU_STAGE=2"))
			fmt.Println("EXEC FAILED:", err)
			os.Exit(4)
		}
		os.Exit(childMain(sc))
	}
	os.Exit(m.Run())
}

func childMain(scenario string) int {
	label := os.Getenv("NETEMU_LABEL")
	var err error
	switch {
	case scenario == "samePorts":
		err = scenarioSamePorts(label)
	case scenario == "stress":
		err = scenarioStress()
	case scenario == "unix":
		err = scenarioUnix()
	case strings.HasPrefix(scenario, "serve:"):
		err = scenarioServe(strings.TrimPrefix(scenario, "serve:"), label)
	case strings.HasPrefix(scenario, "dial:"):
		err = scenarioDial(strings.TrimPrefix(scenario, "dial:"), label)
	case scenario == "selftest":
		err = SelfTest()
	default:
		err = fmt.Errorf("unknown scenario %s", scenario)
	}
	if err != nil {
		fmt.Println("FAIL:", err)
		return 1
	}
	fmt.Println("OK", label)
	return 0
}

// echoServer answers every line with the label of its workspace, and every datagram too.
func echoServer(l net.Listener, label string) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			line, _ := bufio.NewReader(c).ReadString('\n')
			fmt.Fprintf(c, "%s:%s", label, line)
		}()
	}
}

func ask(network, addr, label string) error {
	c, err := net.DialTimeout(network, addr, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "hi\n")
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if got != label+":hi\n" {
		return fmt.Errorf("%s answered %q, want %q", addr, got, label+":hi")
	}
	return nil
}

func scenarioSamePorts(label string) error {
	l, err := net.Listen("tcp", ":8080") // the default Go listener: dual-stack
	if err != nil {
		return fmt.Errorf("listen: %v", err)
	}
	defer l.Close()
	go echoServer(l, label)
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if err := ask("tcp", addr, label); err != nil {
			return fmt.Errorf("tcp %s: %v", addr, err)
		}
	}
	if l.Addr().String() != "[::]:8080" {
		return fmt.Errorf("a wildcard listener reports %s", l.Addr())
	}

	pc, err := net.ListenPacket("udp", ":9090")
	if err != nil {
		return fmt.Errorf("udp listen: %v", err)
	}
	defer pc.Close()
	go func() {
		b := make([]byte, 64)
		for {
			n, from, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			pc.WriteTo([]byte(label+":"+string(b[:n])), from)
		}
	}()
	cl, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return err
	}
	defer cl.Close()
	cl.SetDeadline(time.Now().Add(3 * time.Second))
	dst, _ := net.ResolveUDPAddr("udp", "127.0.0.1:9090")
	if _, err := cl.WriteTo([]byte("hi"), dst); err != nil {
		return fmt.Errorf("udp write: %v", err)
	}
	b := make([]byte, 64)
	n, from, err := cl.ReadFrom(b)
	if err != nil || string(b[:n]) != label+":hi" {
		return fmt.Errorf("udp reply %q from %v: %v", b[:n], from, err)
	}
	// a connected UDP socket
	uc, err := net.Dial("udp", "127.0.0.1:9090")
	if err != nil {
		return fmt.Errorf("udp dial: %v", err)
	}
	defer uc.Close()
	uc.SetDeadline(time.Now().Add(3 * time.Second))
	uc.Write([]byte("yo"))
	n, err = uc.Read(b)
	if err != nil || string(b[:n]) != label+":yo" {
		return fmt.Errorf("connected udp reply %q: %v", b[:n], err)
	}

	// nothing listens there: refused, like a real network
	if _, err := net.DialTimeout("tcp", "127.0.0.1:8099", 2*time.Second); err == nil || !strings.Contains(err.Error(), "refused") {
		return fmt.Errorf("a port nobody listens on: %v", err)
	}
	// outside the workspace nothing is reachable
	for _, addr := range []string{"192.0.2.1:80", "[2001:db8::1]:80"} {
		if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
			c.Close()
			return fmt.Errorf("%s was reachable", addr)
		}
	}
	if pc2, err := net.Dial("udp", "192.0.2.1:53"); err == nil {
		_, werr := pc2.Write([]byte("x"))
		pc2.Close()
		if werr == nil {
			return fmt.Errorf("UDP to an outside address was allowed")
		}
	}
	return nil
}

func scenarioStress() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 1500; i++ {
			c, err := net.Dial("tcp", l.Addr().String())
			if err != nil {
				done <- fmt.Errorf("connection %d: %v", i, err)
				return
			}
			c.Close()
		}
		done <- nil
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(25 * time.Second):
		return fmt.Errorf("1500 connections did not finish in 25 s")
	}
}

func scenarioUnix() error {
	if _, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_DGRAM, 0); err != syscall.EACCES {
		return fmt.Errorf("a unix datagram socket: %v", err)
	}
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("a unix stream socket: %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: "/tmp/netemu-test.sock"}); err != syscall.EACCES {
		return fmt.Errorf("binding a unix path: %v", err)
	}
	if err := syscall.Connect(fd, &syscall.SockaddrUnix{Name: "@openrepl-ne/other/t/80"}); err != syscall.EACCES {
		return fmt.Errorf("connecting to an abstract name: %v", err)
	}
	// a socket pair carries no names and is not touched
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("socketpair: %v", err)
	}
	syscall.Write(pair[0], []byte("x"))
	b := make([]byte, 1)
	if n, _ := syscall.Read(pair[1], b); n != 1 {
		return fmt.Errorf("a socket pair does not carry data")
	}
	// raw and packet sockets, netlink
	for _, dom := range []int{syscall.AF_NETLINK, syscall.AF_PACKET} {
		if _, err := syscall.Socket(dom, syscall.SOCK_RAW, 0); err == nil {
			return fmt.Errorf("a socket of family %d was allowed", dom)
		}
	}
	if _, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP); err == nil {
		return fmt.Errorf("a raw ICMP socket was allowed")
	}
	return nil
}

func scenarioServe(port, label string) error {
	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return err
	}
	defer l.Close()
	go echoServer(l, label)
	time.Sleep(3 * time.Second)
	return nil
}

func scenarioDial(port, label string) error {
	return ask("tcp", "127.0.0.1:"+port, label)
}

// ---- the server side of the tests ---------------------------------------------------------------

type started struct {
	cmd *exec.Cmd
	out *bytes.Buffer
}

func startChild(t *testing.T, workspace, scenario, label string) *started {
	t.Helper()
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	parent := os.NewFile(uintptr(pair[0]), "parent")
	child := os.NewFile(uintptr(pair[1]), "child")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), childEnv+"="+scenario, "NETEMU_LABEL="+label)
	cmd.ExtraFiles = []*os.File{child}
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child.Close()
	Attach(parent, workspace, cmd.Process.Pid)
	return &started{cmd, out}
}

func (s *started) wait(t *testing.T, limit time.Duration) (string, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		return strings.TrimSpace(s.out.String()), err
	case <-time.After(limit):
		// SIGQUIT: a Go program prints its goroutines, which says where it was stuck
		s.cmd.Process.Signal(syscall.SIGQUIT)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			s.cmd.Process.Kill()
			<-done
		}
		return strings.TrimSpace(s.out.String()), fmt.Errorf("timeout")
	}
}

// skipUnlessItWorks skips where the host does not allow the emulation (a container profile without
// pidfd_getfd, a kernel without seccomp notifications): that is a fact about the host, not a failure.
func skipUnlessItWorks(t *testing.T) {
	t.Helper()
	s := startChild(t, WorkspaceID("selftest"), "selftest", "probe")
	out, err := s.wait(t, 20*time.Second)
	if err != nil {
		t.Skipf("the network emulation does not work on this host: %v %s", err, out)
	}
}

func TestTwoWorkspacesUseTheSamePorts(t *testing.T) {
	skipUnlessItWorks(t)
	var wg sync.WaitGroup
	results := make([]string, 3)
	errs := make([]error, 3)
	for i, label := range []string{"A", "B", "C"} {
		i, label := i, label
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = startChild(t, WorkspaceID("ws-"+label), "samePorts", label).wait(t, 30*time.Second)
		}()
	}
	wg.Wait()
	for i, label := range []string{"A", "B", "C"} {
		if errs[i] != nil || !strings.Contains(results[i], "OK "+label) {
			t.Errorf("workspace %s: %v\n%s", label, errs[i], results[i])
		}
	}
}

func TestTheTerminalsAndForksOfAWorkspaceShareItsNetwork(t *testing.T) {
	skipUnlessItWorks(t)
	ws := WorkspaceID("shared")
	srv := startChild(t, ws, "serve:8181", "S")
	time.Sleep(700 * time.Millisecond)
	out, err := startChild(t, ws, "dial:8181", "S").wait(t, 15*time.Second)
	if err != nil || !strings.Contains(out, "OK") {
		t.Errorf("a second terminal of the same workspace: %v %s", err, out)
	}
	// another workspace does not see that server
	other := startChild(t, WorkspaceID("other"), "dial:8181", "S")
	out, err = other.wait(t, 15*time.Second)
	if err == nil {
		t.Errorf("another workspace reached the server: %s", out)
	}
	if !strings.Contains(out, "refused") {
		t.Errorf("another workspace should be refused, got: %s", out)
	}
	srv.wait(t, 15*time.Second)
}

func TestUnixNamesAndRawSocketsAreRefused(t *testing.T) {
	skipUnlessItWorks(t)
	out, err := startChild(t, WorkspaceID("unix"), "unix", "U").wait(t, 20*time.Second)
	if err != nil {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestManyShortConnectionsDoNotHang(t *testing.T) {
	skipUnlessItWorks(t)
	out, err := startChild(t, WorkspaceID("stress"), "stress", "X").wait(t, 40*time.Second)
	if err != nil {
		t.Errorf("%v\n%s", err, out)
	}
}
