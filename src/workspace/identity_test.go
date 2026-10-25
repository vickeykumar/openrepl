package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityForIsStableForWorkspace(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "user-space")
	first, err := IdentityFor(base, home)
	if err != nil {
		t.Fatal(err)
	}
	second, err := IdentityFor(base, home)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("identity changed for same workspace: first=%+v second=%+v", first, second)
	}
	if first.UID != first.GID {
		t.Fatalf("workspace UID/GID should match: %+v", first)
	}
}

func TestIdentityForRejectsHomesOutsideBase(t *testing.T) {
	base := t.TempDir()
	for _, home := range []string{
		base,
		filepath.Join(base, "..", "outside"),
		filepath.Join(base, "nested", "home"),
	} {
		if _, err := IdentityFor(base, home); !errors.Is(err, ErrInvalidHome) {
			t.Errorf("IdentityFor(%q) error = %v, want ErrInvalidHome", home, err)
		}
	}
}

func TestRelativePathConfinesPathsToWorkspace(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user-home")
	inside := filepath.Join(home, "New node", "test.go")
	got, err := RelativePath(home, inside)
	if err != nil {
		t.Fatal(err)
	}
	if got != "New node/test.go" {
		t.Fatalf("RelativePath() = %q, want %q", got, "New node/test.go")
	}
	for _, outside := range []string{
		filepath.Dir(home),
		filepath.Join(filepath.Dir(home), "another-user", "secret"),
	} {
		if _, err := RelativePath(home, outside); !errors.Is(err, ErrInvalidHome) {
			t.Errorf("RelativePath(%q) error = %v, want ErrInvalidHome", outside, err)
		}
	}
	if _, err := RelativePath(home, "relative/path"); !errors.Is(err, ErrInvalidHome) {
		t.Errorf("relative candidate error = %v, want ErrInvalidHome", err)
	}
}

func TestPrepareMakesWorkspacePrivate(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("assigning synthetic workspace IDs requires root")
	}
	base := filepath.Join(t.TempDir(), "home")
	home := filepath.Join(base, "private-user")
	if err := os.MkdirAll(filepath.Join(home, "nested"), 0777); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "nested", "shared.txt")
	if err := os.WriteFile(file, []byte("private"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(home, "nested"), 0777); err != nil {
		t.Fatal(err)
	}

	id, err := Prepare(base, home)
	if err != nil {
		t.Fatal(err)
	}
	for path, wantMode := range map[string]os.FileMode{
		home:                          0700,
		filepath.Join(home, "nested"): 0700,
		file:                          0600,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != wantMode {
			t.Errorf("mode(%s) = %#o, want %#o", path, got, wantMode)
		}
		if !ownerMatches(info, id.UID, id.GID) {
			t.Errorf("%s owner does not match workspace identity %+v", path, id)
		}
	}
	baseInfo, err := os.Lstat(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := baseInfo.Mode().Perm(); got != 0711 {
		t.Errorf("home root mode = %#o, want %#o", got, 0711)
	}
}
