// Package appstore is the App Store (LLD 18): libraries and tools installed once on a server, in a shared store, and switched on
// for each signed-in user by a list. Nothing is copied into a workspace. The terminal's filesystem sandbox (Landlock) is given
// exactly the folders on the user's list, so a bundle that is off for a user cannot be read or run by it.
//
//	<dir>/pkgs/                 the download cache, one copy of every package file
//	<dir>/apps/<name>-<ver>/    a complete conda prefix (the package and everything it needs), installed once
//	<dir>/envs/<home>/          manifest.json (the bundles that are on) and env.sh (what a shell sets to use them)
//
// This file is the part that touches the disk and needs no network, no database and no server.
package appstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// DefaultDir is where the store is, unless OPENREPL_STORE_DIR says another place. It is outside /opt on purpose: the sandbox
// gives every terminal read access to the other folders of /opt.
const DefaultDir = "/store"

// markerName is written last into a bundle, so that a bundle that was being installed when the server stopped is never used.
const markerName = ".openrepl-bundle.json"

var (
	appNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	versionPattern = regexp.MustCompile(`^[0-9][A-Za-z0-9._+!-]{0,31}$`)
	bundlePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}-[0-9][A-Za-z0-9._+!-]{0,31}$`)
	homePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	aptPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{1,62}$`)
)

// ValidName says whether s can be the name of a conda package (lower case letters, digits, . _ -).
func ValidName(s string) bool { return appNamePattern.MatchString(s) && !strings.Contains(s, "..") }

// ValidVersion says whether s can be a version of one.
func ValidVersion(s string) bool { return versionPattern.MatchString(s) && !strings.Contains(s, "..") }

// ValidBundle says whether s is the name of a bundle folder: <name>-<version>.
func ValidBundle(s string) bool { return bundlePattern.MatchString(s) && !strings.Contains(s, "..") }

// ValidHome says whether s can name a user's folder in envs (a workspace folder's name).
func ValidHome(s string) bool { return homePattern.MatchString(s) && !strings.Contains(s, "..") }

// ValidApt says whether s can be the name of a Debian package.
func ValidApt(s string) bool { return aptPattern.MatchString(s) }

// BundleID is the folder name of a bundle.
func BundleID(name, version string) string { return name + "-" + version }

// Marker is what a finished bundle says about itself.
type Marker struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
}

// Store is the store on one server.
type Store struct{ Dir string }

func (s Store) AppsDir() string { return filepath.Join(s.Dir, "apps") }
func (s Store) PkgsDir() string { return filepath.Join(s.Dir, "pkgs") }
func (s Store) EnvsDir() string { return filepath.Join(s.Dir, "envs") }

// BundlePath is the folder of a bundle, or "" for a name that is not one.
func (s Store) BundlePath(id string) string {
	if !ValidBundle(id) {
		return ""
	}
	return filepath.Join(s.AppsDir(), id)
}

// HomeDir is the folder of a user's manifest, or "".
func (s Store) HomeDir(home string) string {
	if !ValidHome(home) {
		return ""
	}
	return filepath.Join(s.EnvsDir(), home)
}

// Ready makes the folders of the store.
func (s Store) Ready() error {
	for _, d := range []string{s.AppsDir(), s.PkgsDir(), s.EnvsDir()} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

// HasBundle says whether a bundle is finished (its marker is there).
func (s Store) HasBundle(id string) bool {
	p := s.BundlePath(id)
	if p == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(p, markerName))
	return err == nil
}

// Bundles lists the finished bundles.
func (s Store) Bundles() []string {
	entries, err := os.ReadDir(s.AppsDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && s.HasBundle(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Finish writes the marker of a bundle that was installed completely.
func (s Store) Finish(id, name, version string) (Marker, error) {
	p := s.BundlePath(id)
	if p == "" {
		return Marker{}, fmt.Errorf("%q is not a bundle name", id)
	}
	m := Marker{Name: name, Version: version, Size: DirSize(p)}
	raw, _ := json.Marshal(m)
	return m, os.WriteFile(filepath.Join(p, markerName), raw, 0644)
}

// RemoveBundle deletes a bundle (finished or not).
func (s Store) RemoveBundle(id string) error {
	p := s.BundlePath(id)
	if p == "" {
		return fmt.Errorf("%q is not a bundle name", id)
	}
	return os.RemoveAll(p)
}

// Size is the disk the store uses: every file counted once, however many hard links there are to it.
func (s Store) Size() int64 { return DirSize(s.Dir) }

// DirSize is the size of the files below dir, each inode counted once (a bundle is hard-linked from the cache).
func DirSize(dir string) int64 {
	seen := map[[2]uint64]bool{}
	var total int64
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			key := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			if seen[key] {
				return nil
			}
			seen[key] = true
		}
		total += info.Size()
		return nil
	})
	return total
}

// Manifest is the list of bundles that are on for a user, [] when there is none. A bad entry is an error, never skipped.
func (s Store) Manifest(home string) ([]string, error) {
	d := s.HomeDir(home)
	if d == "" {
		return nil, fmt.Errorf("%q is not a home", home)
	}
	return ReadManifest(filepath.Join(d, "manifest.json"))
}

// ReadManifest reads a manifest file: a JSON list of bundle names. A file that is not there is an empty list.
func ReadManifest(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil, fmt.Errorf("the manifest %s is not a list of names: %v", path, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if !ValidBundle(id) {
			return nil, fmt.Errorf("the manifest %s has %q, which is not a bundle name", path, id)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// WriteManifest saves a user's list and the env.sh that goes with it. Only finished bundles may be on it. The files are written
// whole and then renamed, so a terminal that starts at that moment reads the old list or the new one.
func (s Store) WriteManifest(home string, ids []string) error {
	d := s.HomeDir(home)
	if d == "" {
		return fmt.Errorf("%q is not a home", home)
	}
	seen := map[string]bool{}
	var clean []string
	for _, id := range ids {
		if !s.HasBundle(id) {
			return fmt.Errorf("%s is not installed on this server", id)
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	if err := os.MkdirAll(d, 0755); err != nil {
		return err
	}
	if clean == nil {
		clean = []string{}
	}
	raw, _ := json.Marshal(clean)
	if err := writeAtomic(filepath.Join(d, "manifest.json"), raw); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d, "env.sh"), []byte(EnvScript(s.Dir, clean)))
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Homes lists the users that have a manifest on this server.
func (s Store) Homes() []string {
	entries, err := os.ReadDir(s.EnvsDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidHome(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}
