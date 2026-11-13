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
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"objstore"
)

// What a user may do with the snapshots of their own workspace (the profile page): see the list, get one back into a new
// folder or as a zip, or make the workspace exactly that snapshot. The list is read from the object store at the time.

var (
	// ErrNotAvailable is what these calls answer on a node that keeps no snapshots (no object store, or a worker).
	ErrNotAvailable = errors.New("homesnap: this server keeps no snapshots")
	// ErrBadSnapshot is what Restore answers when the snapshot cannot be read as a snapshot at all (not its manifest only).
	ErrBadSnapshot = errors.New("homesnap: the snapshot cannot be read")
	// ErrNoSuchSnapshot is what they answer for a generation that is not in the bucket (it may have been pruned).
	ErrNoSuchSnapshot = errors.New("homesnap: that snapshot is no longer there")
	// ErrTooSoon is what a restore answers when another was done for the same home less than a minute ago.
	ErrTooSoon = errors.New("homesnap: a snapshot was got back a moment ago; try again in a minute")
)

// restoreEvery is the least time between two restores of one home.
const restoreEvery = time.Minute

// Info is one snapshot of a home, as the profile page lists it. The ID is what a request names (the server finds the
// object from it; a user never names a key).
type Info struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // "day", "legacy", "version" (an earlier version of a day's file) or "hidden" (a day's file that was removed)
	Day     string    `json:"day,omitempty"`
	At      time.Time `json:"at"`
	Size    int64     `json:"size"`
	Newest  bool      `json:"newest"`
	Few     bool      `json:"few"` // a lot smaller than the one before it (a hint, nothing more)
	Earlier []Info    `json:"earlier,omitempty"`

	key       string
	versionID string
}

// maxEarlier bounds the earlier versions listed for one day, and maxHidden the removed days.
const (
	maxEarlier = 60
	maxHidden  = 6
)

// List is the snapshots of a home that are in the bucket now, the newest first: the days that are kept, what is left of the earlier scheme, and, when the store keeps versions, the earlier versions of each day (what the
// day's file was before each overwrite) and the days that were removed and are still there as hidden versions.
func (s *Snapper) List(ctx context.Context, home string) ([]Info, error) {
	if !IsKept(home) || !s.Enabled() {
		return nil, ErrNotAvailable
	}
	c, err := s.Client()
	if err != nil {
		return nil, ErrNotAvailable
	}
	objs, err := s.objects(ctx, c, home)
	if err != nil {
		return nil, err
	}
	var out []Info
	byKey := map[string]int{}
	for _, o := range objs {
		in := Info{At: o.at, Size: o.size, key: o.key}
		switch o.kind {
		case kindDay:
			in.ID, in.Kind, in.Day = "d:"+o.day, kindDay, o.day
		case kindLegacy:
			in.ID, in.Kind = fmt.Sprintf("g:%d", o.gen), kindLegacy
		}
		byKey[o.key] = len(out)
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	byKey = map[string]int{}
	for i := range out {
		byKey[out[i].key] = i
	}
	// what the store remembers: earlier versions of a day's file, and days that were removed
	if vs, verr := c.ListVersions(ctx, s.prefix(home)); verr == nil {
		type group struct {
			key      string
			versions []objstore.Version
		}
		groups := map[string]*group{}
		var order []string
		for _, v := range vs {
			if g := groups[v.Key]; g != nil {
				g.versions = append(g.versions, v)
			} else {
				groups[v.Key] = &group{key: v.Key, versions: []objstore.Version{v}}
				order = append(order, v.Key)
			}
		}
		hidden := 0
		for _, k := range order {
			g := groups[k]
			ob, ok := parseObject(home, objstore.Object{Key: k})
			if !ok || ob.kind != kindDay {
				continue
			}
			sort.Slice(g.versions, func(i, j int) bool { return g.versions[i].LastModified.After(g.versions[j].LastModified) })
			var earlier []Info
			for _, v := range g.versions {
				if v.DeleteMarker || v.IsLatest && !g.versions[0].DeleteMarker {
					continue
				}
				if len(earlier) >= maxEarlier {
					break
				}
				earlier = append(earlier, Info{ID: "d:" + ob.day + "@" + v.VersionID, Kind: "version", Day: ob.day, At: v.LastModified, Size: v.Size, key: k, versionID: v.VersionID})
			}
			if i, here := byKey[k]; here {
				out[i].Earlier = earlier
			} else if len(earlier) > 0 && hidden < maxHidden {
				// the day's file was removed (it fell out of the kept days): its versions are still in the store, for a while
				hidden++
				top := earlier[0]
				out = append(out, Info{ID: "d:" + ob.day + "@" + top.versionID, Kind: "hidden", Day: ob.day, At: top.At, Size: top.Size, Earlier: earlier[1:], key: k, versionID: top.versionID})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		hi, hj := out[i].Kind == "hidden", out[j].Kind == "hidden"
		if hi != hj {
			return hj // removed days last
		}
		return out[i].At.After(out[j].At)
	})
	first := true
	for i := range out {
		if out[i].Kind == "hidden" {
			continue
		}
		if first {
			out[i].Newest = true
			first = false
		}
		// a lot smaller than the one before it: the trouble of a fresh folder that was snapshotted over the user's files
		for j := i + 1; j < len(out); j++ {
			if out[j].Kind == "hidden" {
				continue
			}
			if out[j].Size >= 4096 && out[i].Size*5 < out[j].Size {
				out[i].Few = true
			}
			break
		}
	}
	return out, nil
}

// find is the entry of the list that an ID names (also among the earlier versions).
func find(infos []Info, id string) (Info, bool) {
	for _, in := range infos {
		if in.ID == id {
			return in, true
		}
		for _, e := range in.Earlier {
			if e.ID == id {
				return e, true
			}
		}
	}
	return Info{}, false
}

// Archive reads the snapshot an ID names and checks it is whole (its hash, when the object has one).
func (s *Snapper) Archive(ctx context.Context, home, id string) ([]byte, Info, error) {
	infos, err := s.List(ctx, home)
	if err != nil {
		return nil, Info{}, err
	}
	want, ok := find(infos, id)
	if !ok {
		return nil, Info{}, ErrNoSuchSnapshot
	}
	c, err := s.Client()
	if err != nil {
		return nil, Info{}, ErrNotAvailable
	}
	var data []byte
	var obj *objstore.Object
	if want.versionID != "" {
		data, obj, err = c.GetVersion(ctx, want.key, want.versionID)
	} else {
		data, obj, err = c.Get(ctx, want.key)
	}
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return nil, Info{}, ErrNoSuchSnapshot
		}
		return nil, Info{}, err
	}
	if obj != nil {
		if h := obj.Meta["sha256"]; h != "" && h != sum(data) {
			return nil, Info{}, errors.New("homesnap: that snapshot is damaged")
		}
	}
	return data, want, nil
}

// RestoreResult says what a restore did.
type RestoreResult struct {
	Mode   string `json:"mode"`
	Folder string `json:"folder,omitempty"` // "folder": the name of the new folder
	Files  int    `json:"files"`
	// Forced says that the snapshot did not match its manifest, and its files were restored as they are in it.
	Forced bool `json:"forced,omitempty"`
}

// Restore gets a snapshot back. mode "folder": its files are put in a new folder of the home, beside what is there, and
// nothing existing is touched. mode "replace": the home becomes exactly the snapshot and the other files are removed; there is
// no copy of them first (the page tells the user to copy or download them before), so it cannot be taken back.
func (s *Snapper) Restore(ctx context.Context, home, id, mode string) (RestoreResult, error) {
	res := RestoreResult{Mode: mode}
	if mode != "folder" && mode != "replace" {
		return res, fmt.Errorf("homesnap: %q is not a way to get a snapshot back", mode)
	}
	archive, info, err := s.Archive(ctx, home, id) // read first: the copy below may push the oldest snapshot out
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
	_, err = Extract(archive, tmp)
	if errors.Is(err, errManifest) {
		// the files are the user's own and the user chose this snapshot: what is in it is restored, and the answer says so
		log.Printf("homesnap: the snapshot %s of %s does not match its manifest (%v): restoring it as it is", id, home, err)
		_, err = extractForced(archive, tmp)
		res.Forced = err == nil
	}
	if err != nil {
		os.RemoveAll(tmp)
		return res, fmt.Errorf("%w: %v", ErrBadSnapshot, err)
	}
	filepath.Walk(tmp, func(path string, fi os.FileInfo, werr error) error {
		if werr == nil && fi.Mode().IsRegular() {
			res.Files++
		}
		return nil
	})
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
