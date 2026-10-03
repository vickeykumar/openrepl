package wsync

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sort"
	"time"
)

const (
	// staleTemp is how old a leftover temporary file must be before a scan
	// removes it; a younger one may still be being written.
	staleTemp = 10 * time.Minute
	// hashAttempts is how often a file that keeps changing is re-read.
	hashAttempts = 3
)

// ScanOptions tunes a scan.
type ScanOptions struct {
	// Base, if set, lets the scan reuse the hash of a file whose size and
	// modification time are those in the record, instead of reading it.
	Base *Record
	// MaxFileSize skips larger files. Zero means DefaultMaxFileSize.
	MaxFileSize int64
	// Now is the time used to age temporary files. Zero means time.Now().
	Now time.Time
}

// Skip is a path a scan left out, and why.
type Skip struct {
	Path   string
	Reason string
}

// ScanResult is the current state of a home.
type ScanResult struct {
	Entries map[string]Entry
	Skipped []Skip
}

// Scan walks the home without following links and returns what it finds.
// Sockets, FIFOs, device files, files over the size limit, files that keep
// changing, and files being received are left out (the last are removed if
// they are old). A path that disappears during the walk is ignored.
func (h *Home) Scan(opts ScanOptions) (*ScanResult, error) {
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = DefaultMaxFileSize
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	res := &ScanResult{Entries: make(map[string]Entry)}
	if err := h.scanDir("", opts, res); err != nil {
		return nil, err
	}
	return res, nil
}

func join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ScanPath is Scan for one path: the entry for rel itself, and everything
// under it if it is a directory. A path that does not exist gives no entries.
func (h *Home) ScanPath(rel string, opts ScanOptions) (*ScanResult, error) {
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = DefaultMaxFileSize
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	res := &ScanResult{Entries: make(map[string]Entry)}
	if rel == "" {
		return h.Scan(opts)
	}
	if ValidRel(rel) != nil {
		res.Skipped = append(res.Skipped, Skip{rel, "name is not allowed"})
		return res, nil
	}
	e, found, skip := h.entryAt(rel, opts.Base, opts.MaxFileSize)
	if skip != "" {
		res.Skipped = append(res.Skipped, Skip{rel, skip})
		return res, nil
	}
	if !found {
		return res, nil
	}
	res.Entries[rel] = e
	if e.Type == Dir {
		if err := h.scanDir(rel, opts, res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// entryAt describes one path with its hash filled in (for files), reusing the
// hash in base when size and modification time match. found is false for a
// path that does not exist; skip says why a path that exists is left out.
func (h *Home) entryAt(rel string, base *Record, maxSize int64) (e Entry, found bool, skip string) {
	e, err := h.Lstat(rel)
	switch {
	case os.IsNotExist(err):
		return Entry{}, false, ""
	case err == ErrNotRegular:
		return Entry{}, false, "not a regular file, directory or link"
	case err != nil:
		// A directory swapped for a link under us, or a permission problem.
		return Entry{}, false, err.Error()
	}
	switch e.Type {
	case Symlink:
		target, err := h.Readlink(rel)
		if err != nil {
			return Entry{}, false, err.Error()
		}
		if target == "" || len(target) > maxLinkTarget {
			return Entry{}, false, "link target is empty or too long"
		}
		e.Link = target
	case File:
		if e.Size > maxSize {
			return Entry{}, false, "larger than the size limit"
		}
		if b, ok := base.get(rel); ok && b.Type == File && b.Size == e.Size && b.ModTime == e.ModTime {
			e.Hash = b.Hash
			break
		}
		full, err := h.hashFile(rel)
		if err != nil {
			if os.IsNotExist(err) {
				return Entry{}, false, ""
			}
			return Entry{}, false, err.Error()
		}
		e = full
	}
	return e, true, ""
}

func (h *Home) scanDir(dir string, opts ScanOptions, res *ScanResult) error {
	names, err := h.ListDir(dir)
	if err != nil {
		if os.IsNotExist(err) && dir != "" {
			return nil // removed while we walked
		}
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		rel := join(dir, name)
		if isTemp(name) {
			h.dropStaleTemp(rel, opts.Now)
			continue
		}
		if ValidRel(rel) != nil {
			res.Skipped = append(res.Skipped, Skip{rel, "name is not allowed"})
			continue
		}
		e, found, skip := h.entryAt(rel, opts.Base, opts.MaxFileSize)
		if skip != "" {
			res.Skipped = append(res.Skipped, Skip{rel, skip})
			continue
		}
		if !found {
			continue
		}
		res.Entries[rel] = e
		if e.Type == Dir {
			if err := h.scanDir(rel, opts, res); err != nil {
				return err
			}
		}
	}
	return nil
}

// get is Get that tolerates a nil record.
func (r *Record) get(path string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	return r.Get(path)
}

// dropStaleTemp removes a leftover temporary file from a crashed transfer.
func (h *Home) dropStaleTemp(rel string, now time.Time) {
	h.removeStaleTemp(rel, now.Add(-staleTemp).UnixNano())
}

// HashFile reads rel and returns its entry with the hash filled in. A file
// that changes while it is being read is read again, and reported as an
// error if it never settles.
func (h *Home) hashFile(rel string) (Entry, error) {
	var lastErr error = errChanging
	for attempt := 0; attempt < hashAttempts; attempt++ {
		f, err := h.OpenRead(rel)
		if err != nil {
			return Entry{}, err
		}
		e, err := hashOpen(f, rel)
		f.Close()
		if err == nil {
			return e, nil
		}
		lastErr = err
		if err != errChanging {
			break
		}
	}
	return Entry{}, lastErr
}

type changingError struct{}

func (changingError) Error() string { return "file kept changing while it was read" }

var errChanging error = changingError{}

// hashOpen hashes an open file and checks it did not change meanwhile.
func hashOpen(f *os.File, rel string) (Entry, error) {
	before, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	sum := sha256.New()
	n, err := io.Copy(sum, f)
	if err != nil {
		return Entry{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return Entry{}, err
	}
	if n != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return Entry{}, errChanging
	}
	return Entry{
		Path:    rel,
		Type:    File,
		Size:    n,
		ModTime: before.ModTime().UnixNano(),
		Mode:    uint32(before.Mode().Perm()),
		Hash:    hex.EncodeToString(sum.Sum(nil)),
	}, nil
}
