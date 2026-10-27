//go:build !linux
// +build !linux

package wsync

import (
	"io"
	"os"
)

// Home is only implemented on Linux; see the package comment.
type Home struct {
	// MaxFileSize is the largest file WriteFile accepts.
	MaxFileSize int64
}

func OpenHome(base, name string, create bool) (*Home, error) { return nil, ErrUnsupported }
func (h *Home) Name() string                                 { return "" }
func (h *Home) Close() error                                 { return nil }
func (h *Home) Lstat(rel string) (Entry, error)              { return Entry{}, ErrUnsupported }
func (h *Home) ListDir(rel string) ([]string, error)         { return nil, ErrUnsupported }
func (h *Home) Readlink(rel string) (string, error)          { return "", ErrUnsupported }
func (h *Home) OpenRead(rel string) (*os.File, error)        { return nil, ErrUnsupported }
func (h *Home) WriteFile(e Entry, r io.Reader) error         { return ErrUnsupported }
func (h *Home) PutDir(e Entry) error                         { return ErrUnsupported }
func (h *Home) PutSymlink(e Entry) error                     { return ErrUnsupported }
func (h *Home) Remove(rel string) error                      { return ErrUnsupported }
func (h *Home) Rename(from, to string) error                 { return ErrUnsupported }
func (h *Home) Chmod(rel string, mode uint32) error          { return ErrUnsupported }
func (h *Home) SetTime(rel string, mtimeNs int64) error      { return ErrUnsupported }
func (h *Home) removeStaleTemp(rel string, cutoffNs int64)   {}
