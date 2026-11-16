package appstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// KV is the part of a persist store that the App Store needs (persist.Store satisfies it).
type KV interface {
	Store(key, value []byte) error
	Fetch(key []byte) ([]byte, error)
	Delete(key []byte) error
	Commit() error
	Each(fn func(key, value []byte) bool) error
}

// The states of an app.
const (
	StateInstalling = "installing"
	StateInstalled  = "installed"
	StateFailed     = "failed"
)

// The kinds of an app.
const (
	KindLib    = "lib"    // a Python library (conda-forge)
	KindTool   = "tool"   // a program (conda-forge)
	KindNode   = "node"   // a Node library (npm)
	KindLang   = "lang"   // a language: a program that runs code (conda-forge); an admin installs it
	KindPlugin = "plugin" // an editor plugin that comes with the server; an admin installs it
	KindGoMod  = "gomod"  // a Go module, mirrored from the Go module proxy (LLD 18, "mirrors")
	KindJar    = "jar"    // a Java library: its jars and what they need, from Maven Central
	KindCrate  = "crate"  // a Rust crate, vendored from crates.io
	KindPy27   = "py27"   // a Python 2.7 library (conda-forge, in an environment with Python 2.7)
)

// Errors a caller turns into a message.
var (
	ErrNeedsApproval = errors.New("this app is not on the safe list: ask for it, and an admin decides")
	ErrBusy          = errors.New("this app is being installed already")
	ErrInstalled     = errors.New("this app is installed already")
	ErrBuiltin       = errors.New("this is built in: the server already has it")
	ErrFull          = errors.New("the store is full: an admin has to make room")
	ErrTooSoon       = errors.New("try again in a minute")
	ErrUnknown       = errors.New("no such app")
	ErrTooMany       = errors.New("too many apps are on: turn one off first")
	ErrDaily         = errors.New("you have made as many requests as are allowed today: try again tomorrow")
	ErrInUse         = errors.New("other users have it on")
)

// MaxOn is how many apps one user may have on.
const MaxOn = 40

// App is an app that has been (or is being) installed on this site. One version of each, pinned when it was installed.
type App struct {
	Name     string  `json:"name"`
	Version  string  `json:"version"`
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	State    string  `json:"state"`
	Size     int64   `json:"size,omitempty"`
	By       string  `json:"by,omitempty"`
	At       int64   `json:"at"`
	Err      string  `json:"err,omitempty"`
	Log      string  `json:"log,omitempty"`
	Retried  int64   `json:"retried,omitempty"`
	Want     string  `json:"want,omitempty"`     // a version an admin picked
	Node     string  `json:"node,omitempty"`     // the server the install ran on
	LastUsed int64   `json:"lastUsed,omitempty"` // the last time a user switched it on
	Packages int     `json:"packages,omitempty"` // what the solve brought with it
	Locked   bool    `json:"locked,omitempty"`   // the exact packages of the install are saved (a Lock): every node installs those
	Download float64 `json:"download,omitempty"` // MB fetched for it
	Manager  string  `json:"manager,omitempty"`  // what made the bundle (pixi, npm, apt, ...): a node makes it the same way
	Full     bool    `json:"full,omitempty"`     // the app did not run after the trim, so its bundle is whole
	Trimmed  int     `json:"trimmed,omitempty"`  // files the trim deleted
	Partial  bool    `json:"partial,omitempty"`  // solved for the gateway's CPU type only: every node installs the best version that exists for its own CPU type
	Waiting  bool    `json:"waiting,omitempty"`  // shown, not kept: it waits for another install (this node installs one at a time)
}

// Request is a user's ask for an app that is not on the safe list. One for each app: a second asker is added to it.
type Request struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Reason  string   `json:"reason,omitempty"`
	Askers  []string `json:"askers"`
	Status  string   `json:"status"` // waiting, rejected
	Answer  string   `json:"answer,omitempty"`
	At      int64    `json:"at"`
	Seen    []string `json:"seen,omitempty"` // askers that were told the answer
	Repl    string   `json:"repl,omitempty"` // the REPL (or place of a plugin) the asker wants it for
	Pending bool     `json:"-"`
}

// Report is a failed install that a user told the admin about.
type Report struct {
	Name      string   `json:"name"`
	Reporters []string `json:"reporters"`
	Note      string   `json:"note,omitempty"`
	Log       string   `json:"log,omitempty"`
	Status    string   `json:"status"` // open, fixed, rejected
	Answer    string   `json:"answer,omitempty"`
	At        int64    `json:"at"`
	Node      string   `json:"node,omitempty"` // the server the install failed on
	Version   string   `json:"version,omitempty"`
	Step      string   `json:"step,omitempty"`  // what it stopped at
	Fails     int      `json:"fails,omitempty"` // how many times the check of a switch-on failed (counted by the server, before anyone reported)
	Auto      bool     `json:"auto,omitempty"`  // made by the server from a failed check, not by a user
	Seen      []string `json:"seen,omitempty"`  // reporters that were told the answer
}

// Config is what the service is made of.
type Config struct {
	Store     Store
	KV        KV
	Installer Installer
	Apt       AptRunner       // installs the Debian packages an admin approved, nil for a node that does not
	Safe      func() []string // the names a user may install without asking
	LimitMB   func() int      // the most the store may use, 0 for no limit
	ReserveMB func() int      // room that is kept free for installs in progress: an install is refused when it would leave less (0: none)
	PerDay    func() int      // how many requests one user may make in a day (0: no limit)
	Now       func() time.Time
	// OnStep is told how the node stands after each bundle of an Apply ended (installed or failed), so an admin sees a long catch-up
	// go on; nil on a node that tells nobody.
	OnStep func(NodeState)
	// Lock gets the lock of a bundle from the gateway (a worker; nil on the node that has the catalog: it reads its own database). A
	// node that cannot get it does not install the bundle: it would not be the same one.
	Lock func(ctx context.Context, id string) (Lock, error)
}

// Service is the App Store of one server (the gateway or a standalone server holds the catalog; a worker only applies it).
type Service struct {
	cfg  Config
	mu   sync.Mutex // the records
	inst sync.Mutex // one install at a time
	jobs map[string]*Job
	wg   sync.WaitGroup

	builtins []Builtin // what the image has built in (guarded by mu)
	remote   []Entry   // the gateway's admin entries, on a node that has no catalog (guarded by remoteMu)
	remoteMu sync.Mutex

	gwFails map[string]int  // the gateway's own failed installs of an app in a row (for Backoff)
	retryOf map[string]bool // an install that is the gateway's own retry (it does not reset the count)
	retryMu sync.Mutex
	retry   map[string]retryEntry // the bundles that failed to install here: when to try again (memory only: a restart tries at once)
}

// retryEntry is a failed install of one bundle on this node.
type retryEntry struct {
	fails  int
	next   time.Time
	err    string
	gaveUp bool // not tried again by itself: a failure that cannot pass, or the last try was made
}

// Backoff is how long a node waits before it tries a bundle again after its 1st, 2nd, ... failure that may pass (the network, the disk); after the last it gives
// up and waits for an admin (Reinstall). A failure that cannot pass (no candidates for this CPU) is not tried again at all.
var Backoff = []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, 60 * time.Minute}

// Job is an install that is going on.
type Job struct {
	Name   string
	Step   string
	Index  int    // which of installSteps is going on
	Detail string // what the solve found, for the step list
	Start  time.Time
}

// New makes the service.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Safe == nil {
		cfg.Safe = func() []string { return nil }
	}
	if cfg.LimitMB == nil {
		cfg.LimitMB = func() int { return 0 }
	}
	if cfg.ReserveMB == nil {
		cfg.ReserveMB = func() int { return 0 }
	}
	if cfg.PerDay == nil {
		cfg.PerDay = func() int { return 0 }
	}
	cfg.KV = &lockedKV{inner: cfg.KV}
	return &Service{cfg: cfg, jobs: map[string]*Job{}, retry: map[string]retryEntry{}, gwFails: map[string]int{}, retryOf: map[string]bool{}}
}

// lockedKV makes every call of the store one at a time (a callback of Each must not call the store again).
type lockedKV struct {
	mu    sync.Mutex
	inner KV
}

func (l *lockedKV) Store(k, v []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Store(k, v)
}
func (l *lockedKV) Fetch(k []byte) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Fetch(k)
}
func (l *lockedKV) Delete(k []byte) error { l.mu.Lock(); defer l.mu.Unlock(); return l.inner.Delete(k) }
func (l *lockedKV) Commit() error         { l.mu.Lock(); defer l.mu.Unlock(); return l.inner.Commit() }
func (l *lockedKV) Each(fn func(k, v []byte) bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inner.Each(fn)
}

func (s *Service) now() int64 { return s.cfg.Now().Unix() }

// Store is the store of this server.
func (s *Service) Store() Store { return s.cfg.Store }

// Installer is the installer in use.
func (s *Service) Installer() Installer { return s.cfg.Installer }

// ---- records -------------------------------------------------------------------------------------------------------------

func (s *Service) put(key string, v interface{}) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.cfg.KV.Store([]byte(key), raw); err != nil {
		return err
	}
	return s.cfg.KV.Commit()
}

func (s *Service) get(key string, v interface{}) bool {
	raw, err := s.cfg.KV.Fetch([]byte(key))
	if err != nil || len(raw) == 0 {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

func (s *Service) del(key string) {
	s.cfg.KV.Delete([]byte(key))
	s.cfg.KV.Commit()
}

func (s *Service) each(prefix string, fn func(key string, raw []byte)) error {
	return s.cfg.KV.Each(func(k, v []byte) bool {
		if strings.HasPrefix(string(k), prefix) {
			fn(string(k), v)
		}
		return true
	})
}

// Apps lists the apps of the site.
func (s *Service) Apps() []App {
	out := []App{} // never nil: a nil list is "null" in JSON and the pages call .filter on it
	s.each("app:", func(_ string, raw []byte) {
		var a App
		if json.Unmarshal(raw, &a) == nil {
			out = append(out, a)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AppsWithWaiting is Apps with Waiting set on the ones that are only waiting for their turn (the job has not started its first step).
func (s *Service) AppsWithWaiting() []App {
	out := s.Apps()
	for i := range out {
		if out[i].State == StateInstalling {
			if j, ok := s.Job(out[i].Name); ok && j.Index < 0 {
				out[i].Waiting = true
			}
		}
	}
	return out
}

func (s *Service) app(name string) (App, bool) {
	var a App
	ok := s.get("app:"+name, &a)
	return a, ok
}

// Requests lists the requests (status waiting or rejected).
func (s *Service) Requests() []Request {
	out := []Request{}
	s.each("req:", func(_ string, raw []byte) {
		var r Request
		if json.Unmarshal(raw, &r) == nil {
			out = append(out, r)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// Reports lists the reports of failures.
func (s *Service) Reports() []Report {
	out := []Report{}
	s.each("rep:", func(_ string, raw []byte) {
		var r Report
		if json.Unmarshal(raw, &r) == nil {
			out = append(out, r)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// ownList is the bundles a user switched on (the user's own list).
func (s *Service) ownList(home string) []string {
	var ids []string
	s.get("user:"+home, &ids)
	return ids
}

// offList is the bundles a user switched off although an admin turned them on for everyone.
func (s *Service) offList(home string) []string {
	var ids []string
	s.get("off:"+home, &ids)
	return ids
}

// Defaults are the names of the apps an admin turned on for every signed-in user (the people who sign in later too); each user may turn
// one off for themselves.
func (s *Service) Defaults() []string {
	var names []string
	s.get("defaults", &names)
	if names == nil {
		names = []string{}
	}
	return names
}

// defaultIDs are the bundles of the installed apps among the Defaults.
func (s *Service) defaultIDs() []string {
	want := map[string]bool{}
	for _, n := range s.Defaults() {
		want[n] = true
	}
	var ids []string
	for _, a := range s.Apps() {
		if want[a.Name] && a.State == StateInstalled && a.ID != "" {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// MyList is the bundles that are on for a user: what the user switched on and what is on for everyone (less what the user switched off).
func (s *Service) MyList(home string) []string {
	own := s.ownList(home)
	if !Kept(home) {
		return own
	}
	return merge(s.defaultIDs(), s.offList(home), own)
}

// SetDefault turns an installed or installing app on for every signed-in user (on) or stops doing that (not on). A user's own choice
// stays: a user who turned it off keeps it off, and one who turned it on keeps it on.
func (s *Service) SetDefault(admin, name string, on bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.app(name); !ok {
		return ErrUnknown
	}
	var next []string
	for _, n := range s.Defaults() {
		if n != name {
			next = append(next, n)
		}
	}
	if on {
		next = append(next, name)
	}
	if next == nil {
		next = []string{}
	}
	if err := s.put("defaults", next); err != nil {
		return err
	}
	log.Printf("appstore: %s turned %s %s for everyone", admin, name, map[bool]string{true: "on", false: "off"}[on])
	return s.refreshDefaults()
}

// refreshDefaults writes the list of what is on for everyone and the env.sh of every user that has files here.
func (s *Service) refreshDefaults() error {
	if err := s.cfg.Store.WriteDefaults(s.defaultIDs()); err != nil {
		return err
	}
	for _, home := range s.cfg.Store.Homes() {
		s.cfg.Store.WriteHome(home, s.ownList(home), s.offList(home))
	}
	return nil
}

func (s *Service) setOff(home string, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	return s.put("off:"+home, ids)
}

func (s *Service) setList(home string, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	return s.put("user:"+home, ids)
}

// ---- who may install -----------------------------------------------------------------------------------------------------

// IsSafe says whether a user may install the app without asking.
func (s *Service) IsSafe(name string) bool { return s.isSafe(KindLib, name) }

func (s *Service) isSafe(kind, name string) bool {
	if !safeKind(kind) {
		return false
	}
	for _, e := range s.cfg.Safe() {
		k, n := safeEntry(e)
		if n == name && (k == kind || (kind == KindTool && k == KindLib)) {
			return true
		}
	}
	// an entry of the catalogue that anyone may install
	for _, e := range s.Catalogue() {
		if e.Install == "anyone" && e.InstallKind() == kind && e.PackageName() == name {
			return true
		}
	}
	return false
}

func cleanKind(k string) string {
	switch k {
	case KindTool, KindNode, KindLang, KindPlugin, KindGoMod, KindJar, KindCrate, KindPy27:
		return k
	}
	return KindLib
}

// safeEntry reads an entry of the safe list: "flask" is a library, "tool:jq" a tool, "npm:express" a Node library.
func safeEntry(e string) (kind, name string) {
	e = strings.ToLower(strings.TrimSpace(e))
	switch {
	case strings.HasPrefix(e, "npm:"):
		return KindNode, e[4:]
	case strings.HasPrefix(e, "tool:"):
		return KindTool, e[5:]
	}
	return KindLib, e
}

// safeKind says which kinds a user may install without asking (when the name is on the safe list): a language or a plugin is always
// an admin's to install.
func safeKind(k string) bool {
	return k == KindLib || k == KindTool || k == KindNode || IsMirrorKind(k) || k == KindPy27
}

// ---- install -------------------------------------------------------------------------------------------------------------

// installSteps are the stages of an install, in order; the page shows them as a list.
var installSteps = []string{"Checked that it is allowed", "Resolved the dependencies", "Downloading and linking", "Checking that it works"}

// Install starts the install of an app. A user may install the apps on the safe list; an admin any app. It returns at once: the
// work goes on in the background (Job and Views tell how far it is). Installing an app that failed before is a try again.
func (s *Service) Install(who, name, kind string, admin bool) error {
	return s.install(who, name, kind, admin, "")
}

// InstallVersion installs a version that an admin picked (for an app whose newest version does not solve or does not work).
func (s *Service) InstallVersion(who, name, kind, version string) error {
	if !ValidVersion(version) {
		return fmt.Errorf("%q is not a version", version)
	}
	return s.install(who, name, kind, true, version)
}

// Relock makes an installed app again with a fresh lock: the same version, solved now, so a worker or a wiped gateway that cannot get the
// packages of the old lock (a build conda-forge no longer has) gets what exists today. The bundle is gone from the gateway until the new
// install is done; the people who have it on keep their switch. The nodes are told by the caller (they reinstall it from the new lock).
func (s *Service) Relock(admin, name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	s.mu.Lock()
	a, ok := s.app(name)
	if !ok || a.State != StateInstalled || a.ID == "" {
		s.mu.Unlock()
		return "", ErrUnknown
	}
	if s.jobs[name] != nil {
		s.mu.Unlock()
		return "", ErrBusy
	}
	id := a.ID
	s.del("lock:" + id)
	s.cfg.Store.RemoveBundle(id)
	s.mu.Unlock()
	log.Printf("appstore: %s asked for a new lock of %s", admin, id)
	return id, s.installWith(admin, name, a.Kind, true, a.Version, true)
}

// refreshHomes writes again the files of every user that has a list (a bundle that came back is on their PATH again).
func (s *Service) refreshHomes() {
	snap := map[string][]string{}
	s.each("user:", func(key string, raw []byte) {
		var ids []string
		if json.Unmarshal(raw, &ids) == nil {
			snap[strings.TrimPrefix(key, "user:")] = ids
		}
	})
	for home, ids := range snap {
		if !ValidHome(home) {
			continue
		}
		var have []string
		for _, id := range ids {
			if s.cfg.Store.HasBundle(id) {
				have = append(have, id)
			}
		}
		s.cfg.Store.WriteHome(home, have, s.offList(home))
	}
}

func (s *Service) install(who, name, kind string, admin bool, version string) error {
	return s.installWith(who, name, kind, admin, version, false)
}

// installWith is install; again says that an app that is installed is to be made again (a fresh lock).
func (s *Service) installWith(who, name, kind string, admin bool, version string, again bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidName(name) {
		return fmt.Errorf("%q is not a package name", name)
	}
	kind = cleanKind(kind)
	if b, ok := s.BuiltinNamed(name); ok && (kind == KindLib || kind == KindTool || kind == KindLang) {
		return fmt.Errorf("%w (%s %s)", ErrBuiltin, b.Name, b.Version)
	}
	if !admin && !s.isSafe(kind, name) {
		return ErrNeedsApproval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retryOf[name] {
		delete(s.retryOf, name)
	} else {
		delete(s.gwFails, name) // somebody asked: the count of failed tries starts again
	}
	if old, ok := s.app(name); ok && old.Kind != "" && cleanKind(old.Kind) != kind && old.State != StateFailed {
		return fmt.Errorf("%s is installed already, as a %s", name, old.Kind)
	}
	if a, ok := s.app(name); ok {
		switch a.State {
		case StateInstalled:
			if !again {
				return ErrInstalled
			}
		case StateInstalling:
			if s.jobs[name] != nil {
				return ErrBusy
			}
		case StateFailed:
			if !admin && s.cfg.Now().Unix()-a.Retried < 60 {
				return ErrTooSoon
			}
		}
	}
	if limit := s.cfg.LimitMB(); limit > 0 && s.cfg.Store.Size() > int64(limit-s.cfg.ReserveMB())<<20 {
		return ErrFull
	}
	a := App{Name: name, Kind: kind, State: StateInstalling, By: who, At: s.now(), Retried: s.now(), Want: version}
	if err := s.put("app:"+name, a); err != nil {
		return err
	}
	s.jobs[name] = &Job{Name: name, Step: "Waiting its turn", Index: -1, Start: s.cfg.Now()}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runInstall(a)
	}()
	return nil
}

func (s *Service) step(name string, index int, detail string) {
	s.mu.Lock()
	if j := s.jobs[name]; j != nil {
		j.Index, j.Detail = index, detail
		if index >= 0 && index < len(installSteps) {
			j.Step = installSteps[index]
		}
	}
	s.mu.Unlock()
}

func (s *Service) finishJob(name string) {
	s.mu.Lock()
	delete(s.jobs, name)
	s.mu.Unlock()
}

func hostName() string {
	h, _ := os.Hostname()
	return h
}

func (s *Service) fail(a App, step string, err error, log string) {
	a.State, a.Err, a.Node = StateFailed, step+": "+tail(err.Error(), 300), hostName()
	a.Log = CleanLog(log, s.cfg.Store.Dir)
	s.mu.Lock()
	// a failure that may pass is tried again by itself a few times (Backoff), then given up; one that cannot pass (no candidates, not for this CPU) is not
	n := s.gwFails[a.Name] + 1
	var wait time.Duration
	switch {
	case IsPermanent(err):
		a.Err += " (not available here: no automatic retry)"
	case n <= len(Backoff):
		wait = Backoff[n-1]
		a.Err += fmt.Sprintf(" (trying again in %s)", wait)
	default:
		a.Err += " (gave up: press Retry)"
	}
	s.gwFails[a.Name] = n
	s.put("app:"+a.Name, a)
	s.mu.Unlock()
	s.finishJob(a.Name)
	if wait > 0 {
		name, kind, want, who := a.Name, a.Kind, a.Want, a.By
		time.AfterFunc(wait, func() { s.retryInstall(name, kind, want, who) })
	}
}

// retryInstall is the gateway's own try again of an install that failed in a way that may pass; it does nothing when the app is not failed any more
// (an admin retried it, it was removed) .
func (s *Service) retryInstall(name, kind, want, who string) {
	s.mu.Lock()
	a, ok := s.app(name)
	if !ok || a.State != StateFailed {
		s.mu.Unlock()
		return
	}
	s.retryOf[name] = true
	s.mu.Unlock()
	if err := s.installWith(who, name, kind, true, want, false); err != nil {
		s.mu.Lock()
		delete(s.retryOf, name)
		s.mu.Unlock()
	}
}

func (s *Service) runInstall(a App) {
	s.inst.Lock() // one at a time
	defer s.inst.Unlock()
	ctx0, cancel0 := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel0()
	ctx, cancel := context.WithCancelCause(ctx0)
	defer cancel(nil)
	var logb workLog
	if err := s.cfg.Store.Ready(); err != nil {
		s.fail(a, "Preparing the store", err, "")
		return
	}
	stopWatch := watch(cancel, &logb, watchDirs(s.cfg.Store.Dir)...)
	defer stopWatch()
	// an install that was stopped for no progress says so, not "signal: killed"
	cause := func(err error) error {
		if c := context.Cause(ctx); errors.Is(c, errNoProgress) {
			return c
		}
		return err
	}
	s.step(a.Name, 1, "")
	// the managers that can make it, the chosen one first: when it fails the next one is tried, and the admin is told
	order := []string{""}
	if e, ok := s.EntryFor(a.Name, cleanKind(a.Kind)); ok && cleanKind(a.Kind) != KindPlugin {
		if e.Minimal == nil && e.Kind == CKTool && s.canApt() {
			// never measured (an admin added it): the dry runs choose, once, and the choice is kept in the entry
			if ce, _ := s.Compare(ctx, e); ce.Minimal != nil {
				s.AddEntry("system", ce)
				e = ce
			}
		}
		order = managerOrder(e)
	}
	var dest, detail string
	var failures []string
	var trimRep TrimReport
	for i, mgr := range order {
		a.Manager = mgr
		mctx := WithManager(ctx, mgr)
		res, err := s.cfg.Installer.Resolve(mctx, a.Name, a.Want, a.Kind)
		partial := false
		if err != nil && (mgr == "" || mgr == MgrPixi) && condaKind(a.Kind) && (IsPermanent(err) || solveTimedOut(err)) {
			// it cannot be solved for every CPU type (a build that exists for one only): the best version for this CPU type is installed, and every other
			// node does the same for its own
			first := err
			if r2, e2 := s.cfg.Installer.Resolve(WithSingleCPU(mctx), a.Name, a.Want, a.Kind); e2 == nil {
				res, err, partial = r2, nil, true
				failures = append(failures, mgr+": not solved for every CPU type ("+tail(first.Error(), 120)+"): installed the best version for this CPU type")
			}
		}
		if err != nil {
			failures = append(failures, mgr+": "+tail(err.Error(), 200))
			if i == len(order)-1 {
				s.fail(a, "Resolving the dependencies", cause(err), err.Error())
				return
			}
			continue
		}
		a.Version, a.ID, a.Packages, a.Download, a.Partial = res.Version, BundleID(a.Name, res.Version), res.Packages, res.DownloadMB, partial
		dest = s.cfg.Store.BundlePath(a.ID)
		if dest == "" {
			s.fail(a, "Resolving the dependencies", fmt.Errorf("%s is not a usable version", res.Version), "")
			return
		}
		detail = fmt.Sprintf("%d packages", res.Packages)
		if res.DownloadMB > 0 {
			detail += fmt.Sprintf(", %.0f MB", res.DownloadMB)
		}
		os.RemoveAll(dest) // a half-made bundle of an earlier try
		s.step(a.Name, 2, detail)
		logb.Reset()
		ictx := ctx
		if partial {
			ictx = WithSingleCPU(ctx)
		}
		var full bool
		trimRep, full, err = s.installBundle(ictx, a, mgr, dest, &logb)
		if err == nil {
			a.Full, a.Trimmed = full, trimRep.Files
			break
		}
		err = cause(err)
		os.RemoveAll(dest)
		failures = append(failures, mgr+": "+tail(err.Error(), 200))
		if i == len(order)-1 {
			s.fail(a, "Downloading and linking", err, logb.String()+"\n"+strings.Join(failures, "\n"))
			return
		}
	}
	if len(failures) > 0 {
		s.NoteCheckFailure(a.Name, hostName(), "the first manager failed, "+a.Manager+" made it: "+strings.Join(failures, "; "))
	}
	s.step(a.Name, 3, detail)
	m, err := s.cfg.Store.Finish(a.ID, a.Name, a.Version)
	if err != nil {
		os.RemoveAll(dest)
		s.fail(a, "Checking that it works", err, "")
		return
	}
	a.State, a.Size, a.Err, a.Log, a.Node = StateInstalled, m.Size, "", "", hostName()
	a.Locked = !a.Partial && s.captureLock(a.ID, dest) // a lock of one CPU type is no use to the others: they solve for themselves
	s.mu.Lock()
	delete(s.gwFails, a.Name)
	s.put("app:"+a.Name, a)
	// the people who asked, and who reported the failure, are told (Notices); the request is kept until they have seen it
	var req Request
	if s.get("req:"+a.Name, &req) {
		req.Status, req.Answer, req.Seen = "approved", "It is installed.", nil
		s.put("req:"+a.Name, req)
	}
	var rep Report
	if s.get("rep:"+a.Name, &rep) && rep.Status == "open" {
		rep.Status, rep.Answer, rep.Seen = "fixed", "It is installed.", nil
		s.put("rep:"+a.Name, rep)
	}
	if has(s.Defaults(), a.Name) {
		s.refreshDefaults() // it was turned on for everyone while it was being installed
	}
	s.refreshHomes()
	s.mu.Unlock()
	s.finishJob(a.Name)
	log.Printf("appstore: %s %s installed (%.1f MB), asked by %s", a.Name, a.Version, float64(m.Size)/(1<<20), a.By)
}

// Wait waits for the installs that are going on (tests).
func (s *Service) Wait() { s.wg.Wait() }

// Job tells how far an install is.
func (s *Service) Job(name string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[name]; j != nil {
		return *j, true
	}
	return Job{}, false
}

// ---- requests, reports, decisions ----------------------------------------------------------------------------------------

func addUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// Request asks for an app that is not on the safe list. A second ask for the same app joins the first.
func (s *Service) Request(who, name, kind, reason string) (Request, error) {
	return s.RequestFor(who, name, kind, "", reason)
}

// RequestFor is Request with the REPL (or the place of a plugin) the app is wanted for. A user may make a limited number of asks a day.
func (s *Service) RequestFor(who, name, kind, repl, reason string) (Request, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if repl != "" && !inList(CatalogueRepls, repl) && !inList(CataloguePlaces, repl) {
		return Request{}, fmt.Errorf("%q is not a REPL of this site", repl)
	}
	if !ValidName(name) {
		return Request{}, fmt.Errorf("%q is not a package name", name)
	}
	if b, ok := s.BuiltinNamed(name); ok && cleanKind(kind) != KindNode && cleanKind(kind) != KindPlugin {
		return Request{}, fmt.Errorf("%w (%s %s)", ErrBuiltin, b.Name, b.Version)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.app(name); ok && a.State == StateInstalled {
		return Request{}, ErrInstalled
	}
	var r Request
	if !s.get("req:"+name, &r) {
		r = Request{Name: name, Kind: cleanKind(kind), At: s.now()}
	}
	if !has(r.Askers, who) {
		day := s.cfg.Now().UTC().Format("2006-01-02")
		var n int
		key := "reqday:" + who + ":" + day
		s.get(key, &n)
		if lim := s.cfg.PerDay(); lim > 0 && n >= lim {
			return Request{}, ErrDaily
		}
		s.put(key, n+1)
	}
	r.Status, r.Answer = "waiting", ""
	r.Askers = addUnique(r.Askers, who)
	if r.Reason == "" {
		r.Reason = clip(reason, 200)
	}
	if r.Repl == "" {
		r.Repl = repl
	}
	return r, s.put("req:"+name, r)
}

// RequestsToday is how many asks the user has made today (what the request dialog shows).
func (s *Service) RequestsToday(who string) int {
	var n int
	s.get("reqday:"+who+":"+s.cfg.Now().UTC().Format("2006-01-02"), &n)
	return n
}

// Reject turns a request down, with a reason the askers see.
func (s *Service) Reject(admin, name, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Request
	if !s.get("req:"+name, &r) {
		return ErrUnknown
	}
	r.Status, r.Answer = "rejected", clip(reason, 200)
	r.Seen = nil
	log.Printf("appstore: %s rejected the request for %s", admin, name)
	return s.put("req:"+name, r)
}

// Approve installs a requested app.
func (s *Service) Approve(admin, name string) error {
	s.mu.Lock()
	var r Request
	known := s.get("req:"+name, &r)
	s.mu.Unlock()
	kind := KindLib
	if known {
		kind = r.Kind
	}
	log.Printf("appstore: %s approved %s", admin, name)
	return s.Install(admin, name, kind, true)
}

// Report tells the admin about an install that failed. A second reporter joins the first.
func (s *Service) Report(who, name, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.app(name)
	if !ok || a.State != StateFailed {
		return ErrUnknown
	}
	var r Report
	if !s.get("rep:"+name, &r) || r.Status != "open" {
		r = Report{Name: name, Status: "open", At: s.now(), Log: a.Log, Note: clip(note, 300), Node: a.Node, Version: a.Version, Step: a.Err}
	}
	r.Reporters = addUnique(r.Reporters, who)
	if r.Note == "" {
		r.Note = clip(note, 300)
	}
	return s.put("rep:"+name, r)
}

// ResolveReport ends a report: "retry" installs again, "installed" records that the admin fixed it by hand, "reject" says it will not be.
func (s *Service) ResolveReport(admin, name, action, answer string) error {
	s.mu.Lock()
	var r Report
	known := s.get("rep:"+name, &r)
	s.mu.Unlock()
	if !known {
		return ErrUnknown
	}
	switch action {
	case "retry":
		return s.Install(admin, name, KindLib, true)
	case "installed":
		a, ok := s.app(name)
		if !ok || a.ID == "" || !s.cfg.Store.HasBundle(a.ID) {
			return fmt.Errorf("%s is not in the store on this server: install it first", name)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		a.State, a.Err, a.Log = StateInstalled, "", ""
		a.Size = DirSize(s.cfg.Store.BundlePath(a.ID))
		s.put("app:"+name, a)
		r.Status, r.Answer = "fixed", clip(answer, 200)
		return s.put("rep:"+name, r)
	case "reject":
		s.mu.Lock()
		defer s.mu.Unlock()
		r.Status, r.Answer = "rejected", clip(answer, 200)
		return s.put("rep:"+name, r)
	}
	return fmt.Errorf("%q is not an action", action)
}

// Remove takes an app out of the store: off every user's list, its bundle deleted. It says how many users had it on.
func (s *Service) Remove(admin, name string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.app(name)
	if !ok {
		return 0, ErrUnknown
	}
	if s.jobs[name] != nil {
		return 0, ErrBusy
	}
	users := 0
	lists := map[string][]string{}
	s.each("user:", func(key string, raw []byte) {
		var ids []string
		if json.Unmarshal(raw, &ids) == nil {
			lists[strings.TrimPrefix(key, "user:")] = ids
		}
	})
	for home, ids := range lists {
		var keep []string
		for _, id := range ids {
			if id != a.ID {
				keep = append(keep, id)
			}
		}
		if len(keep) != len(ids) {
			users++
			s.setList(home, keep)
			s.cfg.Store.WriteManifest(home, keep)
		}
	}
	s.del("app:" + name)
	if a.ID != "" {
		s.del("lock:" + a.ID)
	}
	if has(s.Defaults(), name) {
		var next []string
		for _, n := range s.Defaults() {
			if n != name {
				next = append(next, n)
			}
		}
		if next == nil {
			next = []string{}
		}
		s.put("defaults", next)
	}
	if a.ID != "" {
		s.cfg.Store.RemoveBundle(a.ID)
		s.refreshDefaults()
	}
	log.Printf("appstore: %s removed %s (it was on for %d users)", admin, name, users)
	return users, nil
}

// ---- the user's switch ---------------------------------------------------------------------------------------------------

// Switch turns an installed app on or off for one user (home is the name of the user's workspace folder). It changes the user's
// list and the files a terminal reads at its start; nothing else.
func (s *Service) Switch(home, id string, on bool) error {
	if !ValidHome(home) || !ValidBundle(id) {
		return ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	known := false
	for _, a := range s.Apps() {
		if a.ID == id && a.State == StateInstalled {
			known = true
		}
	}
	if !known {
		return ErrUnknown
	}
	if a, ok := s.appByID(id); ok && Kept(home) && has(s.Defaults(), a.Name) {
		// on for everyone: the user's switch is the off list (and the own list drops it, so the two never disagree)
		own, off := s.ownList(home), s.offList(home)
		var nextOwn, nextOff []string
		for _, x := range own {
			if x != id {
				nextOwn = append(nextOwn, x)
			}
		}
		for _, x := range off {
			if x != id {
				nextOff = append(nextOff, x)
			}
		}
		if !on {
			nextOff = append(nextOff, id)
		}
		if err := s.cfg.Store.WriteHome(home, nextOwn, nextOff); err != nil {
			return err
		}
		if on {
			a.LastUsed = s.now()
			s.put("app:"+a.Name, a)
		}
		if err := s.setOff(home, nextOff); err != nil {
			return err
		}
		return s.setList(home, nextOwn)
	}
	cur := s.ownList(home)
	var next []string
	present := false
	for _, x := range cur {
		if x == id {
			present = true
			if !on {
				continue
			}
		}
		next = append(next, x)
	}
	if on && !present {
		if len(cur) >= MaxOn {
			return ErrTooMany
		}
		next = append(next, id)
	}
	if err := s.cfg.Store.WriteManifest(home, next); err != nil {
		return err
	}
	if on {
		for _, a := range s.Apps() {
			if a.ID == id {
				a.LastUsed = s.now()
				s.put("app:"+a.Name, a)
			}
		}
	}
	return s.setList(home, next)
}

// AppByID finds an installed app by its bundle name.
func (s *Service) AppByID(id string) (App, bool) { return s.appByID(id) }

// NoteCheckFailure puts a failed check of a switch-on in the admin's failure list, with the node and the raw log, before anyone reports it:
// the user is told a short plain message and nothing else (LLD 18). One entry for each app; each failure is counted.
func (s *Service) NoteCheckFailure(name, node, log string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.app(name)
	if !ok || a.State != StateInstalled {
		return ErrUnknown
	}
	var r Report
	if !s.get("rep:"+name, &r) || r.Status != "open" {
		r = Report{Name: name, Status: "open", At: s.now(), Version: a.Version, Auto: true}
	}
	r.Fails++
	r.Node, r.Step, r.Log = node, "Turning it on", clip(log, 600)
	return s.put("rep:"+name, r)
}

// ReportUse is a user's report that an installed app did not work for them when they switched it on (what the check said is the log):
// it goes to the admin with the failed installs, once for each app, with each reporter added to it.
func (s *Service) ReportUse(who, name, node, step, log, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.app(name)
	if !ok || a.State != StateInstalled {
		return ErrUnknown
	}
	var r Report
	if !s.get("rep:"+name, &r) || r.Status != "open" {
		r = Report{Name: name, Status: "open", At: s.now(), Log: clip(log, 600), Note: clip(note, 300), Node: node, Version: a.Version, Step: step}
	}
	r.Auto = false // a person has reported it now
	r.Reporters = addUnique(r.Reporters, who)
	if r.Note == "" {
		r.Note = clip(note, 300)
	}
	return s.put("rep:"+name, r)
}

// appByID finds an installed app by its bundle name.
func (s *Service) appByID(id string) (App, bool) {
	for _, a := range s.Apps() {
		if a.ID == id && a.State == StateInstalled {
			return a, true
		}
	}
	return App{}, false
}

// ---- what the page shows -------------------------------------------------------------------------------------------------

// View is one app as a user sees it.
type View struct {
	Default  bool       `json:"default,omitempty"`  // an admin turned it on for everyone (a user may turn it off for themselves)
	OthersOn int        `json:"othersOn,omitempty"` // how many other users have it on: a user may uninstall it only when this is 0
	Summary  string     `json:"summary,omitempty"`  // for the samples, which are not looked up
	At       int64      `json:"at,omitempty"`       // when it was installed or asked for
	Steps    []StepView `json:"steps,omitempty"`    // while installing: the list with what is done
	Pct      int        `json:"pct,omitempty"`      // how far the install is
	Detail   string     `json:"detail,omitempty"`   // what the solve found ("14 packages, 41 MB")
	Partial  bool       `json:"partial,omitempty"`  // the version may differ by the CPU type of a server
	Waiting  bool       `json:"waiting,omitempty"`  // it is only waiting for another install to end (this server installs one at a time)
	Packages int        `json:"packages,omitempty"` // what an installed app brought
	Node     string     `json:"node,omitempty"`     // where a failed install ran
	Name     string     `json:"name"`
	Version  string     `json:"version,omitempty"`
	ID       string     `json:"id,omitempty"`
	Kind     string     `json:"kind"`
	State    string     `json:"state"` // available, installing, installed, failed, requested, rejected
	Safe     bool       `json:"safe"`
	On       bool       `json:"on"`
	SizeMB   float64    `json:"sizeMB,omitempty"`
	Step     string     `json:"step,omitempty"`
	Err      string     `json:"err,omitempty"`
	Log      string     `json:"log,omitempty"`
	Reported bool       `json:"reported,omitempty"`
	Answer   string     `json:"answer,omitempty"`
	Mine     bool       `json:"mine,omitempty"` // I asked, or I installed it, or I reported it
	// from the catalogue
	Entry    string   `json:"entry,omitempty"`
	Docs     string   `json:"docs,omitempty"`
	Repls    []string `json:"repls,omitempty"`
	Category string   `json:"category,omitempty"`
	Level    string   `json:"level,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	RAMMB    int      `json:"ramMB,omitempty"`
	InstMB   int      `json:"instMB,omitempty"`
	Licence  string   `json:"licence,omitempty"`
	Manager  string   `json:"manager,omitempty"`
	Place    string   `json:"place,omitempty"`
	Source   string   `json:"source,omitempty"` // the package at its manager: a module path, group:artifact, a crate
	Use      string   `json:"use,omitempty"`    // the first line of how to use it
	Mod      string   `json:"mod,omitempty"`    // the name a program imports it by (to find what an open file needs)
	// the measured install (minimal.go): the manager that is the smallest, what it brings that a user may not expect, and (the options with their sizes per CPU type are the admin's only: a user is never told what CPUs the servers have)
	Smallest string `json:"smallest,omitempty"`
	Includes string `json:"includes,omitempty"`
	Estimate bool   `json:"estimate,omitempty"` // the size is a dry-run estimate, not a measurement
}

func (v *View) fromEntry(e Entry) {
	v.Entry, v.Docs, v.Repls, v.Category, v.Level, v.Labels = e.ID, e.Docs, e.Repls, e.Category, e.Level, e.Labels
	v.RAMMB, v.InstMB, v.Licence, v.Manager, v.Place = e.RAMMB, e.InstMB, e.Licence, e.Manager, e.Place
	v.Source, v.Use, v.Mod = e.Source(), e.UseLine(), e.Module()
	if e.Minimal != nil {
		v.Smallest, v.Estimate = e.Minimal.Manager, e.Minimal.Estimate
		if o, ok := e.OptionOf(e.Minimal.Manager); ok {
			v.Includes = o.Includes
		}
	}
	if v.Summary == "" {
		v.Summary = e.Summary
	}
}

// StepView is one line of the list of an install.
type StepView struct {
	Text  string `json:"text"`
	State string `json:"state"` // done, current, todo
}

func stepViews(j Job) ([]StepView, int) {
	out := make([]StepView, len(installSteps))
	for i, t := range installSteps {
		st := "todo"
		if i < j.Index {
			st = "done"
		} else if i == j.Index {
			st = "current"
		}
		out[i] = StepView{Text: t, State: st}
	}
	// the first step is only a look at the safe list: it is done as soon as the job has a turn
	if j.Index >= 0 {
		out[0].State = "done"
	}
	pct := 5
	if j.Index >= 0 {
		pct = 10 + j.Index*25
	}
	return out, pct
}

// Views lists the apps for one user: what is installed or failing, what was asked for, and the safe list.
func (s *Service) Views(who, home string) []View { return s.views(who, home, false) }

// ViewsFull is Views for an admin: the error, the raw log and the server a failed install ran on are kept. A user never gets them
// (LLD 18): Views gives a short plain reason instead, and counts of other users are reduced to "some".
func (s *Service) ViewsFull(who, home string) []View { return s.views(who, home, true) }

// plainReason says why an install failed in a few words a user can read: no paths, no servers, no log lines.
func plainReason(a App) string {
	t := strings.ToLower(a.Err + " " + a.Log)
	switch {
	case strings.Contains(t, "solve") || strings.Contains(t, "conflict") || strings.Contains(t, "no candidates") || strings.Contains(t, "no build"):
		return "It could not be solved for this server's Python."
	case strings.Contains(t, "checking that it works") || strings.Contains(t, "did not start") || strings.Contains(t, "could not be loaded"):
		return "The check after the install failed."
	case strings.Contains(t, "no space") || strings.Contains(t, "disk") || strings.Contains(t, "limit") || strings.Contains(t, "full"):
		return "There was no room for it."
	}
	return "The install stopped."
}

func (s *Service) views(who, home string, full bool) []View {
	my := map[string]bool{}
	for _, id := range s.MyList(home) {
		my[id] = true
	}
	// how many other users have each bundle on (a user may uninstall an app only when nobody else has it on)
	others := map[string]int{}
	if home != "" {
		s.each("user:", func(key string, raw []byte) {
			if strings.TrimPrefix(key, "user:") == home {
				return
			}
			var ids []string
			if json.Unmarshal(raw, &ids) == nil {
				for _, id := range ids {
					others[id]++
				}
			}
		})
	}
	def := map[string]bool{}
	for _, n := range s.Defaults() {
		def[n] = true
	}
	seen := map[string]bool{}
	out := []View{}
	for _, a := range s.Apps() {
		if def[a.Name] && others[a.ID] == 0 {
			others[a.ID] = 1 // on for everyone: others have it on, so a user cannot uninstall it
		}
		v := View{Default: def[a.Name], At: a.At, Name: a.Name, Version: a.Version, ID: a.ID, Kind: a.Kind, State: a.State, Safe: s.isSafe(cleanKind(a.Kind), a.Name),
			On: my[a.ID], SizeMB: float64(a.Size) / (1 << 20), Mine: a.By == who, OthersOn: others[a.ID]}
		v.Packages, v.Partial = a.Packages, a.Partial
		if !full && v.OthersOn > 1 {
			v.OthersOn = 1
		}
		if a.State == StateInstalling {
			if j, ok := s.Job(a.Name); ok {
				v.Step = j.Step
				v.Waiting = j.Index < 0
				v.Steps, v.Pct = stepViews(j)
				v.Detail = j.Detail
			} else {
				v.Step = "Starting"
			}
		}
		if a.State == StateFailed {
			if full {
				v.Err, v.Log, v.Node = a.Err, a.Log, a.Node
			} else {
				v.Err = plainReason(a)
			}
			var r Report
			if s.get("rep:"+a.Name, &r) && r.Status == "open" {
				v.Reported = true
				for _, w := range r.Reporters {
					if w == who {
						v.Mine = true
					}
				}
			}
		}
		if e, ok := s.EntryFor(a.Name, cleanKind(a.Kind)); ok {
			if e.asset() && a.State == StateInstalled && !full {
				continue // a browser library or a plugin from npm: the preview serves it, a terminal does not switch it on
			}
			v.fromEntry(e)
		}
		seen[a.Name] = true
		out = append(out, v)
	}
	for _, e := range s.Catalogue() {
		k := e.InstallKind()
		n := e.PackageName()
		if k == "" || k == KindPlugin || e.asset() || seen[n] || !ValidName(n) {
			continue
		}
		if _, isBuiltin := s.BuiltinNamed(n); isBuiltin && k != KindNode {
			continue
		}
		seen[n] = true
		v := View{Name: n, Kind: k, State: "available", Safe: e.Install == "anyone"}
		v.fromEntry(e)
		out = append(out, v)
	}
	for _, r := range s.Requests() {
		if seen[r.Name] {
			continue
		}
		v := View{At: r.At, Name: r.Name, Kind: r.Kind, State: "requested", Answer: r.Answer}
		if r.Status == "rejected" {
			v.State = "rejected"
		}
		for _, a := range r.Askers {
			if a == who {
				v.Mine = true
			}
		}
		seen[r.Name] = true
		out = append(out, v)
	}
	for _, e := range s.cfg.Safe() {
		k, n := safeEntry(e)
		if n == "" || seen[n] || !ValidName(n) {
			continue
		}
		seen[n] = true
		out = append(out, View{Name: n, Kind: k, State: "available", Safe: true})
	}
	// the samples of the kinds an admin installs: languages and the plugins that come with the server
	for _, l := range SampleLanguages {
		if !seen[l.Name] {
			seen[l.Name] = true
			out = append(out, View{Name: l.Name, Kind: KindLang, State: "available", Summary: l.Summary})
		}
	}
	for _, p := range Plugins {
		if !seen[p.Name] {
			seen[p.Name] = true
			out = append(out, View{Name: p.Name, Kind: KindPlugin, State: "available", Version: p.Version, Summary: p.Summary})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---- replay: a node makes its store match the catalog --------------------------------------------------------------------

// Snapshot is the catalog as a node needs it: the apps that are installed and the users' lists.
type Snapshot struct {
	Apps  []App               `json:"apps"`
	Users map[string][]string `json:"users"`
	Apt   []string            `json:"apt,omitempty"`
	// Defaults are the bundles that are on for every signed-in user; Off is, for a user, the ones of them the user turned off.
	Defaults []string            `json:"defaults,omitempty"`
	Off      map[string][]string `json:"off,omitempty"`
	// Catalogue are the entries an admin added or changed, so that a worker chooses the same checks and constraints as the gateway.
	Catalogue []Entry `json:"catalogue,omitempty"`
	Full      bool    `json:"full"` // the list is complete: a bundle that is not in it may be removed
	// Reinstall are bundles an admin asked this node to make again (the gateway adds them when it answers a worker): the node
	// deletes its copy of each, and the replay that follows fetches it again.
	Reinstall []string `json:"reinstall,omitempty"`
}

// Snapshot takes the catalog of this server.
func (s *Service) Snapshot() Snapshot {
	snap := Snapshot{Users: map[string][]string{}, Full: true}
	// a listing that fails (the database is not reachable) must not look like an empty catalog: nothing would be removed
	if err := s.each("app:", func(_ string, raw []byte) {
		var a App
		if json.Unmarshal(raw, &a) == nil && a.State == StateInstalled {
			snap.Apps = append(snap.Apps, a)
		}
	}); err != nil {
		snap.Full = false
	}
	sort.Slice(snap.Apps, func(i, j int) bool { return snap.Apps[i].Name < snap.Apps[j].Name })
	if err := s.each("user:", func(key string, raw []byte) {
		var ids []string
		if json.Unmarshal(raw, &ids) == nil {
			snap.Users[strings.TrimPrefix(key, "user:")] = ids
		}
	}); err != nil {
		snap.Full = false
	}
	snap.Defaults = s.defaultIDs()
	snap.Off = map[string][]string{}
	if err := s.each("off:", func(key string, raw []byte) {
		var ids []string
		if json.Unmarshal(raw, &ids) == nil && len(ids) > 0 {
			snap.Off[strings.TrimPrefix(key, "off:")] = ids
		}
	}); err != nil {
		snap.Full = false
	}
	var apt []string
	s.get("apt", &apt)
	snap.Apt = apt
	s.each("cat:", func(_ string, raw []byte) {
		var e Entry
		if json.Unmarshal(raw, &e) == nil && e.ID != "" {
			snap.Catalogue = append(snap.Catalogue, e)
		}
	})
	sort.Slice(snap.Catalogue, func(i, j int) bool { return snap.Catalogue[i].ID < snap.Catalogue[j].ID })
	return snap
}

// NodeState is what one node has, for the admin page.
type NodeState struct {
	Have   []string          `json:"have"`
	Failed map[string]string `json:"failed,omitempty"`
	SizeMB float64           `json:"sizeMB"`
	At     int64             `json:"at"`
	// Sizes is the size of each bundle this node has, in MB (from its marker), for the admin's table of a server.
	Sizes map[string]float64 `json:"sizes,omitempty"`
	// Retry is when (unix seconds) the node tries a failed bundle again; it does not try before that, however often it is asked.
	Retry map[string]int64 `json:"retry,omitempty"`
	// GaveUp are the bundles this node does not try again by itself (not available for its CPU type, or tried enough): an admin's Reinstall tries again.
	GaveUp map[string]bool `json:"gaveUp,omitempty"`
	// Versions are the versions this node really installed where they are not the gateway's (its best for its own CPU type).
	Versions map[string]string `json:"versions,omitempty"`
}

// SetOnStep sets what is told after each bundle of an Apply ended (see Config.OnStep).
func (s *Service) SetOnStep(f func(NodeState)) { s.cfg.OnStep = f }

// SetLock sets how a node gets the lock of a bundle it does not have the catalog for (a worker asks the gateway; see Config.Lock).
func (s *Service) SetLock(f func(ctx context.Context, id string) (Lock, error)) { s.cfg.Lock = f }

// NextRetry is the time of the earliest retry of a failed install on this node; false when nothing is waiting for one.
func (s *Service) NextRetry() (time.Time, bool) {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	var first time.Time
	for _, r := range s.retry {
		if first.IsZero() || r.next.Before(first) {
			first = r.next
		}
	}
	return first, !first.IsZero()
}

// Apply makes this node's store match a snapshot: the bundles that are missing are installed (the same pinned versions), the
// users' files are written, and, where the snapshot is complete, bundles that are no longer in it are removed. It is what a
// node does at its start, when it joins and when the catalog changes (a worker gets the snapshot from the gateway).
func (s *Service) Apply(ctx context.Context, snap Snapshot) NodeState {
	st := NodeState{Failed: map[string]string{}, At: s.now()}
	s.remoteMu.Lock()
	s.remote = snap.Catalogue // a worker has no catalog database: it takes the gateway's entries from here
	s.remoteMu.Unlock()
	for _, id := range snap.Reinstall {
		if ValidBundle(id) {
			s.cfg.Store.RemoveBundle(id) // the loop below makes it again, at once: a command from the admin ends the waiting
			s.retryMu.Lock()
			delete(s.retry, id)
			s.retryMu.Unlock()
		}
	}
	if err := s.cfg.Store.Ready(); err != nil {
		st.Failed["store"] = err.Error()
		return st
	}
	want := map[string]bool{}
	for _, a := range snap.Apps {
		if !ValidBundle(a.ID) || !ValidName(a.Name) || !ValidVersion(a.Version) {
			continue
		}
		want[a.ID] = true
		if s.cfg.Store.HasBundle(a.ID) {
			continue
		}
		s.retryMu.Lock()
		r, waiting := s.retry[a.ID]
		s.retryMu.Unlock()
		if waiting && r.gaveUp {
			// not tried again by itself (not available here, or tried enough): an admin's Reinstall makes it again
			st.Failed[a.ID] = r.err
			if st.GaveUp == nil {
				st.GaveUp = map[string]bool{}
			}
			st.GaveUp[a.ID] = true
			continue
		}
		if waiting && s.cfg.Now().Before(r.next) {
			// it failed lately: not before its time, however often the catalog is applied
			st.Failed[a.ID] = r.err
			if st.Retry == nil {
				st.Retry = map[string]int64{}
			}
			st.Retry[a.ID] = r.next.Unix()
			continue
		}
		s.inst.Lock()
		dest := s.cfg.Store.BundlePath(a.ID)
		os.RemoveAll(dest)
		var logb workLog
		ictx0, icancel0 := context.WithTimeout(ctx, 30*time.Minute)
		ictx, icancel := context.WithCancelCause(ictx0)
		stopWatch := watch(icancel, &logb, watchDirs(s.cfg.Store.Dir)...)
		// a node always installs a bundle itself (conda-forge / npm over its own internet): a bundle holds programs for one CPU and the
		// gateway's egress is not given away. It installs exactly what the lock of the bundle says, or nothing: a bundle that is not
		// the same everywhere is worse than one that is late. A bundle that is partial (the gateway could not solve it for every CPU type) has
		// no lock to follow: this node solves for its own CPU type and installs the best version that exists for it.
		var err error
		if a.Locked && !a.Partial {
			var l Lock
			if l, err = s.lockFor(ctx, a.ID); err == nil {
				err = WriteLock(dest, l)
			} else {
				err = fmt.Errorf("the lock of %s could not be had: %v", a.ID, err)
			}
		}
		runCtx := ictx
		if a.Partial {
			runCtx = WithSingleCPU(ictx)
		}
		if err == nil {
			// the node makes the bundle with the manager the gateway used (an apt bundle is not solved by pixi), trims it the same way and proves
			// that what it installed works before it says it has it
			_, _, err = s.installBundle(runCtx, a, a.Manager, dest, &logb)
			if c := context.Cause(ictx); err != nil && errors.Is(c, errNoProgress) {
				err = c
			}
		}
		stopWatch()
		icancel(nil)
		icancel0()
		var actual string
		if err == nil {
			// the version it really holds (a partial bundle may hold another than the gateway's)
			actual = a.Version
			if a.Partial || a.Manager == "" || a.Manager == MgrPixi {
				if v := installedVersion(dest, a.Name); v != "" {
					actual = v
				}
			}
			_, err = s.cfg.Store.Finish(a.ID, a.Name, actual)
		}
		if err != nil {
			os.RemoveAll(dest)
			msg := tail(err.Error()+" "+logb.String(), 300)
			perm := IsPermanent(err)
			s.retryMu.Lock()
			fails := s.retry[a.ID].fails + 1
			e := retryEntry{fails: fails, err: msg}
			switch {
			case perm:
				// not available for this CPU type (or this archive): trying again cannot help, an admin's Reinstall tries once more
				e.gaveUp, e.err = true, "Not available on this server: "+msg
			case fails > len(Backoff):
				e.gaveUp, e.err = true, fmt.Sprintf("Gave up after %d tries: %s", fails, msg)
			default:
				e.next = s.cfg.Now().Add(Backoff[fails-1])
			}
			s.retry[a.ID] = e
			s.retryMu.Unlock()
			st.Failed[a.ID] = e.err
			if e.gaveUp {
				if st.GaveUp == nil {
					st.GaveUp = map[string]bool{}
				}
				st.GaveUp[a.ID] = true
			} else {
				if st.Retry == nil {
					st.Retry = map[string]int64{}
				}
				st.Retry[a.ID] = e.next.Unix()
			}
			log.Printf("appstore: replay: %s could not be installed here (try %d, permanent=%v): %v", a.ID, fails, perm, err)
		} else {
			if actual != "" && actual != a.Version {
				if st.Versions == nil {
					st.Versions = map[string]string{}
				}
				st.Versions[a.ID] = actual
			}
			s.retryMu.Lock()
			delete(s.retry, a.ID)
			s.retryMu.Unlock()
		}
		s.inst.Unlock()
		if s.cfg.OnStep != nil {
			s.cfg.OnStep(s.stateNow(st))
		}
	}
	s.retryMu.Lock()
	for id := range s.retry {
		if !want[id] {
			delete(s.retry, id) // no longer in the catalog: nothing to try again
		}
	}
	s.retryMu.Unlock()
	if snap.Full {
		for _, id := range s.cfg.Store.Bundles() {
			if !want[id] {
				s.cfg.Store.RemoveBundle(id)
			}
		}
	}
	// what is on for everyone (before the users: their env.sh includes it)
	if err := s.cfg.Store.WriteDefaults(snap.Defaults); err != nil {
		st.Failed["defaults"] = err.Error()
	}
	// the users: the bundles that are here; and an empty list for a user that has none any more
	homes := map[string]bool{}
	write := func(home string) {
		homes[home] = true
		var have []string
		for _, id := range snap.Users[home] {
			if s.cfg.Store.HasBundle(id) {
				have = append(have, id)
			}
		}
		if ValidHome(home) {
			if err := s.cfg.Store.WriteHome(home, have, snap.Off[home]); err != nil {
				st.Failed["user:"+home] = err.Error()
			}
		}
	}
	for home := range snap.Users {
		write(home)
	}
	for home := range snap.Off {
		if !homes[home] {
			write(home)
		}
	}
	for _, home := range s.cfg.Store.Homes() {
		if !homes[home] {
			s.cfg.Store.WriteHome(home, nil, nil)
		}
	}
	s.applyApt(ctx, snap.Apt, &st)
	return s.stateNow(st)
}

// stateNow is st with what the node has now: the bundles and their sizes (a copy: the maps of st go on changing).
func (s *Service) stateNow(st NodeState) NodeState {
	out := st
	out.Failed = map[string]string{}
	for k, v := range st.Failed {
		out.Failed[k] = v
	}
	if st.Retry != nil {
		out.Retry = map[string]int64{}
		for k, v := range st.Retry {
			out.Retry[k] = v
		}
	}
	out.Have = s.cfg.Store.Bundles()
	out.Sizes = map[string]float64{}
	for _, id := range out.Have {
		out.Sizes[id] = float64(s.cfg.Store.BundleSize(id)) / (1 << 20)
	}
	out.SizeMB = float64(s.cfg.Store.Size()) / (1 << 20)
	return out
}

// Recover is run at start: an install that was going on when the server stopped did not finish.
func (s *Service) Recover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.Apps() {
		if a.State == StateInstalling && s.jobs[a.Name] == nil {
			a.State, a.Err = StateFailed, "The server stopped while it was being installed. Try again."
			s.put("app:"+a.Name, a)
		}
	}
}

// Apt is the list of Debian packages an admin approved for every user.
func (s *Service) Apt() []string {
	l := []string{}
	s.get("apt", &l)
	if l == nil {
		l = []string{}
	}
	return l
}

// SetApt saves the list of Debian packages.
func (s *Service) SetApt(list []string) error {
	var clean []string
	for _, p := range list {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !ValidApt(p) {
			return fmt.Errorf("%q is not a package name", p)
		}
		clean = addUnique(clean, p)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.put("apt", clean)
}

// ---- the answers users are told ------------------------------------------------------------------------------------------

// Notice is an answer a user has not seen yet: a request that was approved (the app is installed now) or rejected, a report that
// was fixed or turned down.
type Notice struct {
	Kind   string `json:"kind"` // request, report
	Name   string `json:"name"`
	Status string `json:"status"` // approved, rejected, fixed
	Text   string `json:"text"`
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Notices lists what the admin decided about this user's requests and reports, until the user has seen it (Ack).
func (s *Service) Notices(who string) []Notice {
	var out []Notice
	for _, r := range s.Requests() {
		if !has(r.Askers, who) || has(r.Seen, who) {
			continue
		}
		switch r.Status {
		case "approved":
			out = append(out, Notice{"request", r.Name, "approved", r.Name + " is installed. Turn it on in the App Store to use it."})
		case "rejected":
			t := r.Name + " was not approved."
			if r.Answer != "" {
				t += " " + r.Answer
			}
			out = append(out, Notice{"request", r.Name, "rejected", t})
		}
	}
	for _, r := range s.Reports() {
		if !has(r.Reporters, who) || has(r.Seen, who) {
			continue
		}
		switch r.Status {
		case "fixed":
			out = append(out, Notice{"report", r.Name, "fixed", r.Name + " is installed now. Turn it on in the App Store to use it."})
		case "rejected":
			t := "The admin will not install " + r.Name + "."
			if r.Answer != "" {
				t += " " + r.Answer
			}
			out = append(out, Notice{"report", r.Name, "rejected", t})
		}
	}
	return out
}

// Ack says that the user has seen the answer about an app. An approved request is forgotten when every asker has seen it.
func (s *Service) Ack(who, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Request
	if s.get("req:"+name, &r) && has(r.Askers, who) && !has(r.Seen, who) && (r.Status == "approved" || r.Status == "rejected") {
		r.Seen = append(r.Seen, who)
		if r.Status == "approved" && len(r.Seen) >= len(r.Askers) {
			s.del("req:" + name)
		} else {
			s.put("req:"+name, r)
		}
	}
	var rep Report
	if s.get("rep:"+name, &rep) && has(rep.Reporters, who) && !has(rep.Seen, who) && rep.Status != "open" {
		rep.Seen = append(rep.Seen, who)
		s.put("rep:"+name, rep)
	}
}

// ---- what an install would cost, search, and the progress of a fetch ----------------------------------------------------------

type estimate struct {
	r   Resolved
	at  time.Time
	err string
}

var (
	estMu    sync.Mutex
	estCache = map[string]estimate{}
)

// Estimate says what an install of an app would bring: its version, the packages and about how many MB (a solve without installing; the
// answer is kept for an hour).
func (s *Service) Estimate(ctx context.Context, name, kind string) (Resolved, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	kind = cleanKind(kind)
	if !ValidName(name) {
		return Resolved{}, fmt.Errorf("%q is not a package name", name)
	}
	key := kind + ":" + name
	estMu.Lock()
	if e, ok := estCache[key]; ok && time.Since(e.at) < time.Hour {
		estMu.Unlock()
		if e.err != "" {
			return Resolved{}, errors.New(e.err)
		}
		return e.r, nil
	}
	estMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	r, err := s.cfg.Installer.Resolve(ctx, name, "", kind)
	e := estimate{r: r, at: time.Now()}
	if err != nil {
		e.err = err.Error()
	}
	estMu.Lock()
	estCache[key] = e
	estMu.Unlock()
	return r, err
}

// Hit is one answer of a search, with what the store knows of the app.
type Hit struct {
	Found
	State string `json:"state"` // available, installing, installed, failed, requested, rejected, builtin
	Safe  bool   `json:"safe"`
}

// Search looks for apps by a few letters of the name, in the catalogs the installer has (conda-forge, npm, the plugins), and says for
// each what the store knows of it. An app the store has already is first.
func (s *Service) Search(ctx context.Context, q, kind string) ([]Hit, error) {
	found, err := s.cfg.Installer.Search(ctx, q, kind)
	known := map[string]View{}
	for _, v := range s.Views("", "") {
		known[v.Name] = v
	}
	var hits []Hit
	seen := map[string]bool{}
	add := func(f Found) {
		if seen[f.Name+f.Kind] {
			return
		}
		seen[f.Name+f.Kind] = true
		h := Hit{Found: f, State: "available", Safe: s.isSafe(cleanKind(f.Kind), f.Name)}
		if b, ok := s.BuiltinNamed(f.Name); ok && cleanKind(f.Kind) != KindNode && cleanKind(f.Kind) != KindPlugin {
			h.State, h.Safe, h.Version = "builtin", false, b.Version
		}
		if v, ok := known[f.Name]; ok && (v.Kind == f.Kind || v.Kind == "") {
			h.State = v.State
			if v.Summary != "" && h.Summary == "" {
				h.Summary = v.Summary
			}
		}
		hits = append(hits, h)
	}
	for _, f := range found {
		add(f)
	}
	return hits, err
}

// Uninstall removes an installed app on a user's own word: it is allowed only when no other user has it on (the user's own switch does
// not count: it goes with the app). An admin removes an app anyway (Remove). It says how many other users had it on when it refuses.
func (s *Service) Uninstall(who, home, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	a, ok := s.app(name)
	if !ok || a.State != StateInstalled {
		return ErrUnknown
	}
	others := 0
	s.each("user:", func(key string, raw []byte) {
		if strings.TrimPrefix(key, "user:") == home {
			return
		}
		var ids []string
		if json.Unmarshal(raw, &ids) == nil && has(ids, a.ID) {
			others++
		}
	})
	if has(s.Defaults(), name) {
		return fmt.Errorf("%w: it is on for everyone", ErrInUse)
	}
	if others > 0 {
		return fmt.Errorf("%w: other users have it on", ErrInUse)
	}
	_, err := s.Remove(who, name)
	return err
}

// ---- the lock of a bundle ---------------------------------------------------------------------------------------------------

// Lock is what pins a bundle: the manifest and the lock of the install that made it (pixi.toml and pixi.lock; the lock holds the exact
// packages for every CPU type the manifest names). It is saved in the catalog, so a node of any CPU type installs the same packages,
// and a gateway whose disk was wiped installs them again, not whatever is newest.
type Lock struct {
	Manifest string `json:"manifest"`
	Lock     string `json:"lock"`
	Manager  string `json:"manager,omitempty"` // "apt": the files are apt.json and apt.lock (the packages and versions of a rootless apt bundle)
}

// lockFiles are the two files of a bundle that make its Lock.
const (
	manifestFile = "pixi.toml"
	lockFile     = "pixi.lock"
)

// ReadLock reads the lock files of a bundle folder; false when it has none (an app made before the move to pixi, a Node library, a plugin).
func ReadLock(bundle string) (Lock, bool) {
	if IsAptBundle(bundle) {
		m, err1 := os.ReadFile(filepath.Join(bundle, aptManifestFile))
		l, err2 := os.ReadFile(filepath.Join(bundle, aptLockFile))
		if err1 != nil || err2 != nil || len(l) == 0 || len(m) > 256<<10 || len(l) > 4<<20 {
			return Lock{}, false
		}
		return Lock{Manifest: string(m), Lock: string(l), Manager: MgrApt}, true
	}
	m, err1 := os.ReadFile(filepath.Join(bundle, manifestFile))
	l, err2 := os.ReadFile(filepath.Join(bundle, lockFile))
	if err1 != nil || err2 != nil || len(l) == 0 || len(m) > 256<<10 || len(l) > 4<<20 {
		return Lock{}, false
	}
	return Lock{Manifest: string(m), Lock: string(l)}, true
}

// WriteLock puts the lock files in a bundle folder (made if it is not there).
func WriteLock(bundle string, l Lock) error {
	if err := os.MkdirAll(bundle, 0755); err != nil {
		return err
	}
	if l.Manager == MgrApt {
		if err := os.WriteFile(filepath.Join(bundle, aptManifestFile), []byte(l.Manifest), 0644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(bundle, aptLockFile), []byte(l.Lock), 0644)
	}
	if err := os.WriteFile(filepath.Join(bundle, manifestFile), []byte(l.Manifest), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(bundle, lockFile), []byte(l.Lock), 0644)
}

// captureLock saves the lock of a bundle that was just installed in the catalog; false when it has none.
func (s *Service) captureLock(id, dest string) bool {
	l, ok := ReadLock(dest)
	if !ok {
		return false
	}
	return s.put("lock:"+id, l) == nil
}

// LockOf is the saved lock of a bundle (what a worker asks the gateway for).
func (s *Service) LockOf(id string) (Lock, bool) {
	var l Lock
	if !ValidBundle(id) || !s.get("lock:"+id, &l) || l.Lock == "" {
		return Lock{}, false
	}
	return l, true
}

// lockFor finds the lock a node installs a bundle from: the gateway's own database, or the gateway through the hook of a worker.
func (s *Service) lockFor(ctx context.Context, id string) (Lock, error) {
	if s.cfg.Lock != nil {
		return s.cfg.Lock(ctx, id)
	}
	if l, ok := s.LockOf(id); ok {
		return l, nil
	}
	return Lock{}, errors.New("the saved lock of " + id + " is not in the catalog")
}
