package scratch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeVarsUsageClearSweep(t *testing.T) {
	root := t.TempDir()
	dir, err := Make(root, "ann")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, filepath.Join(root, "ann")+"/t-") {
		t.Fatal(dir)
	}
	for _, v := range Vars(dir) {
		if i := strings.IndexByte(v, '='); i < 0 || (!strings.HasPrefix(v[i+1:], dir) && !strings.HasPrefix(v, "OPENREPL")) {
			t.Errorf("a variable that does not point into the scratch folder: %s", v)
		}
	}
	os.WriteFile(filepath.Join(dir, "cache", "pyc"), make([]byte, 100000), 0600)
	os.WriteFile(filepath.Join(dir, "build", "obj"), make([]byte, 100000), 0600)
	if u := Usage(dir); u < 200000 {
		t.Errorf("usage %d", u)
	}
	ClearCaches(dir)
	if _, err := os.Stat(filepath.Join(dir, "cache", "pyc")); err == nil {
		t.Error("a cache was not cleared")
	}
	if _, err := os.Stat(filepath.Join(dir, "build", "obj")); err != nil {
		t.Error("a build file was cleared before the caches were enough")
	}
	// this process is alive: its folder stays; one of a process that is not there goes
	if n := Sweep(root); n != 0 {
		t.Errorf("swept a live terminal: %d", n)
	}
	dead := filepath.Join(root, "bob", "t-0000abcd")
	os.MkdirAll(dead, 0700)
	os.WriteFile(filepath.Join(dead, ".pid"), []byte("999999 1\n"), 0600)
	if n := Sweep(root); n != 1 {
		t.Errorf("swept %d", n)
	}
	if _, err := os.Stat(dead); err == nil {
		t.Error("an ended terminal's folder stays")
	}
	for _, bad := range [][2]string{{"/x", "../a"}, {"", "a"}, {root, "a/b"}, {root, ".."}} {
		if _, err := Make(bad[0], bad[1]); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}
