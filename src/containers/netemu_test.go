package containers

import (
	"reflect"
	"strings"
	"testing"

	"utils"
)

func setNetEmu(t *testing.T, info NetEmuInfo) {
	t.Helper()
	netEmuMu.Lock()
	saved := netEmuInfo
	netEmuInfo = info
	netEmuMu.Unlock()
	t.Cleanup(func() {
		netEmuMu.Lock()
		netEmuInfo = saved
		netEmuMu.Unlock()
	})
}

func TestTheNetworkEmulationIsAskedOnlyOfAREPLWithoutNamespaces(t *testing.T) {
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: true, Helper: "/h", ABI: 4})
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationAuto, Active: true, Helper: "/h"})
	// scopeSignals is "this REPL has no namespaces of its own": then it gets the private network
	with := strings.Join(wrapWithLandlock([]string{"/bin/bash"}, "/home/x/g", false, true), " ")
	if !strings.Contains(with, "--net-emulation -- /bin/bash") {
		t.Errorf("a REPL without namespaces did not get the network emulation: %s", with)
	}
	// a REPL that has namespaces of its own (the cgroup sandbox) has a kernel network namespace already
	without := strings.Join(wrapWithLandlock([]string{"/bin/bash"}, "/home/x/g", false, false), " ")
	if strings.Contains(without, "--net-emulation") {
		t.Errorf("a REPL with its own namespaces got the emulation too: %s", without)
	}
}

func TestTheNetworkEmulationRunsWithoutTheFilesystemSandbox(t *testing.T) {
	setLandlock(t, LandlockInfo{Mode: utils.LandlockOff, Active: false, Reason: "off"})
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationAuto, Active: true, Helper: "/usr/local/bin/landlock-helper"})
	got := wrapWithLandlock([]string{"/usr/bin/python3", "-i"}, "/home/x/g", false, true)
	want := []string{"/usr/local/bin/landlock-helper", "--no-landlock", "--net-emulation", "--", "/usr/bin/python3", "-i"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestAnAdminAndAnInactiveEmulationLeaveTheCommandAlone(t *testing.T) {
	cmd := []string{"/usr/bin/python3"}
	setLandlock(t, LandlockInfo{Mode: utils.LandlockOff, Active: false})
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationAuto, Active: true, Helper: "/h"})
	if got := wrapWithLandlock(cmd, "", false, true); !reflect.DeepEqual(got, cmd) {
		t.Errorf("an admin's REPL (no workspace) was wrapped: %v", got)
	}
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationAuto, Active: false, Reason: "no"})
	if got := wrapWithLandlock(cmd, "/home/x/g", false, true); !reflect.DeepEqual(got, cmd) {
		t.Errorf("wrapped although the emulation is not active: %v", got)
	}
}

func TestOnlyACommandThatAsksForTheEmulationNeedsTheSocket(t *testing.T) {
	for args, want := range map[string]bool{
		"/h --workspace /w --home-root /r --max-file-mb 50 --net-emulation -- /bin/bash": true,
		"/h --no-landlock --net-emulation -- /bin/bash":                                  true,
		"/h --workspace /w --home-root /r -- /bin/bash":                                  false,
		"/bin/bash --net-emulation":                                                      true,
		"/bin/bash -c echo -- --net-emulation":                                           false,
		"nsenter -t5 /h --net-emulation -- /bin/bash":                                    true,
		"": false,
	} {
		if got := WantsNetSocket(strings.Fields(args)); got != want {
			t.Errorf("%q: %v, want %v", args, got, want)
		}
	}
}

func TestTheRequiredEmulationStopsREPLsWhenItIsNotThere(t *testing.T) {
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationRequire, Active: false, Reason: "no kernel support"})
	if err := NetEmuCheck(); err == nil || !strings.Contains(err.Error(), "no kernel support") {
		t.Errorf("require without the emulation: %v", err)
	}
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationAuto, Active: false})
	if err := NetEmuCheck(); err != nil {
		t.Errorf("auto without the emulation stopped the REPLs: %v", err)
	}
	setNetEmu(t, NetEmuInfo{Mode: utils.NetIsolationRequire, Active: true})
	if err := NetEmuCheck(); err != nil {
		t.Errorf("require with the emulation: %v", err)
	}
}
