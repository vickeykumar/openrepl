//go:build linux
// +build linux

package landlock

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A Landlock restriction cannot be lifted, so the test runs the policy in a
// child process: this test program started again, which applies the policy to
// itself and reports what it was and was not allowed to do.
const childEnv = "LANDLOCK_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		child()
		return
	}
	os.Exit(m.Run())
}

func child() {
	abi, err := ABI()
	if err != nil {
		fmt.Println("abi:", err)
		os.Exit(3)
	}
	ws, root := os.Getenv("LL_WORKSPACE"), os.Getenv("LL_ROOT")
	p, err := Build(Options{Workspace: ws, HomeRoot: root, ABI: abi, Self: Self()}, nil)
	if err != nil {
		fmt.Println("build:", err)
		os.Exit(4)
	}
	if err := Restrict(p); err != nil {
		fmt.Println("restrict:", err)
		os.Exit(5)
	}
	other := os.Getenv("LL_OTHER")
	try := func(name string, fn func() error) {
		if err := fn(); err != nil {
			if os.IsPermission(err) || err == syscall.EACCES || strings.Contains(err.Error(), "permission denied") {
				fmt.Printf("%s=denied\n", name)
			} else {
				fmt.Printf("%s=error:%v\n", name, err)
			}
			return
		}
		fmt.Printf("%s=allowed\n", name)
	}
	try("own_write", func() error { return ioutil.WriteFile(filepath.Join(ws, "mine.txt"), []byte("x"), 0644) })
	try("own_read", func() error { _, err := ioutil.ReadFile(filepath.Join(ws, "mine.txt")); return err })
	try("own_mkdir", func() error { return os.Mkdir(filepath.Join(ws, "sub"), 0755) })
	try("own_rename", func() error { return os.Rename(filepath.Join(ws, "mine.txt"), filepath.Join(ws, "sub", "m.txt")) })
	try("own_mknod", func() error { return syscall.Mknod(filepath.Join(ws, "null2"), syscall.S_IFCHR|0600, 0x0103) })
	try("other_read", func() error { _, err := ioutil.ReadFile(filepath.Join(other, "secret.txt")); return err })
	try("other_write", func() error { return ioutil.WriteFile(filepath.Join(other, "pwned.txt"), []byte("x"), 0644) })
	try("other_remove", func() error { return os.Remove(filepath.Join(other, "secret.txt")) })
	try("other_rename", func() error { return os.Rename(other, other+"-moved") })
	try("list_root", func() error { _, err := ioutil.ReadDir(root); return err })
	try("dev_null_write", func() error { return ioutil.WriteFile("/dev/null", []byte("x"), 0) })
	try("etc_read", func() error { _, err := ioutil.ReadFile("/etc/passwd"); return err })
	try("etc_write", func() error { return ioutil.WriteFile("/etc/openrepl-test", []byte("x"), 0644) })
	try("usr_write", func() error { return ioutil.WriteFile("/usr/openrepl-test", []byte("x"), 0644) })
	try("proc_own_status", func() error { _, err := ioutil.ReadFile("/proc/self/status"); return err })
	// a child process reads its own entry too (ps, top, a program that asks for /proc/self/exe)
	try("proc_child_self", func() error { return exec.Command("/bin/cat", "/proc/self/status").Run() })
	try("proc_meminfo", func() error { _, err := ioutil.ReadFile("/proc/meminfo"); return err })
}

func runChild(t *testing.T, ws, root, other string) map[string]string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=NoSuchTest")
	cmd.Env = append(os.Environ(), childEnv+"=1", "LL_WORKSPACE="+ws, "LL_ROOT="+root, "LL_OTHER="+other)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var lines []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if i := strings.Index(sc.Text(), "="); i > 0 {
			got[sc.Text()[:i]] = sc.Text()[i+1:]
		}
	}
	err = cmd.Wait()
	if len(got) == 0 {
		t.Fatalf("the child said nothing useful (%v): %v", err, lines)
	}
	return got
}

func TestAProcessIsConfinedToItsWorkspace(t *testing.T) {
	if _, err := ABI(); err != nil {
		t.Skip("no Landlock on this kernel: ", err)
	}
	root := t.TempDir() // in /tmp: the policy leaves /tmp out, because it holds the workspaces
	ws := filepath.Join(root, "guest-1")
	other := filepath.Join(root, "guest-2")
	for _, d := range []string{ws, other} {
		if err := os.Mkdir(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := ioutil.WriteFile(filepath.Join(other, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	got := runChild(t, ws, root, other)

	want := map[string]string{
		"own_write": "allowed", "own_read": "allowed", "own_mkdir": "allowed", "own_rename": "allowed",
		"other_read": "denied", "other_write": "denied", "other_remove": "denied", "other_rename": "denied",
		"list_root": "denied", "dev_null_write": "allowed", "etc_read": "allowed",
		"etc_write": "denied", "usr_write": "denied", "proc_child_self": "allowed", "proc_meminfo": "allowed",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: %q, want %q", name, got[name], w)
		}
	}
	// the child's own /proc entry is readable, whatever number it has in this /proc
	if got["proc_own_status"] != "allowed" {
		t.Errorf("a process cannot read its own /proc entry: %q", got["proc_own_status"])
	}
	// device creation in the workspace was allowed by the owner: the kernel may still refuse
	// it without the capability, but Landlock must not be what refuses it
	if v := got["own_mknod"]; v == "denied" && os.Geteuid() == 0 {
		t.Errorf("mknod in the workspace was denied while running as root: %q", v)
	}
	if _, err := os.Stat(filepath.Join(other, "pwned.txt")); err == nil {
		t.Error("the child wrote into another workspace")
	}
	if b, _ := ioutil.ReadFile(filepath.Join(other, "secret.txt")); string(b) != "secret" {
		t.Error("the secret of the other workspace was changed")
	}
}
