//go:build linux
// +build linux

package wsync

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Home is one home directory, held open as a file descriptor. Every path is
// resolved from that descriptor one component at a time with O_NOFOLLOW, so
// a path can never leave the home through a symbolic link, even when a
// program inside the home swaps a directory for a link at that moment.
type Home struct {
	name string
	fd   int
	// MaxFileSize is the largest file WriteFile accepts. Zero means
	// DefaultMaxFileSize.
	MaxFileSize int64
}

const (
	openDirFlags  = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	openReadFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
)

// OpenHome opens the home called name under base. With create the home is
// made if it does not exist. The home itself must not be a symbolic link.
func OpenHome(base, name string, create bool) (*Home, error) {
	if err := ValidHomeName(name); err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(base, 0755); err != nil {
			return nil, err
		}
	}
	basefd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: base, Err: err}
	}
	defer unix.Close(basefd)
	if create {
		if err := unix.Mkdirat(basefd, name, 0755); err != nil && err != unix.EEXIST {
			return nil, &os.PathError{Op: "mkdir", Path: name, Err: err}
		}
	}
	fd, err := unix.Openat(basefd, name, openDirFlags, 0)
	if err != nil {
		if err == unix.ELOOP || err == unix.ENOTDIR {
			return nil, fmt.Errorf("%w: home %q", ErrUnsafePath, name)
		}
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return &Home{name: name, fd: fd}, nil
}

func (h *Home) Name() string { return h.name }

// Close releases the directory descriptor.
func (h *Home) Close() error { return unix.Close(h.fd) }

func (h *Home) maxSize() int64 {
	if h.MaxFileSize > 0 {
		return h.MaxFileSize
	}
	return DefaultMaxFileSize
}

// openDir returns a new descriptor for the directory rel ("" is the home).
// With create, missing directories on the way are made.
func (h *Home) openDir(rel string, create bool) (int, error) {
	// Open "." again instead of duplicating the descriptor: a duplicate
	// shares the directory read position, so a second listing of the home
	// would start where the first one ended.
	cur, err := unix.Openat(h.fd, ".", openDirFlags, 0)
	if err != nil {
		return -1, err
	}
	if rel == "" {
		return cur, nil
	}
	for _, comp := range strings.Split(rel, "/") {
		next, err := unix.Openat(cur, comp, openDirFlags, 0)
		if err == unix.ENOENT && create {
			if merr := unix.Mkdirat(cur, comp, 0755); merr != nil && merr != unix.EEXIST {
				unix.Close(cur)
				return -1, &os.PathError{Op: "mkdir", Path: comp, Err: merr}
			}
			next, err = unix.Openat(cur, comp, openDirFlags, 0)
		}
		unix.Close(cur)
		if err != nil {
			if err == unix.ELOOP || err == unix.ENOTDIR {
				return -1, fmt.Errorf("%w: %q is a link or not a directory", ErrUnsafePath, comp)
			}
			return -1, &os.PathError{Op: "open", Path: rel, Err: err}
		}
		cur = next
	}
	return cur, nil
}

// parent returns a descriptor for the directory holding rel, and its name.
func (h *Home) parent(rel string, create bool) (int, string, error) {
	if err := ValidRel(rel); err != nil {
		return -1, "", err
	}
	dir, name := splitRel(rel)
	fd, err := h.openDir(dir, create)
	return fd, name, err
}

// removeStaleTemp deletes the leftover temporary file rel if it is a regular
// file last modified at or before cutoff. Temporary names are not valid
// paths for the public operations, so this is the one place that reaches one.
func (h *Home) removeStaleTemp(rel string, cutoffNs int64) {
	if checkRel(rel, true) != nil {
		return
	}
	dir, name := splitRel(rel)
	if !isTemp(name) {
		return
	}
	dirfd, err := h.openDir(dir, false)
	if err != nil {
		return
	}
	defer unix.Close(dirfd)
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return
	}
	if st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mtim.Nano() <= cutoffNs {
		unix.Unlinkat(dirfd, name, 0)
	}
}

// entryFromStat describes what a stat found. special is true for sockets,
// FIFOs and device files, which are not synchronized.
func entryFromStat(rel string, st *unix.Stat_t) (e Entry, special bool) {
	e = Entry{Path: rel, ModTime: st.Mtim.Nano(), Mode: st.Mode & permMask}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		e.Type, e.Size = File, st.Size
	case unix.S_IFDIR:
		// A directory always keeps owner access, or its contents could not
		// be written on the other side.
		e.Type, e.Mode = Dir, e.Mode|0700
	case unix.S_IFLNK:
		e.Type = Symlink
	default:
		return e, true
	}
	return e, false
}

// Lstat describes rel without following links. The hash is not filled in.
// A missing path is os.ErrNotExist; a socket, FIFO or device is ErrNotRegular.
func (h *Home) Lstat(rel string) (Entry, error) {
	var st unix.Stat_t
	if rel == "" {
		if err := unix.Fstat(h.fd, &st); err != nil {
			return Entry{}, err
		}
	} else {
		dirfd, name, err := h.parent(rel, false)
		if err != nil {
			return Entry{}, err
		}
		defer unix.Close(dirfd)
		if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return Entry{}, &os.PathError{Op: "lstat", Path: rel, Err: err}
		}
	}
	e, special := entryFromStat(rel, &st)
	if special {
		return e, ErrNotRegular
	}
	return e, nil
}

// ListDir returns the names in the directory rel ("" is the home).
func (h *Home) ListDir(rel string) ([]string, error) {
	if rel != "" {
		if err := ValidRel(rel); err != nil {
			return nil, err
		}
	}
	fd, err := h.openDir(rel, false)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), rel) // closes fd
	defer f.Close()
	return f.Readdirnames(-1)
}

// Readlink returns the target text of the symbolic link rel.
func (h *Home) Readlink(rel string) (string, error) {
	dirfd, name, err := h.parent(rel, false)
	if err != nil {
		return "", err
	}
	defer unix.Close(dirfd)
	buf := make([]byte, maxLinkTarget+1)
	n, err := unix.Readlinkat(dirfd, name, buf)
	if err != nil {
		return "", &os.PathError{Op: "readlink", Path: rel, Err: err}
	}
	if n > maxLinkTarget {
		return "", fmt.Errorf("wsync: link target of %q is too long", rel)
	}
	return string(buf[:n]), nil
}

// OpenRead opens the regular file rel for reading. A link is never followed,
// and a FIFO or device that replaced the file is refused without blocking.
func (h *Home) OpenRead(rel string) (*os.File, error) {
	dirfd, name, err := h.parent(rel, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirfd)
	fd, err := unix.Openat(dirfd, name, openReadFlags, 0)
	if err != nil {
		if err == unix.ELOOP {
			return nil, fmt.Errorf("%w: %q is a link", ErrUnsafePath, rel)
		}
		return nil, &os.PathError{Op: "open", Path: rel, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, ErrNotRegular
	}
	return os.NewFile(uintptr(fd), rel), nil
}

func tempName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return TempPrefix + hex.EncodeToString(b[:]), nil
}

func timespec(ns int64) unix.Timespec { return unix.NsecToTimespec(ns) }

// removeEmptyDir removes name in dirfd if it is an empty directory. It
// reports whether name is a directory at all.
func removeEmptyDir(dirfd int, name string) (isDir bool, err error) {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return false, nil
		}
		return false, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false, nil
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil {
		if err == unix.ENOTEMPTY || err == unix.EEXIST {
			return true, ErrIsDirectory
		}
		return true, err
	}
	return true, nil
}

// WriteFile stores a regular file. The content is written to a temporary
// file in the same directory, checked against the size and hash in e, and
// renamed into place, so a reader never sees a partial file and a failure
// leaves nothing behind. Missing parent directories are created.
func (h *Home) WriteFile(e Entry, r io.Reader) (err error) {
	if e.Type != File {
		return ErrNotRegular
	}
	if e.Size < 0 || e.Size > h.maxSize() {
		return ErrTooLarge
	}
	dirfd, name, err := h.parent(e.Path, true)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)

	// An empty directory in the way is replaced; one with content is not.
	if _, err := removeEmptyDir(dirfd, name); err != nil {
		return err
	}

	tmp, err := tempName()
	if err != nil {
		return err
	}
	fd, err := unix.Openat(dirfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return &os.PathError{Op: "create", Path: e.Path, Err: err}
	}
	f := os.NewFile(uintptr(fd), e.Path)
	defer func() {
		if err != nil {
			f.Close()
			unix.Unlinkat(dirfd, tmp, 0)
		}
	}()

	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(r, e.Size))
	if err != nil {
		return err
	}
	if n != e.Size {
		return ErrShortRead
	}
	if e.Hash != "" && hex.EncodeToString(sum.Sum(nil)) != e.Hash {
		return ErrHashMismatch
	}
	if err = unix.Fchmod(fd, e.Mode&permMask); err != nil {
		return err
	}
	ts := []unix.Timespec{timespec(e.ModTime), timespec(e.ModTime)}
	if err = unix.UtimesNanoAt(dirfd, tmp, ts, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Renameat(dirfd, tmp, dirfd, name); err != nil {
		if err == unix.EISDIR || err == unix.ENOTEMPTY {
			err = ErrIsDirectory
		}
		unix.Unlinkat(dirfd, tmp, 0)
		return err
	}
	unix.Fsync(dirfd)
	return nil
}

// PutDir makes sure the directory exists with the entry's permissions. A
// file or link in the way is replaced.
func (h *Home) PutDir(e Entry) error {
	if e.Type != Dir {
		return ErrNotRegular
	}
	dirfd, name, err := h.parent(e.Path, true)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)
	mode := (e.Mode & permMask) | 0700
	for attempt := 0; attempt < 2; attempt++ {
		err = unix.Mkdirat(dirfd, name, mode)
		if err == nil {
			break
		}
		if err != unix.EEXIST {
			return &os.PathError{Op: "mkdir", Path: e.Path, Err: err}
		}
		fd, oerr := unix.Openat(dirfd, name, openDirFlags, 0)
		if oerr == nil {
			err = unix.Fchmod(fd, mode)
			unix.Close(fd)
			return err
		}
		if oerr != unix.ELOOP && oerr != unix.ENOTDIR {
			return &os.PathError{Op: "open", Path: e.Path, Err: oerr}
		}
		// A file or link is in the way.
		if uerr := unix.Unlinkat(dirfd, name, 0); uerr != nil && uerr != unix.ENOENT {
			return &os.PathError{Op: "remove", Path: e.Path, Err: uerr}
		}
	}
	if err != nil {
		return err
	}
	fd, err := unix.Openat(dirfd, name, openDirFlags, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Fchmod(fd, mode)
}

// PutSymlink stores a symbolic link as text. The target is never resolved.
func (h *Home) PutSymlink(e Entry) error {
	if e.Type != Symlink || e.Link == "" || len(e.Link) > maxLinkTarget || strings.ContainsRune(e.Link, 0) {
		return ErrNotRegular
	}
	dirfd, name, err := h.parent(e.Path, true)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)
	tmp, err := tempName()
	if err != nil {
		return err
	}
	if err := unix.Symlinkat(e.Link, dirfd, tmp); err != nil {
		return &os.PathError{Op: "symlink", Path: e.Path, Err: err}
	}
	ts := []unix.Timespec{timespec(e.ModTime), timespec(e.ModTime)}
	unix.UtimesNanoAt(dirfd, tmp, ts, unix.AT_SYMLINK_NOFOLLOW)
	if err := unix.Renameat(dirfd, tmp, dirfd, name); err != nil {
		unix.Unlinkat(dirfd, tmp, 0)
		if err == unix.EISDIR || err == unix.ENOTEMPTY {
			return ErrIsDirectory
		}
		return err
	}
	return nil
}

// Remove deletes a file or link, or an empty directory. A path that is
// already gone is not an error; a directory with content is ErrNotEmpty.
func (h *Home) Remove(rel string) error {
	dirfd, name, err := h.parent(rel, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer unix.Close(dirfd)
	isDir, err := removeEmptyDir(dirfd, name)
	if err == ErrIsDirectory {
		return ErrNotEmpty
	}
	if err != nil || isDir {
		return err
	}
	if err := unix.Unlinkat(dirfd, name, 0); err != nil && err != unix.ENOENT {
		return &os.PathError{Op: "remove", Path: rel, Err: err}
	}
	return nil
}

// Rename moves a path inside the home. Missing parent directories of the new
// path are created.
func (h *Home) Rename(from, to string) error {
	fromfd, fromName, err := h.parent(from, false)
	if err != nil {
		return err
	}
	defer unix.Close(fromfd)
	tofd, toName, err := h.parent(to, true)
	if err != nil {
		return err
	}
	defer unix.Close(tofd)
	if err := unix.Renameat(fromfd, fromName, tofd, toName); err != nil {
		return &os.PathError{Op: "rename", Path: from, Err: err}
	}
	return nil
}

// Chmod sets the permission bits of a regular file or directory without
// following links.
func (h *Home) Chmod(rel string, mode uint32) error {
	dirfd, name, err := h.parent(rel, false)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)
	fd, err := unix.Openat(dirfd, name, openReadFlags, 0)
	if err != nil {
		if err == unix.ELOOP {
			return fmt.Errorf("%w: %q is a link", ErrUnsafePath, rel)
		}
		return &os.PathError{Op: "open", Path: rel, Err: err}
	}
	defer unix.Close(fd)
	return unix.Fchmod(fd, mode&permMask)
}

// SetTime sets the modification time of rel without following links.
func (h *Home) SetTime(rel string, mtimeNs int64) error {
	dirfd, name, err := h.parent(rel, false)
	if err != nil {
		return err
	}
	defer unix.Close(dirfd)
	ts := []unix.Timespec{timespec(mtimeNs), timespec(mtimeNs)}
	return unix.UtimesNanoAt(dirfd, name, ts, unix.AT_SYMLINK_NOFOLLOW)
}
