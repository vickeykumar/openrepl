package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
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
	KindLib  = "lib"
	KindTool = "tool"
)

// Errors a caller turns into a message.
var (
	ErrNeedsApproval = errors.New("this app is not on the safe list: ask for it, and an admin decides")
	ErrBusy          = errors.New("this app is being installed already")
	ErrInstalled     = errors.New("this app is installed already")
	ErrFull          = errors.New("the store is full: an admin has to make room")
	ErrTooSoon       = errors.New("try again in a minute")
	ErrUnknown       = errors.New("no such app")
	ErrTooMany       = errors.New("too many apps are on: turn one off first")
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
	Download float64 `json:"download,omitempty"` // MB fetched for it
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
	Step      string   `json:"step,omitempty"` // what it stopped at
	Seen      []string `json:"seen,omitempty"` // reporters that were told the answer
}

// Config is what the service is made of.
type Config struct {
	Store     Store
	KV        KV
	Installer Installer
	Apt       AptRunner // installs the Debian packages an admin approved, nil for a node that does not
	// Fetch gets a finished bundle from the gateway (a worker; see tarball.go). When it is nil, or fails, the node installs the
	// bundle itself from conda-forge.
	Fetch   func(ctx context.Context, id, dest string) error
	Safe    func() []string // the names a user may install without asking
	LimitMB func() int      // the most the store may use, 0 for no limit
	Now     func() time.Time
}

// Service is the App Store of one server (the gateway or a standalone server holds the catalog; a worker only applies it).
type Service struct {
	cfg  Config
	mu   sync.Mutex // the records
	inst sync.Mutex // one install at a time
	jobs map[string]*Job
	wg   sync.WaitGroup
}

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
	cfg.KV = &lockedKV{inner: cfg.KV}
	return &Service{cfg: cfg, jobs: map[string]*Job{}}
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
	var out []App
	s.each("app:", func(_ string, raw []byte) {
		var a App
		if json.Unmarshal(raw, &a) == nil {
			out = append(out, a)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Service) app(name string) (App, bool) {
	var a App
	ok := s.get("app:"+name, &a)
	return a, ok
}

// Requests lists the requests (status waiting or rejected).
func (s *Service) Requests() []Request {
	var out []Request
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
	var out []Report
	s.each("rep:", func(_ string, raw []byte) {
		var r Report
		if json.Unmarshal(raw, &r) == nil {
			out = append(out, r)
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// MyList is the bundles that are on for a user.
func (s *Service) MyList(home string) []string {
	var ids []string
	s.get("user:"+home, &ids)
	return ids
}

func (s *Service) setList(home string, ids []string) error {
	if ids == nil {
		ids = []string{}
	}
	return s.put("user:"+home, ids)
}

// ---- who may install -----------------------------------------------------------------------------------------------------

// IsSafe says whether a user may install the app without asking.
func (s *Service) IsSafe(name string) bool {
	for _, n := range s.cfg.Safe() {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	return false
}

func cleanKind(k string) string {
	if k == KindTool {
		return KindTool
	}
	return KindLib
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

func (s *Service) install(who, name, kind string, admin bool, version string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidName(name) {
		return fmt.Errorf("%q is not a package name", name)
	}
	if !admin && !s.IsSafe(name) {
		return ErrNeedsApproval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a, ok := s.app(name); ok {
		switch a.State {
		case StateInstalled:
			return ErrInstalled
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
	if limit := s.cfg.LimitMB(); limit > 0 && s.cfg.Store.Size() > int64(limit)<<20 {
		return ErrFull
	}
	a := App{Name: name, Kind: cleanKind(kind), State: StateInstalling, By: who, At: s.now(), Retried: s.now(), Want: version}
	if old, ok := s.app(name); ok && old.Kind != "" {
		a.Kind = old.Kind
	}
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
	s.put("app:"+a.Name, a)
	s.mu.Unlock()
	s.finishJob(a.Name)
}

func (s *Service) runInstall(a App) {
	s.inst.Lock() // one at a time
	defer s.inst.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var logb bytes.Buffer
	if err := s.cfg.Store.Ready(); err != nil {
		s.fail(a, "Preparing the store", err, "")
		return
	}
	s.step(a.Name, 1, "")
	res, err := s.cfg.Installer.Resolve(ctx, a.Name, a.Want)
	if err != nil {
		s.fail(a, "Resolving the dependencies", err, err.Error())
		return
	}
	a.Version, a.ID, a.Packages, a.Download = res.Version, BundleID(a.Name, res.Version), res.Packages, res.DownloadMB
	dest := s.cfg.Store.BundlePath(a.ID)
	if dest == "" {
		s.fail(a, "Resolving the dependencies", fmt.Errorf("%s is not a usable version", res.Version), "")
		return
	}
	detail := fmt.Sprintf("%d packages", res.Packages)
	if res.DownloadMB > 0 {
		detail += fmt.Sprintf(", %.0f MB", res.DownloadMB)
	}
	os.RemoveAll(dest) // a half-made bundle of an earlier try
	s.step(a.Name, 2, detail)
	if err := s.cfg.Installer.Install(ctx, a.Name, res.Version, dest, &logb); err != nil {
		os.RemoveAll(dest)
		s.fail(a, "Downloading and linking", err, logb.String())
		return
	}
	s.step(a.Name, 3, detail)
	if err := checkBundle(ctx, dest, a.Name); err != nil {
		os.RemoveAll(dest)
		s.fail(a, "Checking that it works", err, err.Error())
		return
	}
	m, err := s.cfg.Store.Finish(a.ID, a.Name, a.Version)
	if err != nil {
		os.RemoveAll(dest)
		s.fail(a, "Checking that it works", err, "")
		return
	}
	a.State, a.Size, a.Err, a.Log, a.Node = StateInstalled, m.Size, "", "", hostName()
	s.mu.Lock()
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
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidName(name) {
		return Request{}, fmt.Errorf("%q is not a package name", name)
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
	r.Status, r.Answer = "waiting", ""
	r.Askers = addUnique(r.Askers, who)
	if r.Reason == "" {
		r.Reason = clip(reason, 200)
	}
	return r, s.put("req:"+name, r)
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
		s.cfg.Store.RemoveBundle(a.ID)
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
	cur := s.MyList(home)
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

// ---- what the page shows -------------------------------------------------------------------------------------------------

// View is one app as a user sees it.
type View struct {
	At       int64      `json:"at,omitempty"`       // when it was installed or asked for
	Steps    []StepView `json:"steps,omitempty"`    // while installing: the list with what is done
	Pct      int        `json:"pct,omitempty"`      // how far the install is
	Detail   string     `json:"detail,omitempty"`   // what the solve found ("14 packages, 41 MB")
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
func (s *Service) Views(who, home string) []View {
	my := map[string]bool{}
	for _, id := range s.MyList(home) {
		my[id] = true
	}
	seen := map[string]bool{}
	var out []View
	for _, a := range s.Apps() {
		v := View{At: a.At, Name: a.Name, Version: a.Version, ID: a.ID, Kind: a.Kind, State: a.State, Safe: s.IsSafe(a.Name),
			On: my[a.ID], SizeMB: float64(a.Size) / (1 << 20), Mine: a.By == who}
		v.Packages = a.Packages
		if a.State == StateInstalling {
			if j, ok := s.Job(a.Name); ok {
				v.Step = j.Step
				v.Steps, v.Pct = stepViews(j)
				v.Detail = j.Detail
			} else {
				v.Step = "Starting"
			}
		}
		if a.State == StateFailed {
			v.Err, v.Log, v.Node = a.Err, a.Log, a.Node
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
		seen[a.Name] = true
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
	for _, n := range s.cfg.Safe() {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] || !ValidName(n) {
			continue
		}
		seen[n] = true
		out = append(out, View{Name: n, Kind: KindLib, State: "available", Safe: true})
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
	Full  bool                `json:"full"` // the list is complete: a bundle that is not in it may be removed
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
	var apt []string
	s.get("apt", &apt)
	snap.Apt = apt
	return snap
}

// NodeState is what one node has, for the admin page.
type NodeState struct {
	Have   []string          `json:"have"`
	Failed map[string]string `json:"failed,omitempty"`
	SizeMB float64           `json:"sizeMB"`
	At     int64             `json:"at"`
}

// Apply makes this node's store match a snapshot: the bundles that are missing are installed (the same pinned versions), the
// users' files are written, and, where the snapshot is complete, bundles that are no longer in it are removed. It is what a
// node does at its start, when it joins and when the catalog changes (a worker gets the snapshot from the gateway).
func (s *Service) Apply(ctx context.Context, snap Snapshot) NodeState {
	st := NodeState{Failed: map[string]string{}, At: s.now()}
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
		s.inst.Lock()
		dest := s.cfg.Store.BundlePath(a.ID)
		os.RemoveAll(dest)
		var logb bytes.Buffer
		var err error
		if s.cfg.Fetch != nil {
			// a worker takes the finished bundle from the gateway: it needs neither the internet nor the time of a solve
			if err = s.cfg.Fetch(ctx, a.ID, dest); err != nil {
				log.Printf("appstore: replay: %s could not be fetched from the gateway (%v); installing it here", a.ID, err)
				os.RemoveAll(dest)
			}
		}
		if s.cfg.Fetch == nil || err != nil {
			err = s.cfg.Installer.Install(ctx, a.Name, a.Version, dest, &logb)
		}
		if err == nil {
			_, err = s.cfg.Store.Finish(a.ID, a.Name, a.Version)
		}
		if err != nil {
			os.RemoveAll(dest)
			st.Failed[a.ID] = tail(err.Error()+" "+logb.String(), 300)
			log.Printf("appstore: replay: %s could not be installed here: %v", a.ID, err)
		}
		s.inst.Unlock()
	}
	if snap.Full {
		for _, id := range s.cfg.Store.Bundles() {
			if !want[id] {
				s.cfg.Store.RemoveBundle(id)
			}
		}
	}
	// the users: the bundles that are here; and an empty list for a user that has none any more
	homes := map[string]bool{}
	for home, ids := range snap.Users {
		homes[home] = true
		var have []string
		for _, id := range ids {
			if s.cfg.Store.HasBundle(id) {
				have = append(have, id)
			}
		}
		if ValidHome(home) {
			if err := s.cfg.Store.WriteManifest(home, have); err != nil {
				st.Failed["user:"+home] = err.Error()
			}
		}
	}
	for _, home := range s.cfg.Store.Homes() {
		if !homes[home] {
			s.cfg.Store.WriteManifest(home, nil)
		}
	}
	s.applyApt(ctx, snap.Apt, &st)
	st.Have = s.cfg.Store.Bundles()
	st.SizeMB = float64(s.cfg.Store.Size()) / (1 << 20)
	return st
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
	var l []string
	s.get("apt", &l)
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
