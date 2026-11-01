package backup

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

	"github.com/nobonobo/unqlitego"
)

// An archive is a gzipped tar of the files that matter, flat (no folders), with MANIFEST.json first: the name
// of the deployment, when it was made and the size and SHA-256 of each file. The manifest is what a restore
// checks every file against.

const (
	manifestName = "MANIFEST.json"
	// the most an archive may hold, and the most one file in it may be: a backup that grows past this is
	// reported, not uploaded
	maxArchiveBytes = 256 << 20
	maxFileBytes    = 200 << 20
)

// FileInfo is a file in an archive.
type FileInfo struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes an archive.
type Manifest struct {
	Version     int        `json:"version"`
	Name        string     `json:"name"`
	CreatedAt   time.Time  `json:"createdAt"`
	Fingerprint string     `json:"fingerprint"`
	Files       []FileInfo `json:"files"`
}

// Sources lists the names of the files in dir that a backup holds. With the databases in files (remote is
// empty) that is every database, the settings, the audit log and the usage totals; when MongoDB or Firestore
// holds the databases (and the settings), only the two files that are never there: the audit log and the
// totals. What is not there is not listed.
func Sources(dir, remote string) []string {
	var names []string
	if remote == "" {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.db"))
		for _, m := range matches {
			names = append(names, filepath.Base(m))
		}
		names = append(names, "settings.json")
	}
	names = append(names, "admin-audit.jsonl", "admin-stats.json")
	var out []string
	for _, n := range names {
		if fi, err := os.Stat(filepath.Join(dir, n)); err == nil && fi.Mode().IsRegular() {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// fingerprint says whether the files changed: a hash of their names, sizes and modification times.
func fingerprint(dir string, names []string) string {
	h := sha256.New()
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s|%d|%d\n", n, fi.Size(), fi.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ErrInconsistent is what Build answers when a database file is not in a state that can be opened: it was
// copied in the middle of a write. The next run tries again.
var ErrInconsistent = errors.New("backup: a database file was copied in the middle of a write")

// unqlitePageSize is the page size of the databases (unqlite's default): a copy that is not a whole number of
// pages was cut.
const unqlitePageSize = 4096

// verifyDB checks a copy of a database: it begins as an unqlite file does, is a whole number of pages, opens,
// and every record can be read. (unqlite opens a file that is not a database without a word, so opening alone
// proves nothing.)
func verifyDB(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	head := make([]byte, 7)
	_, herr := io.ReadFull(f, head)
	fi, serr := f.Stat()
	f.Close()
	if herr != nil || string(head) != "unqlite" {
		return errors.New("it does not begin as a database file does")
	}
	if serr != nil || fi.Size()%unqlitePageSize != 0 {
		return errors.New("it is not a whole number of pages")
	}
	db, err := unqlitego.NewDatabase(path)
	if err != nil {
		return err
	}
	defer db.Close()
	cursor, err := db.NewCursor()
	if err != nil {
		return err
	}
	defer cursor.Close()
	if err := cursor.First(); err != nil {
		return nil // an empty database
	}
	for cursor.IsValid() {
		if _, err := cursor.Key(); err != nil {
			return err
		}
		if _, err := cursor.Value(); err != nil {
			return err
		}
		if err := cursor.Next(); err != nil {
			return nil
		}
	}
	return nil
}

// copyFile copies src to a new file in tmp and returns the path.
func copyFile(src, tmp string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := ioutil.TempFile(tmp, "copy-*")
	if err != nil {
		return "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.LimitReader(in, maxFileBytes+1)); err != nil {
		return "", err
	}
	return out.Name(), nil
}

// afterCopyHook is for tests: it runs between the copy of a file and the check that the file did not change.
var afterCopyHook func(path string)

// Build makes the archive of the named files of dir. A database is copied to a folder of its own and the copy
// is opened and read before it goes in, so that a file caught in the middle of a write is never archived
// (ErrInconsistent, tried again by the caller).
func Build(dir, name string, names []string, now time.Time) ([]byte, Manifest, error) {
	tmp, err := ioutil.TempDir("", "openrepl-backup-")
	if err != nil {
		return nil, Manifest{}, err
	}
	defer os.RemoveAll(tmp)
	m := Manifest{Version: 1, Name: name, CreatedAt: now.UTC(), Fingerprint: fingerprint(dir, names)}
	type piece struct {
		name string
		data []byte
	}
	var pieces []piece
	total := int64(0)
	for _, n := range names {
		orig := filepath.Join(dir, n)
		before, err := os.Stat(orig)
		if err != nil {
			return nil, m, err
		}
		copied, err := copyFile(orig, tmp)
		if err != nil {
			return nil, m, err
		}
		if afterCopyHook != nil {
			afterCopyHook(orig)
		}
		// the file must not have been written while it was copied: then the copy is a picture of a moment
		if after, err := os.Stat(orig); err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
			return nil, m, fmt.Errorf("%w (%s changed while it was copied)", ErrInconsistent, n)
		}
		if strings.HasSuffix(n, ".db") {
			if err := verifyDB(copied); err != nil {
				return nil, m, fmt.Errorf("%w (%s: %v)", ErrInconsistent, n, err)
			}
		}
		data, err := ioutil.ReadFile(copied)
		if err != nil {
			return nil, m, err
		}
		if int64(len(data)) > maxFileBytes {
			return nil, m, fmt.Errorf("backup: %s is larger than %d MB", n, maxFileBytes>>20)
		}
		total += int64(len(data))
		sum := sha256.Sum256(data)
		m.Files = append(m.Files, FileInfo{Name: n, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
		pieces = append(pieces, piece{n, data})
	}
	if total > maxArchiveBytes {
		return nil, m, fmt.Errorf("backup: the files are %d MB, more than the %d MB a backup may hold", total>>20, maxArchiveBytes>>20)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	put := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), ModTime: now.UTC(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	manifest, _ := json.MarshalIndent(m, "", "  ")
	if err := put(manifestName, manifest); err != nil {
		return nil, m, err
	}
	for _, p := range pieces {
		if err := put(p.name, p.data); err != nil {
			return nil, m, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, m, err
	}
	if err := gz.Close(); err != nil {
		return nil, m, err
	}
	return buf.Bytes(), m, nil
}

// SHA256 is the hash of an archive, as hex.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Open reads an archive, checks every file against the manifest and returns them. A name with a folder in it,
// a file the manifest does not list, a missing file, a wrong size or hash, or an archive that is too large is
// refused: nothing of it is used.
func Open(archive []byte) (Manifest, map[string][]byte, error) {
	var m Manifest
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return m, nil, fmt.Errorf("backup: not an archive: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	total := int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, fmt.Errorf("backup: the archive is damaged: %v", err)
		}
		if h.Typeflag != tar.TypeReg {
			return m, nil, fmt.Errorf("backup: %q is not a plain file", h.Name)
		}
		if h.Name == "" || h.Name != filepath.Base(h.Name) || h.Name == "." || h.Name == ".." || strings.ContainsAny(h.Name, `/\`) {
			return m, nil, fmt.Errorf("backup: %q is not a name an archive may have", h.Name)
		}
		if h.Size > maxFileBytes || total+h.Size > maxArchiveBytes {
			return m, nil, errors.New("backup: the archive is larger than a backup may be")
		}
		data, err := ioutil.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil || int64(len(data)) != h.Size {
			return m, nil, fmt.Errorf("backup: %q could not be read whole", h.Name)
		}
		total += h.Size
		if _, dup := files[h.Name]; dup {
			return m, nil, fmt.Errorf("backup: %q is in the archive twice", h.Name)
		}
		files[h.Name] = data
	}
	raw, ok := files[manifestName]
	if !ok {
		return m, nil, errors.New("backup: the archive has no manifest")
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Version != 1 {
		return m, nil, errors.New("backup: the manifest cannot be read")
	}
	delete(files, manifestName)
	listed := map[string]bool{}
	for _, f := range m.Files {
		listed[f.Name] = true
		data, ok := files[f.Name]
		if !ok {
			return m, nil, fmt.Errorf("backup: %s is listed and not in the archive", f.Name)
		}
		if int64(len(data)) != f.Size || SHA256(data) != f.SHA256 {
			return m, nil, fmt.Errorf("backup: %s does not match its manifest", f.Name)
		}
	}
	for n := range files {
		if !listed[n] {
			return m, nil, fmt.Errorf("backup: %s is in the archive and not in its manifest", n)
		}
	}
	return m, files, nil
}
