// Package ideusage meters what the IDE sends out to each user (docs/lld/20-ide-usage.md).
//
// It counts the size of what the server sends to the browser from the parts of the IDE: the terminal, the editor, the file
// browser, Genie and the Preview pane. Only sizes are kept, never the content, and only what goes out: what the user sends in
// is not counted. The website itself (pages, scripts, images, the profile, the admin) is not counted.
//
// The count is for one workspace (the folder of a signed-in user or of a guest). A signed-in user has a total for a day (UTC,
// moved by the reset minute), a guest one for the session: a guest folder lasts an hour, and the count goes with it.
package ideusage

import (
	"encoding/json"
	"log"
	"sort"
	"sync"
	"time"
)

// Part is one part of the IDE.
type Part int

const (
	Terminal Part = iota
	Editor
	Files
	Genie
	Preview
	numParts
)

// Parts are the parts in the order the pages show them.
var Parts = []Part{Terminal, Genie, Files, Editor, Preview}

var partNames = [numParts]string{"terminal", "editor", "files", "genie", "preview"}

func (p Part) String() string {
	if p < 0 || p >= numParts {
		return "?"
	}
	return partNames[p]
}

// ParsePart is the part with a name, or false.
func ParsePart(name string) (Part, bool) {
	for i, n := range partNames {
		if n == name {
			return Part(i), true
		}
	}
	return 0, false
}

// Counts is bytes by part.
type Counts [numParts]int64

// Total is the sum of all parts.
func (c Counts) Total() int64 {
	var t int64
	for _, n := range c {
		t += n
	}
	return t
}

// Map is the counts by the name of the part, for JSON.
func (c Counts) Map() map[string]int64 {
	m := make(map[string]int64, numParts)
	for i, n := range c {
		m[partNames[i]] = n
	}
	return m
}

// Config is what the admin set, with the built-in value where a setting is empty.
type Config struct {
	On           bool // metering is on
	AdminsExempt bool
	Counted      [numParts]bool

	TermBytes     int64 // most a terminal may send in one window
	TermWindow    time.Duration
	TermGrace     time.Duration // how long a terminal over the cap is paused before the run is ended
	TermPauseOnly bool          // pause only: the run is not ended

	GuestBytes   int64 // for a guest session
	UserBytes    int64 // for a signed-in user's day
	WarnPct      int
	ReserveBytes int64 // kept open at the limit, so that people can still save and fetch their own work
	SmallBytes   int64 // the largest file the editor still opens at the limit
	ResetMinutes int   // minutes after 00:00 UTC at which a day starts, 0 to 1439
}

const MB = 1 << 20

// Defaults are the built-in settings (the canvas, boards 2, 3 and 7).
func Defaults() Config {
	c := Config{
		On: true, AdminsExempt: true,
		TermBytes: 20 * MB, TermWindow: 10 * time.Second, TermGrace: 5 * time.Second,
		GuestBytes: 50 * MB, UserBytes: 200 * MB, WarnPct: 80, ReserveBytes: 2 * MB, SmallBytes: 256 << 10,
	}
	for i := range c.Counted {
		c.Counted[i] = true
	}
	return c
}

// ConfigFunc gives the current settings. The server sets it; the default is Defaults.
var ConfigFunc = Defaults

func cfg() Config { return ConfigFunc() }

// Identity says whose count it is.
type Identity struct {
	Key    string // the workspace folder name
	UID    string // the account, "" for a guest
	Admin  bool
	Guest  bool
	Signed bool
}

// State is where a count stands against its limit.
type State struct {
	Used     int64            `json:"used"`
	Limit    int64            `json:"limit"`
	Pct      int              `json:"pct"`
	Parts    map[string]int64 `json:"parts"`
	Scope    string           `json:"scope"` // "day" or "session"
	Warn     bool             `json:"warn"`
	AtLimit  bool             `json:"atLimit"`
	ResetsAt int64            `json:"resetsAt"` // milliseconds; 0 for a guest session
	WarnPct  int              `json:"warnPct"`
	Exempt   bool             `json:"exempt"`
	On       bool             `json:"on"`
	Plan     string           `json:"plan"`
	Term     int64            `json:"termBytes"`
	TermSecs int              `json:"termSecs"`
	Reserve  int64            `json:"reserve"`
	// SignInLimit is what a signed-in user gets, for the note that asks a guest to sign in
	SignInLimit int64 `json:"signInLimit"`
}

type acct struct {
	UID     string            `json:"uid,omitempty"`
	Day     string            `json:"day"` // the day of Used
	Used    Counts            `json:"used"`
	Reserve int64             `json:"reserve"`        // bytes sent past the limit, from the reserve
	Last    int64             `json:"last"`           // milliseconds
	Hist    map[string]Counts `json:"hist,omitempty"` // by day, signed-in users only
	Guest   bool              `json:"guest,omitempty"`
	Admin   bool              `json:"admin,omitempty"` // seen as an admin (a preview address carries no cookie to say so)
	TZ      *int              `json:"tz,omitempty"`    // minutes east of UTC, as the page told, for the time in a notice

	loaded  bool      // the saved record of the account was read (signed-in accounts)
	changed bool      // there is something not saved yet
	savedAt time.Time // when it was saved last
}

// record is what is saved for a signed-in account, in the user database next to the profile.
type record struct {
	Day     string            `json:"day"`
	Used    Counts            `json:"used"`
	Reserve int64             `json:"reserve"`
	Hist    map[string]Counts `json:"hist,omitempty"`
}

// Store is where the records are kept: the user database, which also holds the profile (package user).
type Store interface {
	Get(key string) ([]byte, bool)
	Put(key string, data []byte) error
}

const (
	userPrefix = "ide-usage:u:"
	guestKey   = "ide-usage:guests" // all guests together, by day
)

// Meter keeps the counts.
type Meter struct {
	mu           sync.Mutex
	store        Store
	now          func() time.Time
	accts        map[string]*acct
	guest        map[string]Counts
	guestChanged bool
	guestSaved   time.Time
}

const (
	keepDays    = 90
	maxAccts    = 200000
	guestIdle   = 3 * time.Hour    // a guest session that nobody used for this long is forgotten
	saveEvery   = 30 * time.Minute // a signed-in account is saved at most this often: the database writes through to a remote one
	tickEvery   = time.Minute
	maxGuestAge = 400
)

// New is a meter with no store yet (SetStore); until then it keeps its counts in memory.
func New() *Meter {
	return &Meter{now: time.Now, accts: map[string]*acct{}, guest: map[string]Counts{}}
}

// SetClock replaces the clock, for tests.
func (m *Meter) SetClock(now func() time.Time) { m.now = now }

// SetStore sets where records are kept and reads the guests' totals.
func (m *Meter) SetStore(st Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = st
	if st == nil {
		return
	}
	if data, ok := st.Get(guestKey); ok {
		var g map[string]Counts
		if json.Unmarshal(data, &g) == nil && g != nil {
			m.guest = g
		}
	}
}

// load reads the saved record of a signed-in account the first time its uid is known, and adds what was counted meanwhile.
func (m *Meter) load(a *acct, c Config) {
	if a.loaded || a.UID == "" || a.Guest || m.store == nil {
		return
	}
	a.loaded = true
	data, ok := m.store.Get(userPrefix + a.UID)
	if !ok {
		return
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		log.Println("ideusage: ignoring the record of an account: ", err)
		return
	}
	a.Hist = r.Hist
	if r.Day == a.Day || a.Day == "" {
		for i := range a.Used {
			a.Used[i] += r.Used[i]
		}
		a.Reserve += r.Reserve
		a.Day = r.Day
	} else if r.Day != "" {
		if a.Hist == nil {
			a.Hist = map[string]Counts{}
		}
		a.Hist[r.Day] = r.Used
	}
}

// dayKey is the day a time belongs to: days start at the reset minute (UTC).
func dayKey(t time.Time, resetMinutes int) string {
	return t.UTC().Add(-time.Duration(resetMinutes) * time.Minute).Format("2006-01-02")
}

// nextReset is when the day of t ends.
func nextReset(t time.Time, resetMinutes int) time.Time {
	t = t.UTC()
	day := t.Add(-time.Duration(resetMinutes) * time.Minute).Truncate(24 * time.Hour)
	return day.Add(24*time.Hour + time.Duration(resetMinutes)*time.Minute)
}

func (m *Meter) acctFor(id Identity, c Config) *acct {
	a := m.accts[id.Key]
	now := m.now()
	if a == nil {
		if len(m.accts) >= maxAccts {
			m.sweep(now)
		}
		a = &acct{Guest: id.Guest}
		m.accts[id.Key] = a
	}
	if id.UID != "" {
		a.UID = id.UID
	}
	if id.Admin {
		a.Admin = true
	}
	a.Guest = id.Guest
	today := dayKey(now, c.ResetMinutes)
	if !id.Guest && a.UID != "" && !a.loaded {
		if a.Day == "" {
			a.Day = today
		}
		m.load(a, c)
	}
	if id.Guest {
		// a guest's count is the session's: it starts again only when the folder was left alone for long
		if a.Last > 0 && now.Sub(time.UnixMilli(a.Last)) > guestIdle {
			a.Used, a.Reserve = Counts{}, 0
		}
		if a.Day == "" {
			a.Day = today
		}
		return a
	}
	if a.Day != today {
		if a.Day != "" && a.Used.Total() > 0 {
			if a.Hist == nil {
				a.Hist = map[string]Counts{}
			}
			a.Hist[a.Day] = a.Used
			m.trimHist(a, now)
		}
		a.Day, a.Used, a.Reserve = today, Counts{}, 0
	}
	return a
}

func (m *Meter) trimHist(a *acct, now time.Time) {
	cut := now.UTC().AddDate(0, 0, -keepDays).Format("2006-01-02")
	for d := range a.Hist {
		if d < cut {
			delete(a.Hist, d)
		}
	}
}

// sweep drops the guests nobody used for long, and the accounts that have nothing left to show.
func (m *Meter) sweep(now time.Time) {
	for k, a := range m.accts {
		last := time.UnixMilli(a.Last)
		if a.Guest && now.Sub(last) > guestIdle {
			delete(m.accts, k)
		} else if !a.Guest && len(a.Hist) == 0 && a.Used.Total() == 0 && now.Sub(last) > 24*time.Hour {
			delete(m.accts, k)
		}
	}
}

func limitFor(id Identity, c Config) int64 {
	if id.Guest {
		return c.GuestBytes
	}
	return c.UserBytes
}

// Add counts n bytes sent to the user from a part, and says where the count stands.
func (m *Meter) Add(id Identity, p Part, n int64) State {
	c := cfg()
	if !c.On || id.Key == "" || p < 0 || p >= numParts || !c.Counted[p] || (id.Admin && c.AdminsExempt) {
		return m.state(id, c)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.acctFor(id, c)
	now := m.now()
	if n > 0 {
		wasOver := a.Used.Total() >= limitFor(id, c)
		a.Used[p] += n
		if wasOver {
			a.Reserve += n
		}
		a.Last = now.UnixMilli()
		day := dayKey(now, c.ResetMinutes)
		if id.Guest {
			g := m.guest[day]
			g[p] += n
			m.guest[day] = g
			m.guestChanged = true
		}
		a.changed = true
	}
	return m.stateOf(a, id, c)
}

// Touch notes that the user is here (so a guest session stays), without counting anything. A page that knows its time zone says
// it (minutes east of UTC; tz nil: not known), for the time a notice names.
func (m *Meter) Touch(id Identity, tz *int) {
	if id.Key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accts[id.Key]
	if a == nil {
		a = &acct{Guest: id.Guest, UID: id.UID, Admin: id.Admin}
		m.accts[id.Key] = a
	}
	a.Last = m.now().UnixMilli()
	if tz != nil && *tz >= -14*60 && *tz <= 14*60 {
		v := *tz
		a.TZ = &v
	}
}

// Known says what the meter remembers of a workspace without a cookie to ask: whether it was seen as an admin, whether it is a
// guest's, and the page's time zone (minutes east of UTC; 0 and false when not known).
func (m *Meter) Known(key string) (admin, guest bool, tz int, hasTZ bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accts[key]
	if a == nil {
		return false, false, 0, false
	}
	if a.TZ != nil {
		tz, hasTZ = *a.TZ, true
	}
	return a.Admin, a.Guest, tz, hasTZ
}

// StateOf is where the count of a user stands. It counts nothing.
func (m *Meter) StateOf(id Identity) State { return m.state(id, cfg()) }

func (m *Meter) state(id Identity, c Config) State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id.Key == "" {
		return m.stateOf(&acct{}, id, c)
	}
	a := m.accts[id.Key]
	if a == nil {
		a = &acct{Guest: id.Guest}
	} else {
		// a day that ended shows as a new one without changing the record
		today := dayKey(m.now(), c.ResetMinutes)
		if !id.Guest && a.Day != today {
			a = &acct{UID: a.UID, Day: today}
		} else if id.Guest && a.Last > 0 && m.now().Sub(time.UnixMilli(a.Last)) > guestIdle {
			a = &acct{Guest: true}
		}
	}
	return m.stateOf(a, id, c)
}

func (m *Meter) stateOf(a *acct, id Identity, c Config) State {
	limit := limitFor(id, c)
	used := a.Used.Total()
	s := State{
		Used: used, Limit: limit, Parts: a.Used.Map(), Scope: "day", WarnPct: c.WarnPct, On: c.On,
		Exempt: id.Admin && c.AdminsExempt, Plan: "Free", Term: c.TermBytes, TermSecs: int(c.TermWindow / time.Second), Reserve: c.ReserveBytes, SignInLimit: c.UserBytes,
	}
	if limit > 0 {
		s.Pct = int(used * 100 / limit)
		if s.Pct > 100 {
			s.Pct = 100
		}
	}
	s.AtLimit = c.On && !s.Exempt && limit > 0 && used >= limit
	s.Warn = c.On && !s.Exempt && limit > 0 && used*100 >= limit*int64(c.WarnPct) && !s.AtLimit
	if id.Guest {
		s.Scope = "session"
	} else {
		s.ResetsAt = nextReset(m.now(), c.ResetMinutes).UnixMilli()
	}
	return s
}

// AtLimit reports whether the user has used all of the limit.
func (m *Meter) AtLimit(id Identity) bool { return m.StateOf(id).AtLimit }

// ReserveLeft is how much of the reserve is still open to a user at the limit.
func (m *Meter) ReserveLeft(id Identity) int64 {
	c := cfg()
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accts[id.Key]
	if a == nil {
		return c.ReserveBytes
	}
	left := c.ReserveBytes - a.Reserve
	if left < 0 {
		return 0
	}
	return left
}

// Reset forgets today's count of an account, by hand (the admin's button).
func (m *Meter) Reset(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.accts[key]
	if a == nil {
		return false
	}
	a.Used, a.Reserve = Counts{}, 0
	a.changed = true
	return true
}

// KeyOfUID is the workspace key of an account the meter has seen.
func (m *Meter) KeyOfUID(uid string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, a := range m.accts {
		if a.UID == uid && !a.Guest {
			return k
		}
	}
	return ""
}

// Day is one day of an account.
type Day struct {
	Day   string           `json:"day"`
	Total int64            `json:"total"`
	Parts map[string]int64 `json:"parts"`
}

// History is the last days of an account, oldest first, today included.
func (m *Meter) History(key string, days int) []Day {
	c := cfg()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	a := m.accts[key]
	out := make([]Day, 0, days)
	for i := days - 1; i >= 0; i-- {
		d := dayKey(now.AddDate(0, 0, -i), c.ResetMinutes)
		var cs Counts
		if a != nil {
			if a.Day == d {
				cs = a.Used
			} else if h, ok := a.Hist[d]; ok {
				cs = h
			}
		}
		out = append(out, Day{Day: d, Total: cs.Total(), Parts: cs.Map()})
	}
	return out
}

// Summary is what the admin card shows for today.
type Summary struct {
	Day         string           `json:"day"`
	Total       int64            `json:"total"`
	Parts       map[string]int64 `json:"parts"`
	GuestBytes  int64            `json:"guestBytes"`
	GuestsNow   int              `json:"guestsNow"` // guest sessions counted in the last hour
	SignedCount int              `json:"signedCount"`
	AtLimit     int              `json:"atLimit"`
	Top         []TopUser        `json:"top"`
}

// TopUser is one signed-in account of the day's list.
type TopUser struct {
	Key   string           `json:"key"`
	UID   string           `json:"uid,omitempty"`
	Name  string           `json:"name,omitempty"`
	Total int64            `json:"total"`
	Limit int64            `json:"limit"`
	Parts map[string]int64 `json:"parts"`
}

// Summarize is today for all users: the totals and the top signed-in accounts.
func (m *Meter) Summarize(top int) Summary {
	c := cfg()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	today := dayKey(now, c.ResetMinutes)
	s := Summary{Day: today}
	var all Counts
	var list []TopUser
	for k, a := range m.accts {
		if a.Guest {
			if now.Sub(time.UnixMilli(a.Last)) <= time.Hour {
				s.GuestsNow++
				if a.Used.Total() >= c.GuestBytes && c.GuestBytes > 0 {
					s.AtLimit++
				}
			}
			continue
		}
		if a.Day != today || a.Used.Total() == 0 {
			continue
		}
		s.SignedCount++
		for i, n := range a.Used {
			all[i] += n
		}
		if a.Used.Total() >= c.UserBytes && c.UserBytes > 0 {
			s.AtLimit++
		}
		list = append(list, TopUser{Key: k, UID: a.UID, Total: a.Used.Total(), Limit: c.UserBytes, Parts: a.Used.Map()})
	}
	g := m.guest[today]
	s.GuestBytes = g.Total()
	for i, n := range g {
		all[i] += n
	}
	s.Total = all.Total()
	s.Parts = all.Map()
	sort.Slice(list, func(i, j int) bool { return list[i].Total > list[j].Total })
	if top > 0 && len(list) > top {
		list = list[:top]
	}
	s.Top = list
	return s
}

// Save writes the records that changed: a signed-in account at most every saveEvery (all of them when force), and the guests' totals.
func (m *Meter) Save(force bool) {
	m.mu.Lock()
	st := m.store
	if st == nil {
		m.mu.Unlock()
		return
	}
	now := m.now()
	type put struct {
		key  string
		data []byte
	}
	var puts []put
	for _, a := range m.accts {
		if a.Guest || a.UID == "" || !a.changed || (!force && now.Sub(a.savedAt) < saveEvery) {
			continue
		}
		m.trimHist(a, now)
		data, err := json.Marshal(record{Day: a.Day, Used: a.Used, Reserve: a.Reserve, Hist: a.Hist})
		if err != nil {
			continue
		}
		puts = append(puts, put{userPrefix + a.UID, data})
		a.changed, a.savedAt = false, now
	}
	if m.guestChanged && (force || now.Sub(m.guestSaved) >= saveEvery) {
		cut := now.UTC().AddDate(0, 0, -maxGuestAge).Format("2006-01-02")
		for d := range m.guest {
			if d < cut {
				delete(m.guest, d)
			}
		}
		if data, err := json.Marshal(m.guest); err == nil {
			puts = append(puts, put{guestKey, data})
			m.guestChanged, m.guestSaved = false, now
		}
	}
	m.sweep(now)
	m.mu.Unlock()
	for _, p := range puts {
		if err := st.Put(p.key, p.data); err != nil {
			log.Println("ideusage: saving: ", err)
		}
	}
}

// Run saves what changed every minute until stop is closed, and everything once more at the end.
func (m *Meter) Run(stop <-chan struct{}) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.Save(false)
		case <-stop:
			m.Save(true)
			return
		}
	}
}
