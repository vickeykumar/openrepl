package wsync

import (
	"path"
	"strings"
)

// maxNameLen is the longest single path element Linux accepts.
const maxNameLen = 255

// ValidHomeName reports whether name can name a home: one path element that
// cannot point anywhere else.
func ValidHomeName(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > maxNameLen {
		return ErrUnsafePath
	}
	if strings.ContainsAny(name, "/\x00") {
		return ErrUnsafePath
	}
	return nil
}

// ValidRel checks a path that came from a peer. It must be relative, already
// clean, and made of ordinary names. A symbolic link in the middle of the
// path cannot be seen from the text alone; the file operations refuse those
// when they walk it.
func ValidRel(rel string) error { return checkRel(rel, false) }

// checkRel is ValidRel. With allowTemp it also accepts the reserved names of
// files being received, which only the clean-up of leftovers needs.
func checkRel(rel string, allowTemp bool) error {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.ContainsRune(rel, 0) {
		return ErrUnsafePath
	}
	if path.Clean(rel) != rel {
		return ErrUnsafePath
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." || len(part) > maxNameLen {
			return ErrUnsafePath
		}
		if !allowTemp && isTemp(part) {
			// Reserved for files being received; never synchronized.
			return ErrUnsafePath
		}
	}
	return nil
}

// splitRel splits "a/b/c" into the directory "a/b" and the name "c".
func splitRel(rel string) (dir, name string) {
	i := strings.LastIndexByte(rel, '/')
	if i < 0 {
		return "", rel
	}
	return rel[:i], rel[i+1:]
}
