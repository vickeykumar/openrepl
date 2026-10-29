// Package landlock is the filesystem sandbox of a REPL (LLD 14): a Linux
// Landlock policy that lets a REPL use its own workspace and the system's
// programs and nothing else. It needs no privilege, and a restriction cannot be
// lifted by the process that has it, root included.
//
// This file builds the policy and runs on every platform. The system calls are
// in landlock_linux.go.
package landlock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The filesystem access rights of the kernel (include/uapi/linux/landlock.h).
const (
	Execute    uint64 = 1 << 0
	WriteFile  uint64 = 1 << 1
	ReadFile   uint64 = 1 << 2
	ReadDir    uint64 = 1 << 3
	RemoveDir  uint64 = 1 << 4
	RemoveFile uint64 = 1 << 5
	MakeChar   uint64 = 1 << 6
	MakeDir    uint64 = 1 << 7
	MakeReg    uint64 = 1 << 8
	MakeSock   uint64 = 1 << 9
	MakeFifo   uint64 = 1 << 10
	MakeBlock  uint64 = 1 << 11
	MakeSym    uint64 = 1 << 12
	Refer      uint64 = 1 << 13 // ABI 2
	Truncate   uint64 = 1 << 14 // ABI 3
	IoctlDev   uint64 = 1 << 15 // ABI 5
)

// Every right of an ABI version, and the ones that mean something for a file
// (a rule on a file with a right that is for directories is refused by the kernel).
func allRights(abi int) uint64 {
	r := uint64(Execute | WriteFile | ReadFile | ReadDir | RemoveDir | RemoveFile | MakeChar | MakeDir |
		MakeReg | MakeSock | MakeFifo | MakeBlock | MakeSym)
	if abi >= 2 {
		r |= Refer
	}
	if abi >= 3 {
		r |= Truncate
	}
	if abi >= 5 {
		r |= IoctlDev
	}
	return r
}

// FileRights masks rights down to those a rule on a file may have.
func FileRights(rights uint64) uint64 {
	return rights & (Execute | WriteFile | ReadFile | Truncate | IoctlDev)
}

// Handled is the set of rights the ruleset handles: all the kernel knows. What
// no rule allows is denied.
func Handled(abi int) uint64 { return allRights(abi) }

// Rule allows Rights on Path and everything below it.
type Rule struct {
	Path   string
	Rights uint64
}

// Options say what to build the policy for.
type Options struct {
	Workspace string // the REPL's own folder: all rights
	HomeRoot  string // the folder that holds all the workspaces: no rule may reach it
	ABI       int    // the kernel's Landlock ABI version
	Self      string // what /proc/self points to for the process that applies the policy, or ""
	// Debug is a Debug run: its debugger (gdb) writes the memory of the program it debugs through
	// /proc/<pid>/mem, which needs write access to /proc. Only this run gets it: with it a process could
	// write the memory of any other process of the same user that the kernel lets it trace.
	Debug bool
}

// Policy is what Build returns.
type Policy struct {
	Handled uint64
	Rules   []Rule
	// Skipped says which paths of the lists below were left out, and why (for the log)
	Skipped []string
	// TmpClosed is true when /tmp has no rule because the workspaces are inside it: a program
	// that writes there is refused, so the helper points TMPDIR into the workspace instead.
	TmpClosed bool
}

// The system, read and execute only. A path that does not exist on this system is skipped.
var readOnlyRoots = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc", "/sys",
	// where the image keeps node, rust and cling
	"/root/.nvm", "/root/.cargo", "/root/.rustup", "/opt/cling",
}

// Shared scratch folders: read and write. Every REPL sees what another puts there.
var scratchRoots = []string{"/tmp", "/var/tmp", "/dev/shm"}

// The devices a program may use. A device file needs read and write (and ioctl for a terminal).
var deviceFiles = []string{"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty", "/dev/ptmx"}
var deviceDirs = []string{"/dev/pts"}

// /proc is read-only for all: a process reads its own entry (and a child its own: ps, top, a compiled
// program that asks for /proc/self/exe) and the information about the machine. A rule can name a process
// number but not "my children", so a narrower rule breaks them. What this opens, the environment of the
// other processes, is closed another way (containers.InitLandlock makes the server non-dumpable; a REPL
// in its own user namespace cannot read it either).
const procRoot = "/proc"

// FS is what the builder needs from the file system, so a test can use a fake.
type FS interface {
	Stat(path string) (os.FileInfo, error)
	EvalSymlinks(path string) (string, error)
	ReadDir(path string) ([]os.FileInfo, error)
}

type osFS struct{}

func (osFS) Stat(p string) (os.FileInfo, error)    { return os.Stat(p) }
func (osFS) EvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }
func (osFS) ReadDir(p string) ([]os.FileInfo, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdir(-1)
}

// OS is the real file system.
var OS FS = osFS{}

// contains reports whether path is dir itself or lies below it.
func contains(dir, path string) bool {
	dir, path = filepath.Clean(dir), filepath.Clean(path)
	if dir == "/" {
		return true
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}

// Build makes the policy of a REPL.
//
// A rule is recursive and cannot be narrowed by another, so a folder that
// contains HomeRoot, or is it, may never get a rule: it would open every
// workspace. Such a path (for example /tmp when the workspaces are in
// /tmp/home) is left out, and the reason goes to Skipped.
func Build(o Options, fs FS) (*Policy, error) {
	if fs == nil {
		fs = OS
	}
	if o.ABI < 1 {
		return nil, fmt.Errorf("Landlock is not available (ABI %d)", o.ABI)
	}
	if o.Workspace == "" || !filepath.IsAbs(o.Workspace) {
		return nil, fmt.Errorf("the workspace %q is not an absolute path", o.Workspace)
	}
	if o.HomeRoot == "" || !filepath.IsAbs(o.HomeRoot) {
		return nil, fmt.Errorf("the folder of the workspaces %q is not an absolute path", o.HomeRoot)
	}
	root, err := fs.EvalSymlinks(filepath.Clean(o.HomeRoot))
	if err != nil {
		return nil, fmt.Errorf("the folder of the workspaces: %v", err)
	}
	ws, err := fs.EvalSymlinks(filepath.Clean(o.Workspace))
	if err != nil {
		return nil, fmt.Errorf("the workspace: %v", err)
	}
	if fi, err := fs.Stat(ws); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("the workspace %s is not a folder", ws)
	}
	// Resolved, so that a symlink cannot point a workspace at another one or out of the root.
	if ws == root || !contains(root, ws) {
		return nil, fmt.Errorf("the workspace %s is not inside %s", ws, root)
	}
	if root == "/" || root == "/tmp" {
		return nil, fmt.Errorf("the folder of the workspaces cannot be %s", root)
	}

	p := &Policy{Handled: Handled(o.ABI)}
	all := allRights(o.ABI)
	ro := (Execute | ReadFile | ReadDir) & all
	rw := all
	devRW := (ReadFile | WriteFile | Truncate | IoctlDev) & all
	devDirRW := (ReadFile | WriteFile | ReadDir | Truncate | IoctlDev) & all
	// a rule must not reach the other workspaces
	add := func(path string, rights uint64) {
		path = filepath.Clean(path)
		real, err := fs.EvalSymlinks(path)
		if err != nil {
			return // not on this system
		}
		if contains(real, root) {
			p.Skipped = append(p.Skipped, path+" (it holds the workspaces)")
			if path == "/tmp" {
				p.TmpClosed = true
			}
			return
		}
		p.Rules = append(p.Rules, Rule{real, rights})
	}

	add(ws, rw)
	for _, d := range scratchRoots {
		add(d, rw)
	}
	for _, d := range readOnlyRoots {
		add(d, ro)
	}
	// the tools folders of the server; its databases are files in this folder itself, not below
	if entries, err := fs.ReadDir("/opt/gotty"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add("/opt/gotty/"+e.Name(), ro)
			}
		}
	}
	if entries, err := fs.ReadDir("/opt"); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != "openrepl" && e.Name() != "gotty" && e.Name() != "cling" {
				add("/opt/"+e.Name(), ro)
			}
		}
	}
	for _, f := range deviceFiles {
		add(f, devRW)
	}
	for _, d := range deviceDirs {
		add(d, devDirRW)
	}
	procRights := ro
	if o.Debug {
		procRights = (ro | WriteFile | Truncate) & all
	}
	add(procRoot, procRights)
	if o.Self != "" && !strings.ContainsAny(o.Self, "/.") {
		// the process' own entry can also be written (oom_score_adj and the like)
		add("/proc/"+o.Self, (ro|WriteFile|Truncate)&all)
	}
	return p, nil
}
