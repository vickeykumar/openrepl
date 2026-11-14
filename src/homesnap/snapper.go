// Package homesnap keeps a copy of the workspaces of signed-in users in the object store (S3, R2), and puts
// one back when its folder is missing (a host that keeps no disk between deploys). Only the node that is the
// authority for a home does it: the gateway (which holds a copy of every home) or a single server, never a
// worker (workers have no keys). Guest workspaces are not kept. The sync engine (package wsync) stays as it
// is: after a restore it reconciles the home with the worker as it always does, and the worker's newer files
// win by its usual rules.
//
// A snapshot is a new object, homes/<name>/<home>/<generation>-<time>.tar.gz, never overwritten; a small
// latest.json beside them, written last, says which is the newest complete one. A snapshot that dies on the
// way is never the one that is restored.
package homesnap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"objstore"
	"utils"
)

const (
	// Interval is how often the homes that changed are snapshotted; a home is snapshotted only after Settle has
	// passed with no change in it
	Interval = 5 * time.Minute
	Settle   = 30 * time.Second
	// Keep is how many snapshots of a home are kept
	Keep = 3
	// a home that has no snapshot is not asked about again for this long
	noSnapshotFor = 10 * time.Minute
	timeLayout    = "20060102T150405Z"
	// a restore that failed with an error is tried again at the next request, at most this often, and this many times
	restoreRetryAfter = time.Minute
	maxRestoreTries   = 10
)

// ErrConflict is what SnapshotHome answers when another writer advanced the home's pointer meanwhile.
var ErrConflict = errors.New("homesnap: another writer made a newer snapshot of this home")

// Latest is what latest.json holds.
type Latest struct {
	Generation int64     `json:"generation"`
	Key        string    `json:"key"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	Writer     string    `json:"writer"`
	CreatedAt  time.Time `json:"createdAt"`
	Forced     bool      `json:"-"` // read as it is, because no snapshot matched its manifest
}

var homeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// IsKept says that the home of this name is one a snapshot is made of: a signed-in user's, not a guest's, not a
// folder of the sandboxes.
func IsKept(home string) bool {
	return homeName.MatchString(home) && !strings.HasPrefix(home, "guest-")
}

// Snapper keeps the homes under Base.
type Snapper struct {
	Base   func() string // utils.HOME_DIR
	Name   func() string // names this deployment in the bucket
	Client func() (*objstore.Client, error)
	Now    func() time.Time

	// beforePointer is for tests: it runs after the archive is uploaded and before the pointer is written
	beforePointer func(home string)

	mu       sync.Mutex
	interval time.Duration // set from the admin dashboard; 0: Interval
	wake     chan struct{} // a new interval is to be used now (made by Start)
	enabled  bool
	writer   string
	homes    map[string]*homeState
	started  bool
	status   Status
}

type homeState struct {
	fingerprint string    // of what was last snapshotted or restored
	generation  int64     // the last one this process knows
	noSnapshot  time.Time // asked at this time and there was none
	busy        bool
	touched     bool      // Touch has looked at its folder, and the restore (if one was due) has answered
	pending     bool      // Touch has begun, the restore has not answered: no snapshot of the folder is made yet
	restoring   bool      // a restore is under way for this home (Touch)
	tries       int       // restores of this home that failed with an error
	retryAt     time.Time // not before this time is a failed restore tried again
	changing    int       // rounds in a row in which the home was still changing
	lastNote    string    // why the last round did not snapshot it ("" when it did)
	lastRestore time.Time // the last time a snapshot was got back by the user
}

// Status is what the dashboard shows.
type Status struct {
	Homes      int // homes known to this process
	LastRun    time.Time
	LastCount  int // snapshots made by the last run
	Total      int64
	LastErr    string
	LastErrAt  time.Time
	Restores   int64
	LastRestor string
	// what the last round did, in words, and why each home was not snapshotted
	LastRound string
	Notes     map[string]string
}

// Default is the snapshotter of this server. It does nothing until Enable is called.
var Default = &Snapper{
	Base:   func() string { return utils.HOME_DIR },
	Name:   utils.BackupName,
	Client: objstore.Default,
	Now:    time.Now,
}

// SetInterval sets how often the homes that changed are snapshotted (0: the built-in Interval). It is used from the next
// round on; a job that is waiting is told to wait for the new time instead.
func (s *Snapper) SetInterval(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.mu.Lock()
	changed := s.interval != d
	s.interval = d
	wake := s.wake
	s.mu.Unlock()
	if changed && wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// CurrentInterval is the time between two rounds in force.
func (s *Snapper) CurrentInterval() time.Duration {
	s.mu.Lock()
	d := s.interval
	s.mu.Unlock()
	if d <= 0 {
		return Interval
	}
	return d
}

func (s *Snapper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Enable lets the snapshotter work (it is not enabled on a worker).
func (s *Snapper) Enable() {
	s.mu.Lock()
	s.enabled = true
	s.mu.Unlock()
}

// Enabled says whether it works: enabled, and an object store is set up.
func (s *Snapper) Enabled() bool {
	s.mu.Lock()
	on := s.enabled
	s.mu.Unlock()
	if !on {
		return false
	}
	_, err := s.Client()
	return err == nil
}

// Status reports what it did.
func (s *Snapper) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Homes = len(s.homes)
	st.Notes = map[string]string{}
	for h, hs := range s.homes {
		if hs.lastNote != "" {
			st.Notes[h] = hs.lastNote
		}
	}
	return st
}

func (s *Snapper) writerID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer == "" {
		b := make([]byte, 6)
		rand.Read(b)
		host, _ := os.Hostname()
		s.writer = host + "-" + hex.EncodeToString(b)
	}
	return s.writer
}

func (s *Snapper) state(home string) *homeState {
	if s.homes == nil {
		s.homes = map[string]*homeState{}
	}
	h := s.homes[home]
	if h == nil {
		h = &homeState{}
		s.homes[home] = h
	}
	return h
}

func (s *Snapper) prefix(home string) string { return "homes/" + s.Name() + "/" + home + "/" }

func (s *Snapper) dir(home string) string { return filepath.Join(s.Base(), home) }

func (s *Snapper) fail(err error) {
	s.mu.Lock()
	s.status.LastErr, s.status.LastErrAt = err.Error(), s.now()
	s.mu.Unlock()
}

// readLatest reads latest.json of a home (ErrNotFound when there is none).
func (s *Snapper) readLatest(ctx context.Context, c *objstore.Client, home string) (Latest, error) {
	var l Latest
	raw, _, err := c.Get(ctx, s.prefix(home)+"latest.json")
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(raw, &l); err != nil || l.Key == "" {
		return l, errors.New("homesnap: latest.json cannot be read")
	}
	return l, nil
}

// ---- snapshot --------------------------------------------------------------------------------------

// Result is what one snapshot did.
type Result struct {
	Made       bool
	Generation int64
	Size       int64
	Files      int
	Skipped    int
	Deleted    int
	// VersionsRemoved is how many older versions of the home's files the bucket was asked to drop (versions.go)
	VersionsRemoved int
	Note            string
}

// SnapshotHome makes a snapshot of one home if it changed since the last one and has settled (nothing in it
// changed for Settle); force skips both checks. It is safe to call from several places: a home that is being
// snapshotted is skipped.
func (s *Snapper) SnapshotHome(ctx context.Context, home string, force bool) (Result, error) {
	return s.snapshot(ctx, home, force, !force)
}

// snapshot is SnapshotHome with the wait for a home to settle apart from the check for a change.
func (s *Snapper) snapshot(ctx context.Context, home string, force, settle bool) (Result, error) {
	if !IsKept(home) {
		return Result{Note: "not a home that is kept"}, nil
	}
	c, err := s.Client()
	if err != nil {
		return Result{Note: "no object store is set up"}, nil
	}
	dir := s.dir(home)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return s.noted(home, Result{Note: "this server has no folder for it"}), nil
	}
	_, fp, err := scan(dir)
	if err != nil {
		s.fail(err)
		return Result{}, err
	}
	s.mu.Lock()
	st := s.state(home)
	if st.busy {
		s.mu.Unlock()
		return Result{Note: "being snapshotted"}, nil
	}
	if st.fingerprint == fp && !force {
		st.lastNote, st.changing = "", 0
		s.mu.Unlock()
		return Result{Note: "unchanged"}, nil
	}
	if st.pending {
		// the folder has not been looked at for a restore yet (or the restore failed and will be tried again): what is on
		// the disk now may be a fresh home, and a snapshot of it would take the place of the good one
		s.mu.Unlock()
		return s.noted(home, Result{Note: "waiting for its restore"}), nil
	}
	prevGen := st.generation
	st.busy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); st.busy = false; s.mu.Unlock() }()

	if settle {
		if last := newest(dir); !last.IsZero() && s.now().Sub(last) < Settle {
			// a workspace that never rests (a terminal that keeps writing) is snapshotted anyway after a few
			// rounds, as it is: waiting for it would keep it out of the bucket for ever
			s.mu.Lock()
			st.changing++
			waited := st.changing
			s.mu.Unlock()
			if waited <= maxWaitRounds {
				return s.noted(home, Result{Note: "still changing"}), nil
			}
		}
	}

	// the generation after the newest one in the bucket
	latest, lerr := s.readLatest(ctx, c, home)
	if lerr != nil && !errors.Is(lerr, objstore.ErrNotFound) {
		s.fail(lerr)
		return Result{}, lerr
	}
	gen := latest.Generation
	if prevGen > gen {
		gen = prevGen
	}
	gen++
	archive, m, err := Build(dir, home, gen, s.writerID(), s.now())
	if err != nil {
		s.fail(err)
		return Result{}, err
	}
	// one file for each UTC day, overwritten by every snapshot of that day (so it holds the end of the day, and the store's
	// own versions hold the day's history)
	key := s.dayKey(home)
	hash := sum(archive)
	meta := map[string]string{"sha256": hash, "generation": fmt.Sprint(gen), "created": m.CreatedAt.UTC().Format(time.RFC3339)}
	// another writer (an overlapping deploy) that already wrote a newer snapshot of this day is not overwritten
	if cur, herr := c.Head(ctx, key); herr == nil {
		var have int64
		fmt.Sscan(cur.Meta["generation"], &have)
		if have >= gen {
			s.fail(ErrConflict)
			return Result{}, ErrConflict
		}
	} else if !errors.Is(herr, objstore.ErrNotFound) {
		s.fail(herr)
		return Result{}, herr
	}
	if err := c.Put(ctx, key, archive, "application/gzip", meta); err != nil {
		s.fail(err)
		return Result{}, fmt.Errorf("uploading the snapshot: %w", err)
	}
	if s.beforePointer != nil {
		s.beforePointer(home)
	}
	// the pointer moves only if nobody else moved it since it was read
	now, nerr := s.readLatest(ctx, c, home)
	if (nerr == nil && now.Generation != latest.Generation) || (nerr != nil && !errors.Is(nerr, objstore.ErrNotFound)) || (errors.Is(nerr, objstore.ErrNotFound) && lerr == nil) {
		if nerr != nil && !errors.Is(nerr, objstore.ErrNotFound) {
			s.fail(nerr)
			return Result{}, nerr
		}
		s.fail(ErrConflict)
		return Result{}, ErrConflict
	}
	l := Latest{Generation: gen, Key: key, SHA256: hash, Size: int64(len(archive)), Writer: s.writerID(), CreatedAt: m.CreatedAt}
	raw, _ := json.Marshal(l)
	if err := c.Put(ctx, s.prefix(home)+"latest.json", raw, "application/json", nil); err != nil {
		s.fail(err)
		return Result{}, fmt.Errorf("writing latest.json: %w", err)
	}
	s.mu.Lock()
	st.fingerprint, st.generation = fp, gen
	st.lastNote, st.changing = "", 0
	s.status.Total++
	s.status.LastErr = ""
	s.mu.Unlock()
	res := Result{Made: true, Generation: gen, Size: int64(len(archive)), Files: len(m.Entries), Skipped: len(m.Skipped)}
	res.Deleted = s.prune(ctx, c, home, key)
	res.VersionsRemoved = s.pruneVersions(ctx, c, home)
	return res, nil
}

// maxWaitRounds is how many rounds a home that keeps changing is left alone before it is snapshotted anyway.
const maxWaitRounds = 2

// noted remembers why a home was not snapshotted this time, and logs it when the reason changes.
func (s *Snapper) noted(home string, r Result) Result {
	s.mu.Lock()
	st := s.state(home)
	changed := st.lastNote != r.Note
	st.lastNote = r.Note
	s.mu.Unlock()
	if changed {
		log.Printf("homesnap: %s is not snapshotted: %s", home, r.Note)
	}
	return r
}

// Touch tells the snapshotter that a signed-in user's home is in use: it is snapshotted from now on, and if
// its folder is missing it is restored first (this blocks for as long as the restore takes, at most a minute).
func (s *Snapper) Touch(home string) {
	if !IsKept(home) || !s.Enabled() {
		return
	}
	s.mu.Lock()
	st := s.state(home)
	if st.touched || st.restoring || (!st.retryAt.IsZero() && s.now().Before(st.retryAt)) {
		s.mu.Unlock()
		return // looked at already (this is called on every request), or under way, or a failed one is not tried again yet
	}
	st.restoring, st.pending = true, true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := s.Ensure(ctx, home)
	s.mu.Lock()
	defer s.mu.Unlock()
	st.restoring = false
	if err != nil {
		// not answered: the next request tries again, so that a timeout of the object store right after a deploy does not leave
		// the home without its files for good
		st.tries++
		if st.tries >= maxRestoreTries {
			st.touched, st.pending = true, false
			log.Printf("homesnap: restoring %s failed %d times, giving up for this process: %v", home, st.tries, err)
			return
		}
		st.retryAt = s.now().Add(restoreRetryAfter)
		log.Printf("homesnap: restoring %s: %v (try %d of %d, again at the next request after %s)", home, err, st.tries, maxRestoreTries, st.retryAt.Format("15:04:05"))
		return
	}
	st.touched, st.pending, st.tries = true, false, 0
}

// ---- restore ---------------------------------------------------------------------------------------

// ErrBusy is what Ensure answers when the home is being snapshotted or restored just now: it has not looked.
var ErrBusy = errors.New("homesnap: the home is busy")

// madeByTheServer says that a folder holds nothing a user made: it is empty, or every name in it begins with a dot
// (.bashrc that a terminal writes, the histories of the REPLs, .tmp). Such a home is a new one, and the snapshot of
// the user's files is put back into it.
func madeByTheServer(dir string) bool {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return os.IsNotExist(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return false
		}
	}
	return true
}

// Ensure puts a home back from its newest good snapshot when its folder is missing, empty, or holds only what the
// server makes itself, and says whether it did. A home with a file of the user's is left alone. A home with no
// snapshot is not asked about again for ten minutes.
func (s *Snapper) Ensure(ctx context.Context, home string) (bool, error) {
	if !IsKept(home) {
		return false, nil
	}
	c, err := s.Client()
	if err != nil {
		return false, nil
	}
	dir := s.dir(home)
	if !madeByTheServer(dir) {
		return false, nil
	}
	s.mu.Lock()
	st := s.state(home)
	if st.busy {
		s.mu.Unlock()
		return false, ErrBusy
	}
	if !st.noSnapshot.IsZero() && s.now().Sub(st.noSnapshot) < noSnapshotFor {
		s.mu.Unlock()
		return false, nil
	}
	st.busy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); st.busy = false; s.mu.Unlock() }()

	archive, latest, err := s.newestValid(ctx, c, home)
	if err != nil {
		s.fail(err)
		return false, err
	}
	if archive == nil {
		s.mu.Lock()
		st.noSnapshot = s.now()
		s.mu.Unlock()
		return false, nil
	}
	// written into a folder of its own and moved into place, so that a restore that stops leaves no home
	// that looks whole
	tmp := filepath.Join(s.Base(), fmt.Sprintf(".restore-%s-%d", home, s.now().UnixNano()))
	extractor := Extract
	if latest.Forced {
		extractor = extractForced
	}
	if _, err := extractor(archive, tmp); err != nil {
		s.fail(err)
		return false, err
	}
	if err := os.MkdirAll(s.Base(), 0777); err != nil {
		os.RemoveAll(tmp)
		return false, err
	}
	if err := mergeInto(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return false, err
	}
	_, fp, _ := scan(dir)
	s.mu.Lock()
	st.fingerprint, st.generation = fp, latest.Generation
	s.status.Restores++
	s.status.LastRestor = home + " from generation " + fmt.Sprint(latest.Generation)
	s.mu.Unlock()
	log.Printf("homesnap: %s was restored from its snapshot of %s (generation %d)", home, latest.CreatedAt.Format(time.RFC3339), latest.Generation)
	return true, nil
}

// ---- the job ---------------------------------------------------------------------------------------

// Round is what one round of snapshots did.
type Round struct {
	Made   int
	Notes  map[string]int // why the others were not snapshotted, and how many
	Errors int
}

// String says it in a line: "1 snapshot made; 1 home has no folder on this server".
func (r Round) String() string {
	parts := []string{fmt.Sprintf("%d snapshots made", r.Made)}
	var reasons []string
	for note := range r.Notes {
		reasons = append(reasons, note)
	}
	sort.Strings(reasons)
	for _, note := range reasons {
		parts = append(parts, fmt.Sprintf("%d not snapshotted: %s", r.Notes[note], note))
	}
	if r.Errors > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", r.Errors))
	}
	return strings.Join(parts, "; ")
}

// SnapshotAll snapshots the homes this process knows that changed and settled.
func (s *Snapper) SnapshotAll(ctx context.Context) (Round, error) {
	s.mu.Lock()
	var homes []string
	for h := range s.homes {
		homes = append(homes, h)
	}
	s.mu.Unlock()
	sort.Strings(homes)
	round := Round{Notes: map[string]int{}}
	var err error
	for _, h := range homes {
		res, e := s.SnapshotHome(ctx, h, false)
		switch {
		case e != nil:
			err = e
			round.Errors++
		case res.Made:
			round.Made++
		default:
			round.Notes[res.Note]++
		}
		if ctx.Err() != nil {
			break
		}
	}
	s.mu.Lock()
	s.status.LastRun, s.status.LastCount, s.status.LastRound = s.now(), round.Made, round.String()
	s.mu.Unlock()
	return round, err
}

// Start runs the job that snapshots the homes every Interval and reports to utils.Jobs; it returns what stops it.
func (s *Snapper) Start() (stop func()) {
	s.mu.Lock()
	if s.started || !s.enabled {
		s.mu.Unlock()
		return func() {}
	}
	s.started = true
	s.mu.Unlock()
	const what = "Snapshots the workspaces of signed-in users that changed to the object store, so that a disk that is wiped loses none of them."
	if _, err := s.Client(); err != nil {
		utils.Jobs.RegisterOff("home-snapshots", "Workspace snapshots", what, "no object store is set up (OPENREPL_S3_*)")
		return func() {}
	}
	report := utils.Jobs.Register("home-snapshots", "Workspace snapshots", what, s.CurrentInterval())
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	s.mu.Lock()
	s.wake = wake
	s.mu.Unlock()
	go func() {
		timer := time.NewTimer(s.CurrentInterval())
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				report.Run(func() (string, error) {
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
					defer cancel()
					round, err := s.SnapshotAll(ctx)
					return round.String(), err
				})
				timer.Reset(s.CurrentInterval())
			case <-wake:
				// the interval was changed in the dashboard: the wait starts again with the new time
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(s.CurrentInterval())
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// Final snapshots every home that changed, once, when the server stops cleanly (nothing waits for a home to
// settle: the sessions are over).
func (s *Snapper) Final(timeout time.Duration) {
	if !s.Enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.mu.Lock()
	var homes []string
	for h := range s.homes {
		homes = append(homes, h)
	}
	s.mu.Unlock()
	made := 0
	for _, h := range homes {
		if res, err := s.snapshot(ctx, h, false, false); err != nil {
			log.Printf("homesnap: the last snapshot of %s failed: %v", h, err)
		} else if res.Made {
			made++
		}
		if ctx.Err() != nil {
			break
		}
	}
	if made > 0 {
		log.Printf("homesnap: %d workspaces were snapshotted at the stop", made)
	}
}

// mergeInto puts the entries of src (a folder that was extracted whole) into dst, which may hold the files the server made
// itself: an entry that is in both takes the snapshot's version, an entry only in dst stays. A dst that is not there is
// simply replaced by src (one rename, as before).
func mergeInto(src, dst string) error {
	if entries, err := ioutil.ReadDir(dst); err != nil || len(entries) == 0 {
		os.Remove(dst) // an empty folder in the way
		return os.Rename(src, dst)
	}
	entries, err := ioutil.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		to := filepath.Join(dst, e.Name())
		if err := os.RemoveAll(to); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(src, e.Name()), to); err != nil {
			return err
		}
	}
	return os.RemoveAll(src)
}
