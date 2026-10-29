package localcommand

import (
	"bufio"
	"io/ioutil"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive says whether a process is running: a zombie is not.
func alive(pid int) bool {
	data, err := ioutil.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(data)
	end := strings.LastIndex(s, ")")
	if end < 0 || len(s) < end+4 {
		return false
	}
	return s[end+2] != 'Z'
}

func waitGone(t *testing.T, what string, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("%s (pid %d) is still running", what, pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// a terminal as pty.Start makes it: a shell that is the leader of a session, with a program it started
func startTerminal(t *testing.T) (shell *exec.Cmd, child int) {
	t.Helper()
	shell = exec.Command("sh", "-c", "sleep 60 & echo $!; wait")
	shell.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-shell.Process.Pid, syscall.SIGKILL); shell.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	child, err = strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	if !alive(child) {
		t.Fatal("the program did not start")
	}
	return shell, child
}

func TestClosingATerminalStopsWhatItStartedNotOnlyItsShell(t *testing.T) {
	shell, child := startTerminal(t)
	lcmd := &LocalCommand{cmd: shell, ptyClosed: make(chan struct{})}
	lcmd.signalAll(syscall.SIGKILL)
	shell.Wait()
	waitGone(t, "the program the shell started", child)
}

func TestWhatIsLeftWhenTheShellEndsIsStopped(t *testing.T) {
	shell, child := startTerminal(t)
	// the shell ends on its own, as when the user types exit, and leaves the program running
	syscall.Kill(shell.Process.Pid, syscall.SIGKILL)
	shell.Wait()
	if !alive(child) {
		t.Skip("the program ended with its shell here")
	}
	killGroup(shell.Process.Pid)
	waitGone(t, "the program left behind", child)
}

func TestNothingIsSentOnceTheTerminalIsOverBecauseTheNumberMayBeSomebodyElses(t *testing.T) {
	shell, child := startTerminal(t)
	closed := make(chan struct{})
	close(closed)
	lcmd := &LocalCommand{cmd: shell, ptyClosed: closed}
	lcmd.signalAll(syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	if !alive(child) || !alive(shell.Process.Pid) {
		t.Error("a signal was sent to a terminal that was over")
	}
}

func TestKillGroupIgnoresNumbersItMustNotTouch(t *testing.T) {
	killGroup(0)
	killGroup(1)
	killGroup(-5) // nothing happens, and above all not a kill of everything
}
