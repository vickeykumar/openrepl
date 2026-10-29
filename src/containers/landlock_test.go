package containers

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"utils"
)

func setLandlock(t *testing.T, info LandlockInfo) {
	t.Helper()
	landlockMu.Lock()
	saved := landlockInfo
	landlockInfo = info
	landlockMu.Unlock()
	t.Cleanup(func() {
		landlockMu.Lock()
		landlockInfo = saved
		landlockMu.Unlock()
	})
}

func TestACommandIsRunByTheHelperWhenTheSandboxIsOn(t *testing.T) {
	saved := utils.HOME_DIR
	defer func() { utils.HOME_DIR = saved }()
	utils.HOME_DIR = "/home/openrepl/"
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: true, Helper: "/usr/local/bin/landlock-helper", ABI: 4})
	got := wrapWithLandlock([]string{"/usr/bin/python3", "-i"}, "/home/openrepl/guest-1", false)
	want := []string{"/usr/local/bin/landlock-helper", "--workspace", "/home/openrepl/guest-1", "--home-root", "/home/openrepl", "--", "/usr/bin/python3", "-i"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestOnlyADebugRunGetsTheDebugFlag(t *testing.T) {
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: true, Helper: "/h", ABI: 4})
	plain := wrapWithLandlock([]string{"/bin/bash"}, "/home/x/g", false)
	debug := wrapWithLandlock([]string{"/bin/bash"}, "/home/x/g", true)
	if strings.Contains(strings.Join(plain, " "), "--debug") {
		t.Errorf("a plain run got the debug flag: %v", plain)
	}
	if !strings.Contains(strings.Join(debug, " "), "--debug --") {
		t.Errorf("a Debug run did not get it before the --: %v", debug)
	}
}

func TestACommandIsLeftAloneWhenThereIsNoSandboxOrNoWorkspace(t *testing.T) {
	cmd := []string{"/usr/bin/python3"}
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: false, Reason: "no Landlock"})
	if got := wrapWithLandlock(cmd, "/home/x/g", false); !reflect.DeepEqual(got, cmd) {
		t.Errorf("wrapped without a sandbox: %v", got)
	}
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: true, Helper: "/h"})
	if got := wrapWithLandlock(cmd, "", false); !reflect.DeepEqual(got, cmd) {
		t.Errorf("wrapped without a workspace: %v", got)
	}
}

func TestRequireStopsTheStartOfAREPLWithoutTheSandbox(t *testing.T) {
	setLandlock(t, LandlockInfo{Mode: utils.LandlockRequire, Active: false, Reason: "this kernel has no Landlock"})
	err := LandlockCheck()
	if err == nil || !strings.Contains(err.Error(), "no Landlock") {
		t.Errorf("require without the sandbox was allowed: %v", err)
	}
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: false})
	if err := LandlockCheck(); err != nil {
		t.Errorf("auto stopped a REPL: %v", err)
	}
	setLandlock(t, LandlockInfo{Mode: utils.LandlockRequire, Active: true})
	if err := LandlockCheck(); err != nil {
		t.Errorf("require with the sandbox stopped a REPL: %v", err)
	}
}

func TestOffMeansNoSandboxAndAReason(t *testing.T) {
	os.Setenv(utils.EnvLandlock, "off")
	defer os.Unsetenv(utils.EnvLandlock)
	setLandlock(t, LandlockInfo{})
	InitLandlock()
	info := LandlockStatus()
	if info.Active || info.Mode != utils.LandlockOff || !strings.Contains(info.Reason, "off") {
		t.Errorf("off: %+v", info)
	}
}

func TestAForkRunsTheHelperInsideTheNamespacesItJoined(t *testing.T) {
	saved := utils.HOME_DIR
	defer func() { utils.HOME_DIR = saved }()
	utils.HOME_DIR = "/home/openrepl/"
	setLandlock(t, LandlockInfo{Mode: utils.LandlockAuto, Active: true, Helper: "/usr/local/bin/landlock-helper", ABI: 4})
	got := GetCommandArgsIn("python", nil, 4242, map[string][]string{}, "/home/openrepl/guest-1")
	if len(got) < 3 || !strings.HasSuffix(got[0], "nsenter") {
		t.Fatalf("a fork must start with nsenter: %v", got)
	}
	helper := -1
	for i, a := range got {
		if a == "/usr/local/bin/landlock-helper" {
			helper = i
		}
	}
	if helper < 1 {
		t.Fatalf("the helper is not in the command of a fork: %v", got)
	}
	// nsenter first: the helper sets no_new_privs, which a setuid nsenter could not survive
	if !strings.HasPrefix(got[1], "-t") {
		t.Errorf("nsenter's options do not come first: %v", got)
	}
}
