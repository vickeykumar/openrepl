package appstore

import (
	"os"
	"path/filepath"
	"strings"
)

// The trim (LLD 18): a step after any install, for every manager, that deletes what an app does not need to run: documentation, manual pages,
// translations, bytecode, test folders, static libraries, source maps and the markdown files of Node packages. Licence files are never deleted.
// A keep-list protects what other apps compile against (the headers and static libraries of a C or C++ library); an entry's own `keep` adds to it.

// TrimOptions say what a trim leaves alone.
type TrimOptions struct {
	Keep       []string // globs, relative to the bundle; a match (or a folder that holds one) is kept
	KeepStatic bool     // leave the *.a files (a C or C++ library is linked against them)
}

// TrimReport is what a trim did.
type TrimReport struct {
	BeforeMB, AfterMB float64
	Files             int
}

// the folders (relative to a root: the bundle, its pixi prefix or its usr) that are removed whole, except their licence files
var trimDirs = []string{"share/doc", "share/man", "share/info", "share/locale", "share/lintian", "share/gtk-doc", "share/help", "man", "info", "doc"}

func isLicence(base string) bool {
	b := strings.ToLower(base)
	for _, p := range []string{"license", "licence", "copying", "copyright", "notice", "unlicense", "authors"} {
		if strings.HasPrefix(b, p) {
			return true
		}
	}
	return false
}

// kept says whether rel is on the keep-list or inside a folder that is.
func (o TrimOptions) kept(rel string) bool {
	for _, g := range o.Keep {
		if ok, _ := filepath.Match(g, rel); ok || rel == g || strings.HasPrefix(rel, strings.TrimSuffix(g, "/")+"/") {
			return true
		}
	}
	return false
}

// holdsKept says whether the folder rel has something on the keep-list inside it (it is then never deleted whole).
func (o TrimOptions) holdsKept(rel string) bool {
	for _, g := range o.Keep {
		if strings.HasPrefix(strings.TrimSuffix(g, "/")+"/", rel+"/") {
			return true
		}
	}
	return false
}

// TrimBundle trims the bundle folder dest. It never leaves it (a link that points out is not followed) and never touches a file it cannot
// name: it only deletes.
func TrimBundle(dest string, o TrimOptions) TrimReport {
	rep := TrimReport{BeforeMB: dirMB(dest)}
	roots := []string{dest, filepath.Join(dest, "usr")}
	if p := PrefixOf(dest); p != dest {
		roots = append(roots, p)
	}
	for _, root := range roots {
		for _, d := range trimDirs {
			rm := filepath.Join(root, filepath.FromSlash(d))
			if fi, err := os.Lstat(rm); err != nil || !fi.IsDir() {
				continue
			}
			rep.Files += removeExceptLicences(rm, dest, o)
		}
	}
	filepath.WalkDir(dest, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dest, p)
		rel = filepath.ToSlash(rel)
		if o.kept(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		inPkgs := strings.Contains(rel, "/site-packages/") || strings.Contains(rel, "node_modules/")
		name := d.Name()
		if d.IsDir() {
			if (name == "__pycache__" || (inPkgs && (name == "test" || name == "tests"))) && !o.holdsKept(rel) {
				rep.Files += countFiles(p)
				os.RemoveAll(p)
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 && !strings.HasSuffix(name, ".a") {
			return nil
		}
		drop := (strings.HasSuffix(name, ".a") && !o.KeepStatic) ||
			(strings.Contains(rel, "node_modules/") && (strings.HasSuffix(name, ".map") || (strings.HasSuffix(strings.ToLower(name), ".md") && !isLicence(name))))
		if drop && os.Remove(p) == nil {
			rep.Files++
		}
		return nil
	})
	rep.AfterMB = dirMB(dest)
	return rep
}

func countFiles(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// removeExceptLicences deletes the files below dir that are not licences and not kept, then the folders that are left empty.
func removeExceptLicences(dir, bundle string, o TrimOptions) int {
	n := 0
	var dirs []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(bundle, p)
		if o.kept(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if !isLicence(d.Name()) && os.Remove(p) == nil {
			n++
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // only if it is empty
	}
	return n
}
