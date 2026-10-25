package workspace

import (
	"path/filepath"
	"strings"

	"wsync"
)

// OwnEntry assigns the trusted workspace identity to one entry and any
// directories created on the way to it. rel must be a clean workspace path.
func OwnEntry(base, home, rel string) error {
	if err := wsync.ValidRel(rel); err != nil {
		return err
	}
	id, err := IdentityFor(base, home)
	if err != nil {
		return err
	}
	h, err := wsync.OpenHome(base, filepath.Base(home), false)
	if err != nil {
		return err
	}
	defer h.Close()
	parts := strings.Split(rel, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		entry, err := h.Lstat(p)
		if err != nil {
			return err
		}
		if err := h.Chown(p, int(id.UID), int(id.GID)); err != nil {
			return err
		}
		if entry.Type == wsync.Dir {
			if err := h.Chmod(p, 0700); err != nil {
				return err
			}
		} else if entry.Type == wsync.File {
			if err := h.Chmod(p, (entry.Mode&0700)|0600); err != nil {
				return err
			}
		}
	}
	return nil
}
