//go:build linux
// +build linux

package landlock

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// The system call numbers are the same on every architecture Linux has since 5.13.
const (
	sysCreateRuleset = 444
	sysAddRule       = 445
	sysRestrictSelf  = 446

	createRulesetVersion = 1 << 0
	rulePathBeneath      = 1
	prSetNoNewPrivs      = 38
	oPath                = 0x200000
)

// the ruleset attribute of the kernel: the filesystem rights (ABI 1), the network rights (ABI 4, not used here)
// and the scopes (ABI 6). The kernel accepts the shorter form of an older ABI, so only a ruleset with a scope
// is passed in full.
type rulesetAttr struct {
	handledAccessFS  uint64
	handledAccessNet uint64
	scoped           uint64
}

const (
	attrSizeFS    = 8
	attrSizeScope = 24
)

// the kernel declares this packed
type pathBeneathAttr struct {
	allowedAccess uint64
	parentFD      int32
	_             [4]byte // not part of the kernel's struct: only the first 12 bytes are passed
}

// ABI returns the Landlock ABI version of the running kernel (1 or more), or
// an error that says why there is none.
func ABI() (int, error) {
	v, _, errno := syscall.Syscall(sysCreateRuleset, 0, 0, createRulesetVersion)
	switch errno {
	case 0:
		if v < 1 {
			return 0, fmt.Errorf("the kernel reports Landlock ABI %d", v)
		}
		return int(v), nil
	case syscall.ENOSYS:
		return 0, fmt.Errorf("Landlock is not reachable here: the kernel has no Landlock, or the program runs under an emulator (Rosetta, qemu-user) that does not pass its system calls on")
	case syscall.EOPNOTSUPP:
		return 0, fmt.Errorf("Landlock is built into this kernel but switched off (the lsm= boot setting)")
	default:
		return 0, fmt.Errorf("asking the kernel for the Landlock version: %v", errno)
	}
}

// ScopeSupported reports whether the kernel accepts the signal scope (ABI 6). It makes a ruleset and closes
// it, which restricts nothing.
func ScopeSupported() bool {
	attr := rulesetAttr{scoped: uint64(ScopeSignal)}
	fd, _, errno := syscall.Syscall(sysCreateRuleset, uintptr(unsafe.Pointer(&attr)), attrSizeScope, 0)
	if errno != 0 {
		return false
	}
	syscall.Close(int(fd))
	return true
}

// Self is what /proc/self points to for this process: its number in the /proc
// that is mounted (which is not what getpid says inside a PID namespace).
func Self() string {
	s, err := os.Readlink("/proc/self")
	if err != nil {
		return ""
	}
	return s
}

// Restrict puts the policy on this process and on every process it starts. It
// cannot be undone.
func Restrict(p *Policy) error {
	attr := rulesetAttr{handledAccessFS: p.Handled, scoped: p.Scoped}
	size := uintptr(attrSizeFS)
	if p.Scoped != 0 {
		size = attrSizeScope
	}
	fd, _, errno := syscall.Syscall(sysCreateRuleset, uintptr(unsafe.Pointer(&attr)), size, 0)
	if errno != 0 {
		return fmt.Errorf("creating the ruleset: %v", errno)
	}
	defer syscall.Close(int(fd))
	for _, r := range p.Rules {
		if err := addRule(int(fd), r); err != nil {
			return err
		}
	}
	// without this the kernel refuses a process that has not the privilege to restrict others
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("setting no_new_privs: %v", errno)
	}
	if _, _, errno := syscall.Syscall(sysRestrictSelf, fd, 0, 0); errno != 0 {
		return fmt.Errorf("restricting this process: %v", errno)
	}
	return nil
}

func addRule(ruleset int, r Rule) error {
	pfd, err := syscall.Open(r.Path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		if err == syscall.ENOENT {
			return nil
		}
		return fmt.Errorf("opening %s: %v", r.Path, err)
	}
	defer syscall.Close(pfd)
	rights := r.Rights
	var st syscall.Stat_t
	if err := syscall.Fstat(pfd, &st); err == nil && st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		// a rule on a file with a right for folders is refused by the kernel
		rights = FileRights(rights)
	}
	if rights == 0 {
		return nil
	}
	attr := pathBeneathAttr{allowedAccess: rights, parentFD: int32(pfd)}
	if _, _, errno := syscall.Syscall6(sysAddRule, uintptr(ruleset), rulePathBeneath, uintptr(unsafe.Pointer(&attr)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("allowing %s: %v", r.Path, errno)
	}
	return nil
}
