package containers

import (
	"fmt"
	"log"
	"sync"

	"netemu"
	"utils"
)

// The network emulation (LLD 16): a REPL that has no kernel namespaces of its own gets a private network of
// its workspace. It is applied by landlock-helper, like the filesystem policy, and served by this process
// (package netemu). Whether it works is found out once, at start, by running the helper's self test the way a
// REPL runs.

// NetEmuInfo says what the network emulation is on this server.
type NetEmuInfo struct {
	Mode   string // auto, off or require (OPENREPL_NET_ISOLATION)
	Active bool   // REPLs without namespaces of their own are started under it
	Helper string // the program that applies it
	Reason string // why it is not active, or what it does
}

var (
	netEmuMu   sync.Mutex
	netEmuInfo = NetEmuInfo{Mode: utils.NetIsolationAuto, Reason: "not initialised"}
)

// NetEmuStatus is what InitNetEmu found.
func NetEmuStatus() NetEmuInfo {
	netEmuMu.Lock()
	defer netEmuMu.Unlock()
	return netEmuInfo
}

// NetEmuActive says whether REPLs without namespaces of their own run in the private network of their workspace.
func NetEmuActive() bool { return NetEmuStatus().Active }

// InitNetEmu decides, once, whether the emulation is on, and logs what it found.
func InitNetEmu() {
	info := NetEmuInfo{Mode: utils.NetIsolationMode()}
	if info.Mode == utils.NetIsolationOff {
		info.Reason = "switched off by " + utils.EnvNetIsolation + "=off"
	} else if info.Helper = findHelper(); info.Helper == "" {
		info.Reason = fmt.Sprintf("the program %s was not found beside the server or in the PATH", helperName)
	} else if err := netemu.Probe(info.Helper); err != nil {
		info.Reason = err.Error()
	} else {
		info.Active = true
		info.Reason = "a REPL that has no network namespace of its own gets the private network of its workspace: the same ports for every user, no reach to other workspaces or to the outside"
	}
	netEmuMu.Lock()
	netEmuInfo = info
	netEmuMu.Unlock()
	switch {
	case info.Active:
		log.Printf("INFO: network emulation, mode %s: %s", info.Mode, info.Reason)
	case info.Mode == utils.NetIsolationRequire:
		log.Printf("ERROR: the network emulation is required but not available: %s; REPLs will not start", info.Reason)
	default:
		log.Printf("WARNING: no network emulation, mode %s: %s", info.Mode, info.Reason)
	}
}

// NetEmuCheck is an error when the mode is require and the emulation is not active: then no REPL may start.
func NetEmuCheck() error {
	info := NetEmuStatus()
	if info.Mode == utils.NetIsolationRequire && !info.Active {
		return fmt.Errorf("the network emulation is required (%s=require) but not available: %s", utils.EnvNetIsolation, info.Reason)
	}
	return nil
}

// WantsNetSocket says whether a command line made by GetCommandArgsIn needs the socket the server shares with
// the helper (descriptor 3 of the REPL's process).
func WantsNetSocket(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--net-emulation" {
			return true
		}
	}
	return false
}
