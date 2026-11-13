package homesnap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A snapshot of a home is a gzipped tar of its files with their folders, MANIFEST.json first. Only plain files
// and folders go in: a link, a socket or a device never does, and nothing that begins with ".wsync-" (those
// are the sync engine's own). A file larger than MaxFileBytes (sync does not carry it either) and a home
// larger than MaxHomeBytes are left out and reported.

const (
	manifestName = "MANIFEST.json"
	// MaxFileBytes and MaxHomeBytes bound a snapshot (the workspace quota is 50 MB, utils.WorkspaceQuotaMB)
	MaxFileBytes = 50 << 20
	MaxHomeBytes = 100 << 20
	maxEntries   = 20000
)

// Entry is a file or a folder of a snapshot.
type Entry struct {
	Path   string `json:"path"` // relative, with "/", a folder ends with "/"
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Mode   uint32 `json:"mode"`
}

// Manifest describes a snapshot.
type Manifest struct {
	Version    int       `json:"version"`
	Home       string    `json:"home"`
	Generation int64     `json:"generation"`
	Writer     string    `json:"writer"`
	CreatedAt  time.Time `json:"createdAt"`
	Entries    []Entry   `json:"entries"`
	Skipped    []string  `json:"skipped,omitempty"` // what was left out, and why
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ErrTooBig is what Build answers for a home that is larger than MaxHomeBytes.
var ErrTooBig = errors.New("homesnap: the workspace is larger than a snapshot may be")

// skipName says that a name is never part of a snapshot.
func skipName(name string) bool { return strings.HasPrefix(name, ".wsync-") }

// scan lists what a snapshot of dir would hold (files with size and modification time), without reading it.
// The fingerprint changes when anything in it does.
func scan(dir string) (paths []string, fp string, err error) {
	h := sha256.New()
	count := 0
	werr := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // gone while it was looked at
			}
			return err
		}
		if p == dir {
			return nil
		}
		if skipName(fi.Name()) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !fi.Mode().IsRegular() && !fi.IsDir() {
			return nil // a link, a socket, a device
		}
		if count++; count > maxEntries {
			return errors.New("homesnap: too many files in the workspace")
		}
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s|%d|%d|%v\n", rel, fi.Size(), fi.ModTime().UnixNano(), fi.IsDir())
		paths = append(paths, rel)
		return nil
	})
	if werr != nil {
		return nil, "", werr
	}
	sort.Strings(paths)
	return paths, hex.EncodeToString(h.Sum(nil))[:16], nil
}

// newest is the latest modification time of anything in the home (zero for an empty one).
func newest(dir string) time.Time {
	var latest time.Time
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if skipName(fi.Name()) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// only what a snapshot holds: a link or a socket changing is no reason to wait
		if (fi.Mode().IsRegular() || fi.IsDir()) && fi.ModTime().After(latest) {
			latest = fi.ModTime()
		}
		return nil
	})
	return latest
}

// Build makes the snapshot of the home in dir. A file that changes while it is read is left out (it is read
// again at the next snapshot, its modification time having changed the fingerprint).
func Build(dir, home string, generation int64, writer string, now time.Time) ([]byte, Manifest, error) {
	m := Manifest{Version: 1, Home: home, Generation: generation, Writer: writer, CreatedAt: now.UTC()}
	paths, _, err := scan(dir)
	if err != nil {
		return nil, m, err
	}
	type piece struct {
		entry Entry
		data  []byte
	}
	var pieces []piece
	total := int64(0)
	for _, rel := range paths {
		full := filepath.Join(dir, rel)
		before, err := os.Lstat(full)
		if err != nil {
			continue // gone
		}
		slash := filepath.ToSlash(rel)
		if before.IsDir() {
			pieces = append(pieces, piece{entry: Entry{Path: slash + "/", Mode: uint32(before.Mode().Perm())}})
			continue
		}
		if !before.Mode().IsRegular() {
			continue
		}
		if before.Size() > MaxFileBytes {
			m.Skipped = append(m.Skipped, slash+": larger than 50 MB")
			continue
		}
		data, err := ioutil.ReadFile(full)
		if err != nil {
			m.Skipped = append(m.Skipped, slash+": "+err.Error())
			continue
		}
		after, err := os.Lstat(full)
		if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || int64(len(data)) != before.Size() {
			m.Skipped = append(m.Skipped, slash+": changed while it was read")
			continue
		}
		if total += int64(len(data)); total > MaxHomeBytes {
			return nil, m, ErrTooBig
		}
		pieces = append(pieces, piece{entry: Entry{Path: slash, Size: int64(len(data)), SHA256: sum(data), Mode: uint32(before.Mode().Perm())}, data: data})
	}
	for _, p := range pieces {
		m.Entries = append(m.Entries, p.entry)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	raw, _ := json.MarshalIndent(m, "", " ")
	if err := tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0600, Size: int64(len(raw)), Typeflag: tar.TypeReg, ModTime: now}); err != nil {
		return nil, m, err
	}
	tw.Write(raw)
	for _, p := range pieces {
		if strings.HasSuffix(p.entry.Path, "/") {
			tw.WriteHeader(&tar.Header{Name: p.entry.Path, Mode: int64(p.entry.Mode), Typeflag: tar.TypeDir, ModTime: now})
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: p.entry.Path, Mode: int64(p.entry.Mode), Size: int64(len(p.data)), Typeflag: tar.TypeReg, ModTime: now}); err != nil {
			return nil, m, err
		}
		tw.Write(p.data)
	}
	if err := tw.Close(); err != nil {
		return nil, m, err
	}
	if err := gz.Close(); err != nil {
		return nil, m, err
	}
	return buf.Bytes(), m, nil
}

// safeRel says that a path of an archive may be written below a folder: relative, clean, no "..", no
// backslash, nothing reserved.
func safeRel(p string) (string, bool) {
	p = strings.TrimSuffix(p, "/")
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return "", false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." || skipName(part) {
			return "", false
		}
	}
	return filepath.FromSlash(p), true
}

// Extract checks a snapshot against its manifest and writes it into the new folder dest (which must not exist
// yet). A path outside the folder, a link, a file the manifest does not list, a wrong size or hash, or too
// much data is refused, and dest is removed.
func Extract(archive []byte, dest string) (m Manifest, err error) {
	return extract(archive, dest, false)
}

// extractForced is Extract for the last resort (every snapshot of a home fails the check against its manifest): the
// manifest is not held against the files, so what is in the archive is written. The checks that keep a path inside the folder
// (no link, no path that leaves it, the size limits) stay.
// errManifest marks the failures of the check of the files against the manifest (and not those that keep a path inside the folder).
var errManifest = errors.New("homesnap")

func extractForced(archive []byte, dest string) (Manifest, error) {
	return extract(archive, dest, true)
}

func extract(archive []byte, dest string, forced bool) (m Manifest, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return m, fmt.Errorf("homesnap: not a snapshot: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	type file struct {
		data []byte
		mode os.FileMode
		dir  bool
	}
	files := map[string]file{}
	var order []string
	total := int64(0)
	var raw []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("homesnap: the snapshot is damaged: %v", err)
		}
		if h.Name == manifestName {
			raw, _ = ioutil.ReadAll(io.LimitReader(tr, 16<<20))
			continue
		}
		rel, ok := safeRel(h.Name)
		if !ok {
			return m, fmt.Errorf("homesnap: %q is not a path a snapshot may have", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			files[rel] = file{dir: true, mode: os.FileMode(h.Mode).Perm()}
		case tar.TypeReg:
			if h.Size > MaxFileBytes || total+h.Size > MaxHomeBytes {
				return m, errors.New("homesnap: the snapshot is larger than a snapshot may be")
			}
			data, err := ioutil.ReadAll(io.LimitReader(tr, h.Size+1))
			if err != nil || int64(len(data)) != h.Size {
				return m, fmt.Errorf("homesnap: %q could not be read whole", h.Name)
			}
			total += h.Size
			files[rel] = file{data: data, mode: os.FileMode(h.Mode).Perm()}
		default:
			return m, fmt.Errorf("homesnap: %q is not a plain file or a folder", h.Name)
		}
		if _, dup := files[rel]; dup && len(order) > 0 && order[len(order)-1] == rel {
			return m, fmt.Errorf("homesnap: %q is in the snapshot twice", h.Name)
		}
		order = append(order, rel)
		if len(order) > maxEntries {
			return m, errors.New("homesnap: too many files in the snapshot")
		}
	}
	if forced {
		json.Unmarshal(raw, &m) // what can be read of it; it is not needed
	} else if raw == nil || json.Unmarshal(raw, &m) != nil || m.Version != 1 {
		return m, fmt.Errorf("%w: the snapshot has no readable manifest", errManifest)
	}
	// every entry of the manifest is there and matches, and nothing else is
	seen := map[string]bool{}
	for _, e := range m.Entries {
		if forced {
			break
		}
		rel, ok := safeRel(e.Path)
		if !ok {
			return m, fmt.Errorf("homesnap: %q is not a path a snapshot may have", e.Path)
		}
		f, ok := files[rel]
		if !ok {
			return m, fmt.Errorf("%w: %s is in the manifest and not in the snapshot", errManifest, e.Path)
		}
		if strings.HasSuffix(e.Path, "/") != f.dir {
			return m, fmt.Errorf("%w: %s is a file in one place and a folder in the other", errManifest, e.Path)
		}
		if !f.dir && (int64(len(f.data)) != e.Size || sum(f.data) != e.SHA256) {
			return m, fmt.Errorf("%w: %s does not match its manifest", errManifest, e.Path)
		}
		seen[rel] = true
	}
	for rel := range files {
		if !seen[rel] && !forced {
			return m, fmt.Errorf("%w: %s is in the snapshot and not in its manifest", errManifest, rel)
		}
	}
	if err := os.Mkdir(dest, 0755); err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dest)
		}
	}()
	// folders first, then files (a folder is made for a file that has none listed)
	var names []string
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		f := files[rel]
		target := filepath.Join(dest, rel)
		if f.dir {
			if err = os.MkdirAll(target, 0755); err != nil {
				return m, err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return m, err
		}
		mode := f.mode
		if mode == 0 {
			mode = 0644
		}
		if err = ioutil.WriteFile(target, f.data, mode); err != nil {
			return m, err
		}
		os.Chmod(target, mode)
	}
	return m, nil
}
