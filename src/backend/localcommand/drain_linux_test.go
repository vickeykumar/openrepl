//go:build linux

package localcommand

import (
	"io"
	"testing"
	"time"

	"github.com/kr/pty"
)

// A program that prints and exits at once must not lose its output because the terminal was closed before the
// reader took it.
func TestTheTerminalIsNotClosedBeforeItsOutputIsRead(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skip("no pseudo-terminal here:", err)
	}
	defer master.Close()
	if _, err := slave.WriteString("hello\n"); err != nil {
		t.Fatal(err)
	}
	slave.Close() // the program has ended

	// the reader is slow: it reads 150 ms later
	got := make(chan string, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		buf := make([]byte, 64)
		n, _ := master.Read(buf)
		got <- string(buf[:n])
	}()

	start := time.Now()
	drainPty(master, 2*time.Second)
	waited := time.Since(start)
	if waited < 100*time.Millisecond {
		t.Errorf("drainPty returned after %v, before the reader took the output", waited)
	}
	if waited > time.Second {
		t.Errorf("drainPty waited %v although the reader was done", waited)
	}
	master.Close() // what the server does next
	if out := <-got; out != "hello\r\n" {
		t.Errorf("the reader got %q", out)
	}
}

func TestDrainingGivesUpWhenNobodyReads(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Skip("no pseudo-terminal here:", err)
	}
	defer master.Close()
	slave.WriteString("nobody reads this\n")
	slave.Close()
	start := time.Now()
	drainPty(master, 300*time.Millisecond)
	if waited := time.Since(start); waited < 250*time.Millisecond || waited > time.Second {
		t.Errorf("waited %v, want about the limit (300 ms)", waited)
	}
}

var _ = io.EOF
