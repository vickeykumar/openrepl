// Package backup keeps a copy of what is only on this server's disk in the object store (S3, R2), and puts it
// back when the disk is empty (a host that does not keep its disk, such as Render). It covers the database
// files, the settings, the audit log and the usage totals; when MongoDB or Firestore holds the databases it
// covers only the audit log and the totals. The workspaces of signed-in users are in package homesnap.
//
// A backup is an archive (archive.go) under backups/<name>/<time>.tar.gz and a small latest.json beside it
// that is written last, so that a backup that dies half way is never the one that is restored. Retention is
// in retention.go.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"objstore"
	"utils"
)

// Interval is how often a backup is made when something changed.
const (
	Interval   = 15 * time.Minute
	firstAfter = 2 * time.Minute
	archiveExt = ".tar.gz"
	timeLayout = "20060102T150405Z"
)

// Latest is what latest.json holds: which archive is the newest complete one.
type Latest struct {
	Key         string    `json:"key"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"createdAt"`
	Fingerprint string    `json:"fingerprint"`
	Files       []string  `json:"files"`
}

// Backuper makes the backups of one directory. Everything it needs from outside is a field, so that a test can
// give it a fake object store.
type Backuper struct {
	Dir    string                           // the folder with the files (/opt/gotty)
	Name   func() string                    // names this deployment in the bucket
	Remote func() string                    // "" when the databases are files, else where they are
	Client func() (*objstore.Client, error) // the object store; an error when it is not set up
	Now    func() time.Time

	mu       sync.Mutex
	last     Latest // what was uploaded last by this process, or found at start
	pruned   bool   // the first pruning of a process only says what it would delete
	status   Status
	started  bool
	stopOnce sync.Once
}

// Status is what the dashboard shows of the backups.
type Status struct {
	LastOK    time.Time
	LastKey   string
	LastSize  int64
	Kept      int
	LastErr   string
	LastErrAt time.Time
	Restored  *Restored
	Files     []string
}

// Restored says that the files of this server came back from a backup at start.
type Restored struct {
	Key       string
	CreatedAt time.Time
	Files     []string
	At        time.Time
}

// Default is the backup of this server.
var Default = &Backuper{
	Dir:    utils.GOTTY_PATH,
	Name:   utils.BackupName,
	Remote: func() string { return "" },
	Client: objstore.Default,
	Now:    time.Now,
}

func (b *Backuper) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Backuper) prefix() string { return "backups/" + b.Name() + "/" }

// Status reports what the backups did.
func (b *Backuper) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.status
	s.Files = append([]string(nil), s.Files...)
	return s
}

func (b *Backuper) fail(err error) {
	b.mu.Lock()
	b.status.LastErr, b.status.LastErrAt = err.Error(), b.now()
	b.mu.Unlock()
}

func (b *Backuper) archiveKey(at time.Time) string {
	return b.prefix() + at.UTC().Format(timeLayout) + archiveExt
}

// parseKey reads the time out of an archive's key.
func parseKey(key string) (time.Time, bool) {
	base := key[strings.LastIndex(key, "/")+1:]
	if !strings.HasSuffix(base, archiveExt) {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, strings.TrimSuffix(base, archiveExt))
	return t, err == nil
}

// entries lists the archives of this deployment in the bucket.
func (b *Backuper) entries(ctx context.Context, c *objstore.Client) ([]Entry, error) {
	objs, err := c.List(ctx, b.prefix())
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, o := range objs {
		if t, ok := parseKey(o.Key); ok {
			out = append(out, Entry{Key: o.Key, At: t})
		}
	}
	return out, nil
}

// Result is what one run did.
type Result struct {
	Uploaded bool
	Key      string
	Size     int64
	Files    int
	Kept     int
	Deleted  int
	Note     string
}

func (r Result) String() string {
	if !r.Uploaded {
		return r.Note
	}
	s := fmt.Sprintf("uploaded %s (%d files), %d kept", humanSize(r.Size), r.Files, r.Kept)
	if r.Deleted > 0 {
		s += fmt.Sprintf(", %d deleted", r.Deleted)
	}
	if r.Note != "" {
		s += " (" + r.Note + ")"
	}
	return s
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// RunOnce makes a backup if the files changed since the last one (force makes one anyway) and prunes the old
// ones. It is what the job does every Interval and what a clean stop does once more.
func (b *Backuper) RunOnce(ctx context.Context, force bool) (Result, error) {
	c, err := b.Client()
	if err != nil {
		return Result{Note: "no object store is set up"}, nil
	}
	names := Sources(b.Dir, b.Remote())
	if len(names) == 0 {
		return Result{Note: "nothing to back up"}, nil
	}
	fp := fingerprint(b.Dir, names)
	b.mu.Lock()
	unchanged := b.last.Fingerprint == fp
	b.mu.Unlock()
	if unchanged && !force {
		return Result{Note: "nothing changed"}, nil
	}

	var archive []byte
	var m Manifest
	for attempt := 0; attempt < 3; attempt++ {
		archive, m, err = Build(b.Dir, b.Name(), names, b.now())
		if !errors.Is(err, ErrInconsistent) {
			break
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err != nil {
		b.fail(err)
		return Result{}, err
	}
	key := b.archiveKey(m.CreatedAt)
	sum := SHA256(archive)
	if err := c.Put(ctx, key, archive, "application/gzip", map[string]string{"sha256": sum, "fingerprint": m.Fingerprint}); err != nil {
		b.fail(err)
		return Result{}, fmt.Errorf("uploading the archive: %w", err)
	}
	var files []string
	for _, f := range m.Files {
		files = append(files, f.Name)
	}
	latest := Latest{Key: key, Size: int64(len(archive)), SHA256: sum, CreatedAt: m.CreatedAt, Fingerprint: m.Fingerprint, Files: files}
	data, _ := json.Marshal(latest)
	if err := c.Put(ctx, b.prefix()+"latest.json", data, "application/json", nil); err != nil {
		b.fail(err)
		return Result{}, fmt.Errorf("writing latest.json: %w", err)
	}
	b.mu.Lock()
	b.last = latest
	b.mu.Unlock()

	res := Result{Uploaded: true, Key: key, Size: int64(len(archive)), Files: len(files)}
	kept, deleted, note := b.prune(ctx, c, key)
	res.Kept, res.Deleted, res.Note = kept, deleted, note
	b.mu.Lock()
	b.status.LastOK, b.status.LastKey, b.status.LastSize, b.status.Kept = b.now(), key, latest.Size, kept
	b.status.LastErr = ""
	b.status.Files = files
	b.mu.Unlock()
	return res, nil
}

// prune deletes the archives that retention drops. It never runs on a listing it could not read whole, and the
// first time in a process it only logs what it would delete.
func (b *Backuper) prune(ctx context.Context, c *objstore.Client, protect string) (kept, deleted int, note string) {
	entries, err := b.entries(ctx, c)
	if err != nil {
		return 0, 0, "retention skipped: " + err.Error()
	}
	keep, drop := Prune(entries, b.now(), protect)
	b.mu.Lock()
	dry := !b.pruned
	b.pruned = true
	b.mu.Unlock()
	if dry && len(drop) > 0 {
		var names []string
		for _, d := range drop {
			names = append(names, d.Key)
		}
		log.Printf("backup: the first retention run of this process would delete %d archives and does not: %s", len(drop), strings.Join(names, ", "))
		return len(keep) + len(drop), 0, "first retention run: nothing deleted"
	}
	for _, d := range drop {
		if err := c.Delete(ctx, d.Key); err != nil {
			log.Printf("backup: deleting %s: %v", d.Key, err)
			continue
		}
		deleted++
	}
	return len(keep), deleted, ""
}

// Start runs the backup job: the first run two minutes after the start, then every Interval. It reports each
// run to utils.Jobs and returns what stops it.
func (b *Backuper) Start() (stop func()) {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return func() {}
	}
	b.started = true
	b.mu.Unlock()
	if _, err := b.Client(); err != nil {
		utils.Jobs.RegisterOff("backup", "Backup to object storage", "Archives the database files, the settings, the audit log and the usage totals to the object store when they changed.", "no object store is set up (OPENREPL_S3_*)")
		return func() {}
	}
	report := utils.Jobs.Register("backup", "Backup to object storage", "Archives the database files, the settings, the audit log and the usage totals to the object store when they changed, and keeps the last 24 hourly and 7 daily archives.", Interval)
	done := make(chan struct{})
	run := func() {
		report.Run(func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			res, err := b.RunOnce(ctx, false)
			if err != nil {
				log.Println("backup: ", err)
				return "", err
			}
			if res.Uploaded {
				log.Println("backup: ", res.String())
			}
			return res.String(), nil
		})
	}
	go func() {
		first := time.NewTimer(firstAfter)
		defer first.Stop()
		select {
		case <-first.C:
		case <-done:
			return
		}
		run()
		t := time.NewTicker(Interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				run()
			case <-done:
				return
			}
		}
	}()
	return func() { b.stopOnce.Do(func() { close(done) }) }
}

// Final makes one more backup when the server stops cleanly, once the databases are closed, so that the
// archive holds what was written last. It gives up after the timeout.
func (b *Backuper) Final(timeout time.Duration) {
	if _, err := b.Client(); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if res, err := b.RunOnce(ctx, false); err != nil {
		log.Println("backup: the last backup of this run failed:", err)
	} else if res.Uploaded {
		log.Println("backup: the last backup of this run:", res.String())
	}
}

// ---- restore ---------------------------------------------------------------------------------------

// isEmpty reports whether the folder has none of the files a backup holds: a fresh disk.
func (b *Backuper) isEmpty() bool {
	return len(RestoreFiles(b.Dir, b.Remote())) == 0
}

// RestoreIfEmpty puts the newest backup back when this server's folder has none of the files a backup holds.
// It never replaces a file that is there. It is called before the databases are opened. An error leaves the
// server starting as it would have without a backup.
func (b *Backuper) RestoreIfEmpty(ctx context.Context) (*Restored, error) {
	c, err := b.Client()
	if err != nil {
		return nil, nil // no object store: nothing to restore from
	}
	if !b.isEmpty() {
		return nil, nil
	}
	archive, latest, err := b.newestValid(ctx, c)
	if err != nil {
		return nil, err
	}
	if archive == nil {
		return nil, nil // no backup yet: a new deployment
	}
	m, files, err := Open(archive)
	if err != nil {
		return nil, err
	}
	// written into a folder of its own and moved, file by file, to where it belongs: a file that is already
	// there stays
	tmp, err := ioutil.TempDir(b.Dir, ".restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var restored []string
	remote := b.Remote()
	for _, f := range m.Files {
		if remote != "" && !isSmallFile(f.Name) {
			continue // the databases are in MongoDB or Firestore, which wins
		}
		dest := filepath.Join(b.Dir, f.Name)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		part := filepath.Join(tmp, f.Name)
		if err := ioutil.WriteFile(part, files[f.Name], 0600); err != nil {
			return nil, err
		}
		if err := os.Rename(part, dest); err != nil {
			return nil, err
		}
		restored = append(restored, f.Name)
	}
	r := &Restored{Key: latest.Key, CreatedAt: m.CreatedAt, Files: restored, At: b.now()}
	b.mu.Lock()
	b.status.Restored = r
	b.last = Latest{Key: latest.Key, Fingerprint: "", Size: latest.Size, SHA256: latest.SHA256, CreatedAt: m.CreatedAt}
	b.mu.Unlock()
	return r, nil
}

// newestValid finds the newest archive that can be used: the one latest.json names, and if that is missing or
// damaged, the newest of the others, up to three.
func (b *Backuper) newestValid(ctx context.Context, c *objstore.Client) ([]byte, Latest, error) {
	var tried []string
	if raw, _, err := c.Get(ctx, b.prefix()+"latest.json"); err == nil {
		var l Latest
		if json.Unmarshal(raw, &l) == nil && l.Key != "" {
			if data, _, err := c.Get(ctx, l.Key); err == nil && SHA256(data) == l.SHA256 {
				return data, l, nil
			}
			tried = append(tried, l.Key)
			log.Printf("backup: %s is missing or damaged, looking for an older one", l.Key)
		}
	} else if !errors.Is(err, objstore.ErrNotFound) {
		return nil, Latest{}, err
	}
	entries, err := b.entries(ctx, c)
	if err != nil {
		return nil, Latest{}, err
	}
	// newest first
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[j].At.After(entries[i].At) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
	attempts := 0
	for _, e := range entries {
		skip := false
		for _, t := range tried {
			if t == e.Key {
				skip = true
			}
		}
		if skip {
			continue
		}
		if attempts++; attempts > 3 {
			break
		}
		data, _, err := c.Get(ctx, e.Key)
		if err != nil {
			continue
		}
		if _, _, err := Open(data); err != nil {
			log.Printf("backup: %s cannot be used: %v", e.Key, err)
			continue
		}
		return data, Latest{Key: e.Key, Size: int64(len(data)), SHA256: SHA256(data), CreatedAt: e.At}, nil
	}
	if len(entries) > 0 && attempts > 0 {
		return nil, Latest{}, errors.New("backup: none of the newest archives could be used")
	}
	return nil, Latest{}, nil
}

func isSmallFile(name string) bool {
	for _, n := range smallFiles {
		if n == name {
			return true
		}
	}
	return false
}
