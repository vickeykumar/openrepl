package landlock

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// a file system of a few folders, so that the policy can be built anywhere
type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return 0 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() interface{}   { return nil }

type fakeFS struct {
	dirs    map[string]bool   // folders that exist
	files   map[string]bool   // files that exist
	links   map[string]string // symlinks
	entries map[string][]os.FileInfo
}

func (f *fakeFS) EvalSymlinks(p string) (string, error) {
	if to, ok := f.links[p]; ok {
		p = to
	}
	if f.dirs[p] || f.files[p] {
		return p, nil
	}
	return "", errors.New("no such file")
}

func (f *fakeFS) Stat(p string) (os.FileInfo, error) {
	if f.dirs[p] {
		return fakeInfo{p, true}, nil
	}
	if f.files[p] {
		return fakeInfo{p, false}, nil
	}
	return nil, errors.New("no such file")
}

func (f *fakeFS) ReadDir(p string) ([]os.FileInfo, error) {
	if e, ok := f.entries[p]; ok {
		return e, nil
	}
	return nil, errors.New("no such folder")
}

func newFS(home string, workspaces ...string) *fakeFS {
	f := &fakeFS{dirs: map[string]bool{}, files: map[string]bool{}, links: map[string]string{}, entries: map[string][]os.FileInfo{}}
	for _, d := range []string{"/", "/tmp", "/var/tmp", "/dev/shm", "/usr", "/bin", "/lib", "/etc", "/sys", "/dev/pts", "/proc/sys",
		"/opt", "/opt/gotty", "/opt/gotty/bin", "/opt/gotty/src", "/opt/openrepl", "/opt/cling", "/opt/extra", home} {
		f.dirs[d] = true
	}
	for _, p := range []string{"/dev/null", "/dev/zero", "/dev/tty", "/proc/meminfo"} {
		f.files[p] = true
	}
	for _, w := range workspaces {
		f.dirs[w] = true
	}
	// a database is a file of /opt/gotty itself, the tools are folders
	f.entries["/opt/gotty"] = []os.FileInfo{fakeInfo{"bin", true}, fakeInfo{"src", true}, fakeInfo{"openrepl.db", false}, fakeInfo{"jobfile", false}}
	f.entries["/opt"] = []os.FileInfo{fakeInfo{"gotty", true}, fakeInfo{"openrepl", true}, fakeInfo{"cling", true}, fakeInfo{"extra", true}}
	return f
}

func paths(p *Policy) map[string]uint64 {
	m := map[string]uint64{}
	for _, r := range p.Rules {
		m[r.Path] = r.Rights
	}
	return m
}

func TestAWorkspaceOutsideTmpGetsTmpAndItsOwnFolderOnly(t *testing.T) {
	fs := newFS("/home/openrepl", "/home/openrepl/guest-1", "/home/openrepl/guest-2")
	p, err := Build(Options{Workspace: "/home/openrepl/guest-1", HomeRoot: "/home/openrepl", ABI: 4}, fs)
	if err != nil {
		t.Fatal(err)
	}
	m := paths(p)
	if m["/home/openrepl/guest-1"] != allRights(4) {
		t.Errorf("the workspace has %b, want all the rights", m["/home/openrepl/guest-1"])
	}
	if m["/home/openrepl/guest-1"]&MakeChar == 0 || m["/home/openrepl/guest-1"]&MakeBlock == 0 {
		t.Error("device creation in the workspace was allowed by the owner")
	}
	if _, ok := m["/home/openrepl/guest-2"]; ok {
		t.Error("another workspace has a rule")
	}
	if _, ok := m["/home/openrepl"]; ok {
		t.Error("the folder of all workspaces has a rule")
	}
	if p.TmpClosed {
		t.Error("TmpClosed with the workspaces outside /tmp")
	}
	for _, shared := range []string{"/tmp", "/var/tmp", "/dev/shm"} {
		if m[shared] == 0 {
			t.Errorf("%s is the scratch folder and has no rule", shared)
		}
	}
	if m["/etc"]&WriteFile != 0 || m["/usr"]&WriteFile != 0 || m["/usr"]&Execute == 0 {
		t.Errorf("the system must be read and execute only: /etc %b /usr %b", m["/etc"], m["/usr"])
	}
}

func TestTmpIsLeftOutWhenTheWorkspacesAreInIt(t *testing.T) {
	fs := newFS("/tmp/home", "/tmp/home/guest-1")
	p, err := Build(Options{Workspace: "/tmp/home/guest-1", HomeRoot: "/tmp/home", ABI: 4}, fs)
	if err != nil {
		t.Fatal(err)
	}
	m := paths(p)
	if _, ok := m["/tmp"]; ok {
		t.Fatal("/tmp has a rule: it would open every workspace")
	}
	if !p.TmpClosed {
		t.Error("TmpClosed is not set: the helper would not point TMPDIR into the workspace")
	}
	if len(p.Skipped) == 0 || !strings.Contains(strings.Join(p.Skipped, " "), "/tmp") {
		t.Errorf("the log is not told why /tmp is left out: %v", p.Skipped)
	}
	for path := range m {
		if contains(path, "/tmp/home") && path != "/tmp/home/guest-1" {
			t.Errorf("%s reaches the folder of the workspaces", path)
		}
	}
}

func TestNoRuleReachesTheFolderOfTheWorkspacesWhereverItIs(t *testing.T) {
	for _, home := range []string{"/tmp/home", "/opt/extra/home", "/usr/home", "/etc/homes"} {
		fs := newFS(home, home+"/g")
		p, err := Build(Options{Workspace: home + "/g", HomeRoot: home, ABI: 5}, fs)
		if err != nil {
			t.Fatalf("%s: %v", home, err)
		}
		for _, r := range p.Rules {
			if r.Path != home+"/g" && contains(r.Path, home) {
				t.Errorf("home %s: the rule on %s reaches it", home, r.Path)
			}
		}
	}
}

func TestTheWorkspaceMustBeInsideTheFolderOfTheWorkspaces(t *testing.T) {
	fs := newFS("/home/openrepl", "/home/openrepl/g", "/somewhere/else")
	fs.dirs["/somewhere"] = true
	for name, o := range map[string]Options{
		"outside":        {Workspace: "/somewhere/else", HomeRoot: "/home/openrepl", ABI: 4},
		"the root":       {Workspace: "/home/openrepl", HomeRoot: "/home/openrepl", ABI: 4},
		"empty":          {Workspace: "", HomeRoot: "/home/openrepl", ABI: 4},
		"relative":       {Workspace: "g", HomeRoot: "/home/openrepl", ABI: 4},
		"no root":        {Workspace: "/home/openrepl/g", HomeRoot: "", ABI: 4},
		"no landlock":    {Workspace: "/home/openrepl/g", HomeRoot: "/home/openrepl", ABI: 0},
		"does not exist": {Workspace: "/home/openrepl/nothing", HomeRoot: "/home/openrepl", ABI: 4},
		"a file":         {Workspace: "/dev/null", HomeRoot: "/home/openrepl", ABI: 4},
	} {
		if _, err := Build(o, fs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the root cannot be / or /tmp, whose rules would open everything
	fs2 := newFS("/tmp", "/tmp/g")
	if _, err := Build(Options{Workspace: "/tmp/g", HomeRoot: "/tmp", ABI: 4}, fs2); err == nil {
		t.Error("/tmp as the folder of the workspaces was accepted")
	}
}

func TestASymlinkCannotPointAWorkspaceAtAnotherOne(t *testing.T) {
	fs := newFS("/home/openrepl", "/home/openrepl/guest-1", "/home/openrepl/guest-2")
	fs.links["/home/openrepl/evil"] = "/somewhere/else"
	fs.dirs["/somewhere/else"] = true
	if _, err := Build(Options{Workspace: "/home/openrepl/evil", HomeRoot: "/home/openrepl", ABI: 4}, fs); err == nil {
		t.Error("a workspace that is a symlink out of the root was accepted")
	}
	// a symlink to another workspace is that workspace: the rule is on the real folder
	fs.links["/home/openrepl/alias"] = "/home/openrepl/guest-2"
	fs.dirs["/home/openrepl/alias"] = true
	p, err := Build(Options{Workspace: "/home/openrepl/alias", HomeRoot: "/home/openrepl", ABI: 4}, fs)
	if err == nil {
		// it is allowed to be a workspace of the root, but the rule must name the real folder
		if paths(p)["/home/openrepl/guest-2"] == 0 || paths(p)["/home/openrepl/alias"] != 0 {
			t.Errorf("the rule is not on the real folder: %v", paths(p))
		}
	}
}

func TestOnlyTheToolFoldersOfTheServerAreReadable(t *testing.T) {
	fs := newFS("/home/openrepl", "/home/openrepl/g")
	p, _ := Build(Options{Workspace: "/home/openrepl/g", HomeRoot: "/home/openrepl", ABI: 4}, fs)
	m := paths(p)
	if m["/opt/gotty/bin"] == 0 || m["/opt/gotty/src"] == 0 {
		t.Error("the tool folders of /opt/gotty are not readable")
	}
	if _, ok := m["/opt/gotty"]; ok {
		t.Error("/opt/gotty itself has a rule: its database files would be readable")
	}
	if _, ok := m["/opt/openrepl"]; ok {
		t.Error("/opt/openrepl has a rule")
	}
	if m["/opt/extra"] == 0 {
		t.Error("another folder of /opt is not readable")
	}
}

func TestRightsFollowTheABI(t *testing.T) {
	if allRights(1)&(Refer|Truncate|IoctlDev) != 0 {
		t.Error("ABI 1 has no refer, truncate or ioctl")
	}
	if allRights(2)&Refer == 0 || allRights(2)&Truncate != 0 {
		t.Error("ABI 2 has refer and not truncate")
	}
	if allRights(3)&Truncate == 0 || allRights(3)&IoctlDev != 0 {
		t.Error("ABI 3 has truncate and not ioctl")
	}
	if allRights(5)&IoctlDev == 0 {
		t.Error("ABI 5 has the device ioctl")
	}
	if FileRights(allRights(5))&(ReadDir|RemoveDir|MakeReg|Refer) != 0 {
		t.Error("a rule on a file has rights for folders")
	}
	if FileRights(ReadFile|ReadDir) != ReadFile {
		t.Error("ReadDir was not removed from a file rule")
	}
}

func TestProcIsReadableForAllAndWritableOnlyForTheOwnEntry(t *testing.T) {
	fs := newFS("/home/openrepl", "/home/openrepl/g")
	fs.dirs["/proc"] = true
	fs.dirs["/proc/42"] = true
	p, _ := Build(Options{Workspace: "/home/openrepl/g", HomeRoot: "/home/openrepl", ABI: 4, Self: "42"}, fs)
	m := paths(p)
	if m["/proc"]&ReadFile == 0 || m["/proc"]&WriteFile != 0 {
		t.Errorf("/proc must be read-only: %b", m["/proc"])
	}
	if m["/proc/42"]&WriteFile == 0 {
		t.Errorf("the process' own /proc entry cannot be written: %b", m["/proc/42"])
	}
	// a Debug run may also write /proc (gdb writes the memory of what it debugs there); the others may not
	pd, _ := Build(Options{Workspace: "/home/openrepl/g", HomeRoot: "/home/openrepl", ABI: 4, Debug: true}, fs)
	if paths(pd)["/proc"]&WriteFile == 0 {
		t.Error("a Debug run cannot write /proc")
	}
	// a number that is not one (a path) is not made a rule
	p, _ = Build(Options{Workspace: "/home/openrepl/g", HomeRoot: "/home/openrepl", ABI: 4, Self: "../etc"}, fs)
	for path := range paths(p) {
		if strings.Contains(path, "..") {
			t.Errorf("a rule on %s", path)
		}
	}
}

func TestTheDevicesAreAnAllowlist(t *testing.T) {
	fs := newFS("/home/openrepl", "/home/openrepl/g")
	p, _ := Build(Options{Workspace: "/home/openrepl/g", HomeRoot: "/home/openrepl", ABI: 5}, fs)
	m := paths(p)
	if m["/dev/null"]&WriteFile == 0 {
		t.Error("/dev/null cannot be written: 2>/dev/null would fail")
	}
	if _, ok := m["/dev"]; ok {
		t.Error("/dev itself has a rule")
	}
}
