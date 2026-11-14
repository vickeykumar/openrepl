package appstore

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A finished bundle travels from the gateway to a worker as a .tar.gz. Its files hold the store's path (conda wrote it into them when
// it made the prefix), so the worker must have the store in the same folder: every node of one site does (OPENREPL_STORE_DIR).

// WriteBundle writes the bundle's files as a gzipped tar: regular files, folders and relative symlinks, with the marker last.
func (s Store) WriteBundle(id string, w io.Writer) error {
	root := s.BundlePath(id)
	if root == "" || !s.HasBundle(id) {
		return fmt.Errorf("%s is not a finished bundle", id)
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	var marker string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if rel == markerName {
			marker = path
			return nil
		}
		return addEntry(tw, path, rel, info)
	})
	if err == nil && marker != "" {
		fi, e := os.Lstat(marker)
		if e == nil {
			err = addEntry(tw, marker, markerName, fi)
		} else {
			err = e
		}
	}
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func addEntry(tw *tar.Writer, path, rel string, info os.FileInfo) error {
	var link string
	if info.Mode()&os.ModeSymlink != 0 {
		l, err := os.Readlink(path)
		if err != nil {
			return err
		}
		link = l
	}
	h, err := tar.FileInfoHeader(info, link)
	if err != nil {
		return err
	}
	h.Name = filepath.ToSlash(rel)
	h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
	if info.IsDir() {
		h.Name += "/"
	}
	if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeSymlink {
		return nil // devices, sockets and pipes are not part of a bundle
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	if h.Typeflag == tar.TypeReg {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}
	return nil
}

// ReadBundle unpacks a bundle made by WriteBundle into dest (which must not exist). Nothing may leave dest: a name that is
// absolute or climbs, a symlink that points outside, or a kind of file that is not a file, folder or link is refused, and what was
// written is removed.
func ReadBundle(r io.Reader, dest string) (err error) {
	if _, e := os.Lstat(dest); e == nil {
		return fmt.Errorf("%s exists already", dest)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dest)
		}
	}()
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	root := filepath.Clean(dest)
	inside := func(p string) bool { return p == root || strings.HasPrefix(p, root+string(filepath.Separator)) }
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("the entry %q leaves the bundle", h.Name)
		}
		target := filepath.Join(root, name)
		if !inside(target) {
			return fmt.Errorf("the entry %q leaves the bundle", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0777|0200)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			os.Chmod(target, os.FileMode(h.Mode)&0777)
		case tar.TypeSymlink:
			resolved := h.Linkname
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(target), resolved)
			}
			// an absolute link must stay in the bundle too (conda writes none, but a bad archive might)
			if !inside(filepath.Clean(resolved)) {
				return fmt.Errorf("the link %q points outside the bundle", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the entry %q is not a file, folder or link", h.Name)
		}
	}
}
