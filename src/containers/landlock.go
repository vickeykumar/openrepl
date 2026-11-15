package containers

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"landlock"
	"utils"
)

// The filesystem sandbox of a REPL (LLD 14) is a Landlock policy that a small
// static helper (src/landlockhelper) puts on itself before it becomes the REPL.
// It is independent of the cgroups and namespaces of container.go: a host may
// have either, both or neither.

// LandlockInfo says what the sandbox is on this server.
type LandlockInfo struct {
	Mode   string // auto, off or require (OPENREPL_LANDLOCK)
	Active bool   // REPLs are started under the policy
	ABI    int    // the kernel's version, 0 when it has none
	Scope  bool   // the kernel has the signal scope (ABI 6): REPLs without a PID namespace of their own get it
	Helper string // the helper program
	Reason string // why it is not active, or what is applied
}

var (
	landlockMu   sync.Mutex
	landlockInfo = LandlockInfo{Mode: utils.LandlockAuto, Reason: "not initialised"}
)

// LandlockStatus is what InitLandlock found.
func LandlockStatus() LandlockInfo {
	landlockMu.Lock()
	defer landlockMu.Unlock()
	return landlockInfo
}

// helperName is the program that applies the policy, built with the server.
const helperName = "landlock-helper"

// findHelper looks beside the server, then in the PATH.
func findHelper() string {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), helperName)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(helperName); err == nil {
		return p
	}
	return ""
}

// InitLandlock decides, once, whether REPLs are started under the sandbox, and
// logs what it found.
func InitLandlock() {
	info := LandlockInfo{Mode: utils.LandlockMode()}
	switch {
	case info.Mode == utils.LandlockOff:
		info.Reason = "switched off by " + utils.EnvLandlock + "=off"
	default:
		abi, err := landlock.ABI()
		if err != nil {
			info.Reason = err.Error()
			break
		}
		info.ABI = abi
		info.Helper = findHelper()
		if info.Helper == "" {
			info.Reason = fmt.Sprintf("the program %s was not found beside the server or in the PATH", helperName)
			break
		}
		// the helper must run here, as it will for a REPL
		if out, err := exec.Command(info.Helper, "--probe").CombinedOutput(); err != nil {
			info.Reason = fmt.Sprintf("%s does not run: %v %s", helperName, err, strings.TrimSpace(string(out)))
			break
		}
		info.Active = true
		info.Scope = landlock.ScopeSupported()
		info.Reason = fmt.Sprintf("REPLs run under the Landlock policy (kernel ABI %d), a file a REPL writes may have %d MB at most", abi, utils.WorkspaceQuotaMB)
		if info.Scope {
			info.Reason += "; signals between REPLs are blocked where a REPL has no PID namespace of its own"
		} else {
			info.Reason += "; no signal scope on this kernel (needs ABI 6, Linux 6.12)"
		}
	}
	landlockMu.Lock()
	landlockInfo = info
	landlockMu.Unlock()
	// /proc is readable for the REPLs under the policy (ps and top need it), and so is the environment
	// of the server, where a host such as Render keeps its secrets, unless the server is not dumpable
	if info.Mode != utils.LandlockOff {
		if err := makeNonDumpable(); err != nil {
			log.Printf("WARNING: could not make the server non-dumpable, a REPL may read its environment in /proc: %v", err)
		}
	}
	if info.Active {
		log.Printf("INFO: filesystem sandbox (Landlock), mode %s: %s", info.Mode, info.Reason)
	} else if info.Mode == utils.LandlockRequire {
		log.Printf("ERROR: filesystem sandbox (Landlock) is required but not available: %s; REPLs will not start", info.Reason)
	} else {
		log.Printf("WARNING: no filesystem sandbox (Landlock), mode %s: %s", info.Mode, info.Reason)
	}
}

// LandlockCheck is an error when the mode is require and the sandbox is not
// active: then no REPL may start.
func LandlockCheck() error {
	info := LandlockStatus()
	if info.Mode == utils.LandlockRequire && !info.Active {
		return fmt.Errorf("the filesystem sandbox is required (%s=require) but not available: %s", utils.EnvLandlock, info.Reason)
	}
	return nil
}

// wrapWithLandlock makes a command run under the policy of the workspace and, where it has no kernel network
// namespace of its own (scopeSignals: the same condition), in the private network of the workspace: the
// helper applies both and then runs the command. With no workspace, or neither on this server, the command is
// returned as it is.
func wrapWithLandlock(command []string, workspace string, debug, scopeSignals bool) []string {
	if workspace == "" || len(command) == 0 {
		return command
	}
	info := LandlockStatus()
	net := scopeSignals && NetEmuActive()
	if !info.Active && !net {
		return wrapStoreEnv(command, workspace)
	}
	var wrapped []string
	if info.Active {
		wrapped = []string{info.Helper, "--workspace", workspace, "--home-root", strings.TrimSuffix(utils.HOME_DIR, "/"),
			"--max-file-mb", strconv.Itoa(utils.WorkspaceQuotaMB)}
		if scopeSignals && info.Scope {
			wrapped = append(wrapped, "--scope-signals")
		}
		if debug {
			wrapped = append(wrapped, "--debug")
		}
		if dir := utils.StoreDir(); dir != "" {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				wrapped = append(wrapped, "--store-dir", dir) // the user's App Store bundles (LLD 18)
			}
		}
	} else {
		wrapped = append([]string{NetEmuStatus().Helper, "--no-landlock"}, storeArgs(workspace)...)
	}
	if net {
		wrapped = append(wrapped, "--net-emulation")
	}
	return append(append(wrapped, "--"), command...)
}

// WrapForVerification makes a command run in the sandbox of a user's workspace, with one App Store bundle more allowed than the user's
// list has (the one being checked before it is switched on). With no sandbox on this server the command is returned as it is.
func WrapForVerification(command []string, workspace, bundle string) []string {
	wrapped := wrapWithLandlock(command, workspace, false, false)
	if bundle == "" {
		return wrapped
	}
	for i := 0; i+1 < len(wrapped); i++ {
		if wrapped[i] == "--store-dir" {
			out := append([]string{}, wrapped[:i+2]...)
			out = append(out, "--store-extra", bundle)
			return append(out, wrapped[i+2:]...)
		}
	}
	return wrapped
}

// storeArgs are the helper's arguments that let it put the user's App Store apps on the environment (the folder of the store and the
// user's workspace); none when the store is off or its folder is not there.
func storeArgs(workspace string) []string {
	dir := utils.StoreDir()
	if workspace == "" || dir == "" {
		return nil
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	return []string{"--workspace", workspace, "--store-dir", dir}
}

// wrapStoreEnv runs a command that has no sandbox (an admin's terminal, or a server without Landlock) through the helper, only so that the
// user's App Store apps are on its PATH, PYTHONPATH and NODE_PATH like in every other terminal. The command is returned as it is when
// there is no store or no helper.
func wrapStoreEnv(command []string, workspace string) []string {
	args := storeArgs(workspace)
	helper := findHelper()
	if len(args) == 0 || helper == "" || len(command) == 0 {
		return command
	}
	return append(append([]string{helper, "--no-landlock"}, append(args, "--")...), command...)
}
