package homesnap

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// What a user may do with the snapshots of their own workspace (the profile page): see the list, get one back into a new
// folder or as a zip, or make the workspace exactly that snapshot. The list is read from the object store at the time.

var (
	// ErrNotAvailable is what these calls answer on a node that keeps no snapshots (no object store, or a worker).
	ErrNotAvailable = errors.New("homesnap: this server keeps no snapshots")
	// ErrNoSuchSnapshot is what they answer for a generation that is not in the bucket (it may have been pruned).
	ErrNoSuchSnapshot = errors.New("homesnap: that snapshot is no longer there")
	// ErrTooSoon is what a restore answers when another was done for the same home less than a minute ago.
	ErrTooSoon = errors.New("homesnap: a snapshot was got back a moment ago; try again in a minute")
	// ErrSafetyCopy is what Replace answers when the copy of the files that are there now could not be made: nothing is changed.
	ErrSafetyCopy = errors.New("homesnap: the files you have now could not be copied first, so nothing was changed")
)

// restoreEvery is the least time between two restores of one home.
const restoreEvery = time.Minute

// Info is one snapshot of a home, as the profile page lists it.
type Info struct {
	Generation int64     `json:"generation"`
	At         time.Time `json:"at"`
	Size       int64     `json:"size"`
	Newest     bool      `json:"newest"`
	Few        bool      `json:"few"`    // a lot smaller than the one before it (a hint, nothing more)
	Before     bool      `json:"before"` // the copy made before a workspace was replaced
	Key        string    `json:"-"`
}

// parseKey reads the generation, the time and the kind from the name of a snapshot object.
func parseKey(key string) (gen int64, at time.Time, before bool, ok bool) {
	name := key[strings.LastIndex(key, "/")+1:]
	if !strings.HasSuffix(name, ".tar.gz") {
		return 0, at, false, false
	}
	name = strings.TrimSuffix(name, ".tar.gz")
	before = strings.HasSuffix(name, "-before")
	name = strings.TrimSuffix(name, "-before")
	parts := strings.Split(name, "-")
	if len(parts) < 2 {
		return 0, at, false, false
	}
	if _, err := fmt.Sscan(parts[0], &gen); err != nil || gen <= 0 {
		return 0, at, false, false
	}
	t, err := time.Parse(timeLayout, parts[1])
	if err != nil {
		return 0, at, false, false
	}
	return gen, t, before, true
}

// List is the snapshots of a home that are in the bucket now, the newest first.
func (s *Snapper) List(ctx context.Context, home string) ([]Info, error) {
	if !IsKept(home) || !s.Enabled() {
		return nil, ErrNotAvailable
	}
	c, err := s.Client()
	if err != nil {
		return nil, ErrNotAvailable
	}
	objs, err := c.List(ctx, s.prefix(home))
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, o := range objs {
		gen, at, before, ok := parseKey(o.Key)
		if !ok {
			continue
		}
		out = append(out, Info{Generation: gen, At: at, Size: o.Size, Before: before, Key: o.Key})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Generation != out[j].Generation {
			return out[i].Generation > out[j].Generation
		}
		return out[i].Key > out[j].Key
	})
	for i := range out {
		out[i].Newest = i == 0
		// a lot smaller than the one before it: the trouble of a fresh folder that was snapshotted over the user's files
		if i+1 < len(out) && out[i+1].Size >= 4096 && out[i].Size*5 < out[i+1].Size {
			out[i].Few = true
		}
	}
	return out, nil
}

// Archive reads one snapshot and checks it is whole (its hash, when the bucket has it).
func (s *Snapper) Archive(ctx context.Context, home string, generation int64) ([]byte, Info, error) {
	infos, err := s.List(ctx, home)
	if err != nil {
		return nil, Info{}, err
	}
	var want *Info
	for i := range infos {
		if infos[i].Generation == generation {
			want = &infos[i]
			break
		}
	}
	if want == nil {
		return nil, Info{}, ErrNoSuchSnapshot
	}
	c, err := s.Client()
	if err != nil {
		return nil, Info{}, ErrNotAvailable
	}
	data, obj, err := c.Get(ctx, want.Key)
	if err != nil {
		return nil, Info{}, err
	}
	if obj != nil {
		if h := obj.Meta["sha256"]; h != "" && h != sum(data) {
			return nil, Info{}, errors.New("homesnap: that snapshot is damaged")
		}
	}
	return data, *want, nil
}

// RestoreResult says what a restore did.
type RestoreResult struct {
	Mode   string `json:"mode"`
	Folder string `json:"folder,omitempty"` // "folder": the name of the new folder
	Files  int    `json:"files"`
}

// Restore gets a snapshot back. mode "folder": its files are put in a new folder of the home, beside what is there, and
// nothing existing is touched. mode "replace": a copy of what is there now is made first (a snapshot named "before"), and
// then the home becomes exactly the snapshot: the other files are removed.
func (s *Snapper) Restore(ctx context.Context, home string, generation int64, mode string) (RestoreResult, error) {
	res := RestoreResult{Mode: mode}
	if mode != "folder" && mode != "replace" {
		return res, fmt.Errorf("homesnap: %q is not a way to get a snapshot back", mode)
	}
	archive, info, err := s.Archive(ctx, home, generation) // read first: the copy below may push the oldest snapshot out
	if err != nil {
		return res, err
	}
	s.mu.Lock()
	st := s.state(home)
	if st.busy || st.restoring {
		s.mu.Unlock()
		return res, ErrBusy
	}
	if !st.lastRestore.IsZero() && s.now().Sub(st.lastRestore) < restoreEvery {
		s.mu.Unlock()
		return res, ErrTooSoon
	}
	st.lastRestore = s.now()
	s.mu.Unlock()

	if err := os.MkdirAll(s.Base(), 0777); err != nil {
		return res, err
	}
	dir := s.dir(home)
	tmp := filepath.Join(s.Base(), fmt.Sprintf(".restore-%s-%d", home, s.now().UnixNano()))
	m, err := Extract(archive, tmp)
	if err != nil {
		return res, err
	}
	for _, e := range m.Entries {
		if !strings.HasSuffix(e.Path, "/") {
			res.Files++
		}
	}
	switch mode {
	case "folder":
		if err := os.MkdirAll(dir, 0755); err != nil {
			os.RemoveAll(tmp)
			return res, err
		}
		name := "restored-" + info.At.UTC().Format("2006-01-02-1504")
		for n := 2; ; n++ {
			if _, err := os.Lstat(filepath.Join(dir, name)); os.IsNotExist(err) {
				break
			}
			name = fmt.Sprintf("restored-%s-%d", info.At.UTC().Format("2006-01-02-1504"), n)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			os.RemoveAll(tmp)
			return res, err
		}
		res.Folder = name
		return res, nil
	default:
		// the files that are there now are copied first; with none there is nothing to lose
		if fi, serr := os.Stat(dir); serr == nil && fi.IsDir() {
			if _, err := s.snapshot(ctx, home, true, false, "-before"); err != nil {
				os.RemoveAll(tmp)
				return res, fmt.Errorf("%w: %v", ErrSafetyCopy, err)
			}
		}
		// every file gets the time of now: the sync with a worker keeps the newer file, and what comes back must win
		now := s.now()
		filepath.Walk(tmp, func(p string, fi os.FileInfo, err error) error {
			if err == nil && p != tmp {
				os.Chtimes(p, now, now)
			}
			return nil
		})
		old := ""
		if _, err := os.Lstat(dir); err == nil {
			old = filepath.Join(s.Base(), fmt.Sprintf(".replaced-%s-%d", home, s.now().UnixNano()))
			if err := os.Rename(dir, old); err != nil {
				os.RemoveAll(tmp)
				return res, err
			}
		}
		if err := os.Rename(tmp, dir); err != nil {
			if old != "" {
				os.Rename(old, dir) // back as it was
			}
			os.RemoveAll(tmp)
			return res, err
		}
		if old != "" {
			os.RemoveAll(old)
		}
		s.mu.Lock()
		st.fingerprint = "" // what the workspace is now is snapshotted at the next round
		s.mu.Unlock()
		return res, nil
	}
}

// ToZip writes the files of a snapshot as a zip, for a download. The archive is checked as it is read: a name that leaves the
// folder is refused.
func ToZip(archive []byte, w io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("homesnap: not a snapshot: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	zw := zip.NewWriter(w)
	total := int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("homesnap: the snapshot is damaged: %v", err)
		}
		if h.Name == manifestName {
			continue
		}
		rel, ok := safeRel(h.Name)
		if !ok {
			return fmt.Errorf("homesnap: %q is not a path a snapshot may have", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if _, err := zw.Create(filepath.ToSlash(rel) + "/"); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size > MaxFileBytes || total+h.Size > MaxHomeBytes {
				return errors.New("homesnap: the snapshot is larger than a snapshot may be")
			}
			total += h.Size
			fh := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate, Modified: h.ModTime}
			fw, err := zw.CreateHeader(fh)
			if err != nil {
				return err
			}
			if _, err := io.Copy(fw, io.LimitReader(tr, h.Size)); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}
