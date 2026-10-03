package wsync

import (
	"errors"
	"sort"
	"strings"
)

// Reserved names and limits.
const (
	// TempPrefix starts the name of a file that is being received. Such files
	// are never synchronized, so users cannot use names that start with it.
	TempPrefix = ".wsync-"
	// DefaultMaxFileSize is the largest file that is synchronized.
	DefaultMaxFileSize = 50 << 20
	// maxLinkTarget bounds a symbolic link's stored target.
	maxLinkTarget = 4096
)

// Errors reported by the package.
var (
	ErrUnsafePath   = errors.New("wsync: path is not inside the home, or passes through a link")
	ErrNotEmpty     = errors.New("wsync: directory is not empty")
	ErrNotRegular   = errors.New("wsync: not a regular file")
	ErrTooLarge     = errors.New("wsync: file is larger than the limit")
	ErrHashMismatch = errors.New("wsync: content does not match its hash")
	ErrShortRead    = errors.New("wsync: content is shorter than its size")
	ErrUnsupported  = errors.New("wsync: workspace sync needs Linux")
	ErrIsDirectory  = errors.New("wsync: target is a directory that is not empty")
)

// FileType is the kind of a synchronized entry. Sockets, FIFOs and device
// files are not synchronized.
type FileType uint8

const (
	File FileType = iota + 1
	Dir
	Symlink
)

func (t FileType) String() string {
	switch t {
	case File:
		return "file"
	case Dir:
		return "dir"
	case Symlink:
		return "symlink"
	}
	return "unknown"
}

// Entry describes one path of a home.
type Entry struct {
	Path    string   `json:"path"` // relative to the home, slash separated, cleaned
	Type    FileType `json:"type"`
	Size    int64    `json:"size,omitempty"`
	ModTime int64    `json:"mtime"`          // unix nanoseconds
	Mode    uint32   `json:"mode"`           // permission bits only
	Hash    string   `json:"hash,omitempty"` // sha256 hex, regular files only
	Link    string   `json:"link,omitempty"` // target text, symbolic links only
}

// permMask keeps the permission bits. Set-user-id, set-group-id and the
// sticky bit are never synchronized: a file must not gain privileges by being
// copied.
const permMask = 0777

// Equal reports whether two entries have the same content. The path and the
// modification time do not count; the time only decides conflicts.
func (e Entry) Equal(o Entry) bool {
	if e.Type != o.Type || e.Mode != o.Mode {
		return false
	}
	switch e.Type {
	case File:
		return e.Size == o.Size && e.Hash == o.Hash
	case Symlink:
		return e.Link == o.Link
	}
	return true
}

// eq compares optional entries: two absent entries are equal.
func eq(a, b *Entry) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func entryPtr(m map[string]Entry, path string) *Entry {
	if e, ok := m[path]; ok {
		return &e
	}
	return nil
}

// sortedPaths returns the keys of m in ascending order, so that a directory
// comes before what it contains.
func sortedPaths(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// hasPrefixPath reports whether p is under dir (not equal to it).
func hasPrefixPath(p, dir string) bool {
	return strings.HasPrefix(p, dir+"/")
}

// isTemp reports whether a name is a file that is being received.
func isTemp(name string) bool { return strings.HasPrefix(name, TempPrefix) }
