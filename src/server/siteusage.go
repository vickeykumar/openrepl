package server

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cookie"
	"utils"
)

// How much time people spend on the site, and how they arrive. The page sends a
// small beat every half minute while somebody is really using it (usage-beat.js);
// this file adds the beats up for the dashboard's Users and Site usage views.
//
// What is kept: seconds of active time, counts, and labels (language, device
// class, country, where the visitor came from). Never code, terminal text,
// addresses or a guest's identity: a signed-in person is counted under their
// account, a guest only in the day's totals.

var usageFile = utils.GOTTY_PATH + "/admin-usage.json"

const (
	usageKeepDays      = 120
	usageUserDays      = 90 // days of active time kept for each account
	usageSaveEvery     = 30 * time.Second
	usageMaxBeatSecs   = 65 // the most one beat may add
	usageMinBeatGap    = 10 * time.Second
	usageVisitIdle     = 30 * time.Minute // a visit with no beat for this long is over
	usageMaxOpenVisits = 20000
	usageMaxUsers      = 100000
	usageMaxSeen       = 50000 // guests remembered for today, to count each once
	usageMaxLabels     = 40    // distinct sources kept for a day
	usageRecentVisits  = 10
	usageBeatsPerMin   = 8 // from one visitor, beats and new visits together
)

// siteDay is one day (UTC) of the whole site.
type siteDay struct {
	SignedVisitors int              `json:"signedVisitors"`
	GuestVisitors  int              `json:"guestVisitors"`
	SignedVisits   int              `json:"signedVisits"`
	GuestVisits    int              `json:"guestVisits"`
	SignedSecs     int64            `json:"signedSecs"`
	GuestSecs      int64            `json:"guestSecs"`
	GuestRan       int              `json:"guestRan"`   // guests who started a terminal
	GuestGenie     int              `json:"guestGenie"` // guests who asked Genie
	Devices        map[string]int   `json:"devices,omitempty"`
	Countries      map[string]int   `json:"countries,omitempty"`
	Sources        map[string]int   `json:"sources,omitempty"`
	Langs          map[string]int64 `json:"langs,omitempty"` // active seconds by language
	// Seen holds a keyed hash of each guest of today, in the kinds "v:" (opened
	// the site), "r:" (ran code) and "g:" (asked Genie), so that one guest is
	// counted once. It is kept for the current day only, and holds no address.
	Seen []string `json:"seen,omitempty"`

	seen map[string]bool
}

// visitRec is one visit of a signed-in user.
type visitRec struct {
	Start   int64  `json:"start"` // milliseconds
	Secs    int    `json:"secs"`
	Device  string `json:"device,omitempty"`
	Source  string `json:"source,omitempty"`
	Country string `json:"country,omitempty"`
}

// userUsage is what one account has used.
type userUsage struct {
	Secs      int64            `json:"secs"`
	Visits    int              `json:"visits"`
	Terminals int              `json:"terminals"`
	First     int64            `json:"first"` // milliseconds of the first beat
	Last      int64            `json:"last"`  // milliseconds of the latest beat
	Langs     map[string]int64 `json:"langs,omitempty"`
	Days      map[string]int   `json:"days,omitempty"` // active seconds by day
	Devices   map[string]int   `json:"devices,omitempty"`
	Recent    []*visitRec      `json:"recent,omitempty"` // oldest first
}

type openVisit struct {
	uid     string // "" for a guest
	last    time.Time
	started time.Time
	rec     *visitRec
}

type usageFileData struct {
	Since string                `json:"since"`
	Days  map[string]*siteDay   `json:"days"`
	Users map[string]*userUsage `json:"users"`
}

type usageStore struct {
	mu       sync.Mutex
	loaded   bool
	since    string
	days     map[string]*siteDay
	users    map[string]*userUsage
	open     map[string]*openVisit
	limit    map[string]*beatWindow
	lastSave time.Time
	now      func() time.Time
}

type beatWindow struct {
	n     int
	reset time.Time
}

// siteUsage is the server's one store, as tokenTotals is for Genie.
var siteUsage = &usageStore{}

func (s *usageStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *usageStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.days = map[string]*siteDay{}
	s.users = map[string]*userUsage{}
	s.open = map[string]*openVisit{}
	s.limit = map[string]*beatWindow{}
	data, err := ioutil.ReadFile(usageFile)
	if err == nil {
		var f usageFileData
		if err := json.Unmarshal(data, &f); err != nil {
			log.Println("usage: ignoring invalid ", usageFile, ": ", err)
		} else {
			s.since = f.Since
			if f.Days != nil {
				s.days = f.Days
			}
			if f.Users != nil {
				s.users = f.Users
			}
		}
	}
	for _, d := range s.days {
		d.seen = map[string]bool{}
		for _, h := range d.Seen {
			d.seen[h] = true
		}
	}
	if s.since == "" {
		s.since = s.clock().UTC().Format("2006-01-02")
	}
}

func (s *usageStore) save(now time.Time) {
	today := now.UTC().Format("2006-01-02")
	for day, d := range s.days {
		if day != today {
			d.Seen = nil
		}
	}
	data, err := json.Marshal(usageFileData{Since: s.since, Days: s.days, Users: s.users})
	if err != nil {
		return
	}
	if err := os.MkdirAll(utils.GOTTY_PATH, 0755); err != nil {
		return
	}
	tmp := usageFile + ".tmp"
	if ioutil.WriteFile(tmp, data, 0600) == nil {
		os.Rename(tmp, usageFile)
	}
	s.lastSave = now
}

// flush writes the numbers to disk now.
func (s *usageStore) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		s.save(s.clock().UTC())
	}
}

// day returns today's entry, making it. The caller holds s.mu.
func (s *usageStore) day(key string) *siteDay {
	d := s.days[key]
	if d == nil {
		d = &siteDay{seen: map[string]bool{}}
		s.days[key] = d
	}
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	return d
}

// firstToday records that a guest of kind ("v", "r" or "g") has been counted today,
// and says whether this is the first time.
func (d *siteDay) firstToday(kind, guest string) bool {
	if guest == "" {
		return false
	}
	k := kind + ":" + guest
	if d.seen[k] {
		return false
	}
	if len(d.seen) >= usageMaxSeen {
		return true // over the memory cap: counted again rather than dropped
	}
	d.seen[k] = true
	d.Seen = append(d.Seen, k)
	return true
}

func bump(m *map[string]int, key string, limit int) {
	if key == "" {
		return
	}
	if *m == nil {
		*m = map[string]int{}
	}
	if _, ok := (*m)[key]; !ok && limit > 0 && len(*m) >= limit {
		key = "Other"
	}
	(*m)[key]++
}

func (s *usageStore) trim(now time.Time) {
	if len(s.days) > usageKeepDays {
		keys := make([]string, 0, len(s.days))
		for k := range s.days {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys[:len(keys)-usageKeepDays] {
			delete(s.days, k)
		}
	}
	if len(s.open) > usageMaxOpenVisits {
		for id, v := range s.open {
			if now.Sub(v.last) > usageVisitIdle {
				delete(s.open, id)
			}
		}
	}
	if len(s.limit) > 10000 {
		for k, w := range s.limit {
			if now.After(w.reset) {
				delete(s.limit, k)
			}
		}
	}
}

// Beat is what the page tells the server about the last half minute.
type Beat struct {
	Visit  string `json:"vid"`
	Secs   int    `json:"secs"`
	Lang   string `json:"lang"`
	Device string `json:"device"`
	Ref    string `json:"ref"` // the host the visitor came from, as the browser reports it
}

// beatWho is who a beat is from: a signed-in account, or a guest named by the
// day's keyed hash.
type beatWho struct {
	uid, guest string
}

var (
	usageVisitPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,40}$`)
	usageLangPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_+.-]{0,19}$`)
	usageHostPattern  = regexp.MustCompile(`^[a-zA-Z0-9.-]{1,100}$`)
)

func cleanDevice(d string) string {
	switch d {
	case "desktop", "phone", "tablet":
		return d
	}
	return "unknown"
}

// countryOf reads the country Cloudflare saw the request from. A site not behind
// Cloudflare has none, and "unknown" is counted instead. The address itself is
// not looked at or kept.
func countryOf(r *http.Request) string {
	c := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
	if len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z' && c != "XX" && c != "T1" {
		return c
	}
	return "unknown"
}

var searchEngines = map[string]string{
	"google": "Google", "bing": "Bing", "duckduckgo": "DuckDuckGo", "yahoo": "Yahoo", "baidu": "Baidu",
	"yandex": "Yandex", "ecosia": "Ecosia", "brave": "Brave Search", "startpage": "Startpage", "naver": "Naver",
}

var socialSites = map[string]string{
	"github.com": "GitHub", "reddit.com": "Reddit", "news.ycombinator.com": "Hacker News", "ycombinator.com": "Hacker News",
	"twitter.com": "X", "x.com": "X", "t.co": "X", "facebook.com": "Facebook", "linkedin.com": "LinkedIn",
	"youtube.com": "YouTube", "dev.to": "dev.to", "medium.com": "Medium", "stackoverflow.com": "Stack Overflow",
	"producthunt.com": "Product Hunt", "whatsapp.com": "WhatsApp", "telegram.org": "Telegram", "t.me": "Telegram",
	"chatgpt.com": "ChatGPT", "perplexity.ai": "Perplexity", "claude.ai": "Claude",
}

// sourceOf names where a visit came from: "Direct", a search engine or a known
// site by name, or "site: host" for another one. own is the site's own host,
// which counts as direct (moving about the site is not a source).
func sourceOf(ref, own string) string {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if ref == "" || !usageHostPattern.MatchString(ref) {
		return "Direct"
	}
	ref = strings.TrimPrefix(ref, "www.")
	if own != "" {
		if h, _, err := net.SplitHostPort(own); err == nil {
			own = h
		}
		own = strings.TrimPrefix(strings.ToLower(own), "www.")
		if ref == own {
			return "Direct"
		}
	}
	for _, label := range strings.Split(ref, ".") {
		if name, ok := searchEngines[label]; ok {
			return name
		}
	}
	for host, name := range socialSites {
		if ref == host || strings.HasSuffix(ref, "."+host) {
			return name
		}
	}
	return "site: " + ref
}

// beat adds one beat from who. It returns false when the beat was refused: too
// many from one visitor, or not a valid visit id.
func (s *usageStore) beat(who beatWho, b Beat, country, own string) bool {
	if !usageVisitPattern.MatchString(b.Visit) || (who.uid == "" && who.guest == "") {
		return false
	}
	secs := b.Secs
	if secs < 0 {
		secs = 0
	}
	if secs > usageMaxBeatSecs {
		secs = usageMaxBeatSecs
	}
	lang := b.Lang
	if !usageLangPattern.MatchString(lang) {
		lang = ""
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.clock().UTC()
	day := now.Format("2006-01-02")

	// one visitor may not flood the store
	lk := who.uid
	if lk == "" {
		lk = who.guest
	}
	w := s.limit[lk]
	if w == nil || now.After(w.reset) {
		w = &beatWindow{reset: now.Add(time.Minute)}
		s.limit[lk] = w
	}
	if w.n++; w.n > usageBeatsPerMin {
		return false
	}

	d := s.day(day)
	var u *userUsage
	if who.uid != "" {
		u = s.users[who.uid]
		if u == nil {
			if len(s.users) >= usageMaxUsers {
				return false
			}
			u = &userUsage{}
			s.users[who.uid] = u
		}
	}

	ov := s.open[b.Visit]
	if ov != nil && ov.uid != who.uid {
		ov = nil // the visit id of somebody else: start a visit of our own
	}
	if ov != nil && now.Sub(ov.last) > usageVisitIdle {
		ov = nil
	}
	if ov == nil {
		ov = &openVisit{uid: who.uid, started: now, last: now.Add(-usageMaxBeatSecs * time.Second)}
		device, source := cleanDevice(b.Device), sourceOf(b.Ref, own)
		bump(&d.Devices, device, 0)
		bump(&d.Countries, country, 0)
		bump(&d.Sources, source, usageMaxLabels)
		if u != nil {
			d.SignedVisits++
			u.Visits++
			bump(&u.Devices, device, 0)
			ov.rec = &visitRec{Start: now.UnixNano() / 1e6, Device: device, Source: source, Country: country}
			u.Recent = append(u.Recent, ov.rec)
			if len(u.Recent) > usageRecentVisits {
				u.Recent = u.Recent[len(u.Recent)-usageRecentVisits:]
			}
			if u.First == 0 {
				u.First = ov.rec.Start
			}
		} else {
			d.GuestVisits++
			if d.firstToday("v", who.guest) {
				d.GuestVisitors++
			}
		}
		s.open[b.Visit] = ov
	}

	// a beat cannot claim more active time than has passed since the last one
	if elapsed := int(now.Sub(ov.last).Seconds()) + 5; secs > elapsed {
		secs = elapsed
	}
	if now.Sub(ov.last) < usageMinBeatGap {
		secs = 0
	}
	ov.last = now

	if secs > 0 {
		if u != nil {
			if u.Days == nil {
				u.Days = map[string]int{}
			}
			if u.Days[day] == 0 {
				d.SignedVisitors++
			}
			u.Days[day] += secs
			u.Secs += int64(secs)
			u.Last = now.UnixNano() / 1e6
			if lang != "" {
				if u.Langs == nil {
					u.Langs = map[string]int64{}
				}
				u.Langs[lang] += int64(secs)
			}
			if ov.rec != nil {
				ov.rec.Secs += secs
			}
			if len(u.Days) > usageUserDays {
				keys := make([]string, 0, len(u.Days))
				for k := range u.Days {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys[:len(keys)-usageUserDays] {
					delete(u.Days, k)
				}
			}
			d.SignedSecs += int64(secs)
		} else {
			d.GuestSecs += int64(secs)
		}
		if lang != "" {
			if d.Langs == nil {
				d.Langs = map[string]int64{}
			}
			d.Langs[lang] += int64(secs)
		}
	}
	s.trim(now)
	if now.Sub(s.lastSave) >= usageSaveEvery {
		s.save(now)
	}
	return true
}

// noteTerminal counts a terminal started by an account, or by a guest.
func (s *usageStore) noteTerminal(who beatWho) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	d := s.day(s.clock().UTC().Format("2006-01-02"))
	if who.uid != "" {
		if u := s.users[who.uid]; u != nil {
			u.Terminals++
		} else if len(s.users) < usageMaxUsers {
			s.users[who.uid] = &userUsage{Terminals: 1}
		}
		return
	}
	if d.firstToday("r", who.guest) {
		d.GuestRan++
	}
}

// noteGenie counts a guest who asked Genie. A signed-in user's Genie use has its
// own ledger (tokens.go).
func (s *usageStore) noteGenie(who beatWho) {
	if who.uid != "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	if d := s.day(s.clock().UTC().Format("2006-01-02")); d.firstToday("g", who.guest) {
		d.GuestGenie++
	}
}

// whoOf names the visitor of a request.
func whoOf(r *http.Request) beatWho {
	day := time.Now().UTC().Format("2006-01-02")
	if uid := cookie.Get_Uid(r); uid != "" {
		return beatWho{uid: uid}
	}
	return beatWho{guest: visitorHash(r, day)}
}

// handleUsageBeat takes the page's beat. It answers 204 whatever the outcome,
// since the page does nothing with the answer.
func (server *Server) handleUsageBeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// only the site's own pages may report
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var b Beat
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(b.Ref) > 100 {
		b.Ref = ""
	}
	siteUsage.beat(whoOf(r), b, countryOf(r), r.Host)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// ---- what the dashboard reads ---------------------------------------------------

// UserUsageSummary is a user's row in the Users table.
type UserUsageSummary struct {
	ActiveSecs    int64  `json:"activeSecs"`
	Visits        int    `json:"visits"`
	Days30        int    `json:"days30"`
	Bars          []int  `json:"bars"` // active minutes of each of the last 30 days, oldest first
	TopLang       string `json:"topLang"`
	LastVisitSecs int    `json:"lastVisitSecs"`
	LastActive    int64  `json:"lastActive"` // milliseconds, 0 if never
	Terminals     int    `json:"terminals"`
}

func topKey(m map[string]int64) string {
	best, bestV := "", int64(0)
	for k, v := range m {
		if v > bestV || (v == bestV && k < best) {
			best, bestV = k, v
		}
	}
	return best
}

func (s *usageStore) summaryOf(u *userUsage, today time.Time) UserUsageSummary {
	out := UserUsageSummary{ActiveSecs: u.Secs, Visits: u.Visits, LastActive: u.Last, Terminals: u.Terminals, TopLang: topKey(u.Langs), Bars: make([]int, 30)}
	for i := 0; i < 30; i++ {
		secs := u.Days[today.AddDate(0, 0, -(29-i)).Format("2006-01-02")]
		out.Bars[i] = secs / 60
		if secs > 0 {
			out.Days30++
		}
	}
	if n := len(u.Recent); n > 0 {
		out.LastVisitSecs = u.Recent[n-1].Secs
	}
	return out
}

// users returns the summary of every account that has usage.
func (s *usageStore) users30() map[string]UserUsageSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	today := s.clock().UTC()
	out := make(map[string]UserUsageSummary, len(s.users))
	for uid, u := range s.users {
		out[uid] = s.summaryOf(u, today)
	}
	return out
}

// NamedCount is a label with a number, for lists.
type NamedCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
	Share int    `json:"share"` // percent of the list's total
}

// UserUsageDetail is everything the panel of one user shows.
type UserUsageDetail struct {
	UserUsageSummary
	Since     string       `json:"since"` // the day tracking began
	DayMins   []int        `json:"dayMins"`
	Languages []NamedCount `json:"languages"`
	Devices   []NamedCount `json:"devices"`
	Recent    []visitRec   `json:"recent"` // newest first
	AvgVisit  int          `json:"avgVisitSecs"`
}

func ranked(m map[string]int64, limit int) []NamedCount {
	var total int64
	out := make([]NamedCount, 0, len(m))
	for k, v := range m {
		total += v
		out = append(out, NamedCount{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		if total > 0 {
			out[i].Share = int(out[i].Count * 100 / total)
		}
	}
	return out
}

func intsToInt64(m map[string]int) map[string]int64 {
	out := make(map[string]int64, len(m))
	for k, v := range m {
		out[k] = int64(v)
	}
	return out
}

func (s *usageStore) userDetail(uid string) (UserUsageDetail, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	u := s.users[uid]
	if u == nil {
		return UserUsageDetail{Since: s.since, DayMins: make([]int, 30), Languages: []NamedCount{}, Devices: []NamedCount{}, Recent: []visitRec{}, UserUsageSummary: UserUsageSummary{Bars: make([]int, 30)}}, false
	}
	today := s.clock().UTC()
	d := UserUsageDetail{UserUsageSummary: s.summaryOf(u, today), Since: s.since, Languages: ranked(u.Langs, 6), Devices: ranked(intsToInt64(u.Devices), 4)}
	d.DayMins = d.Bars
	for i := len(u.Recent) - 1; i >= 0; i-- {
		d.Recent = append(d.Recent, *u.Recent[i])
	}
	if d.Recent == nil {
		d.Recent = []visitRec{}
	}
	if u.Visits > 0 {
		d.AvgVisit = int(u.Secs / int64(u.Visits))
	}
	return d, true
}

// SiteUsageDay is one point of the Usage view's chart.
type SiteUsageDay struct {
	Date       string `json:"date"`
	Signed     int    `json:"signed"`
	Guests     int    `json:"guests"`
	SignedSecs int64  `json:"signedSecs"`
	GuestSecs  int64  `json:"guestSecs"`
}

// FunnelStep is a step of the guest journey.
type FunnelStep struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	Pct   int    `json:"pct"`
}

// Cohort is the accounts that first signed in during one week and how many came back.
type Cohort struct {
	Week string `json:"week"` // the Monday
	Size int    `json:"size"`
	D1   int    `json:"d1"` // percent, -1 when the week is too recent or before tracking began
	D7   int    `json:"d7"`
	D30  int    `json:"d30"`
}

// SiteUsage is the Usage view.
type SiteUsage struct {
	Since            string         `json:"since"`
	Range            int            `json:"range"`
	Days             []SiteUsageDay `json:"days"`
	AvgSigned        int            `json:"avgSigned"` // visitors a day
	AvgGuests        int            `json:"avgGuests"`
	AvgVisitSecs     int            `json:"avgVisitSecs"`
	AvgSignedVisit   int            `json:"avgSignedVisitSecs"`
	AvgGuestVisit    int            `json:"avgGuestVisitSecs"`
	GuestSignupPct   float64        `json:"guestSignupPct"` // new accounts per guest who ran code, percent
	Back7Pct         int            `json:"back7Pct"`       // -1 when there is not enough history
	Languages        []NamedCount   `json:"languages"`
	Devices          []NamedCount   `json:"devices"`
	Countries        []NamedCount   `json:"countries"`
	Sources          []NamedCount   `json:"sources"`
	Funnel           []FunnelStep   `json:"funnel"`
	Cohorts          []Cohort       `json:"cohorts"`
	NewAccounts      int            `json:"newAccounts"`
	TrackedAccounts  int            `json:"trackedAccounts"`
	GuestVisitorDays int            `json:"guestVisitorDays"`
}

func countList(m map[string]int, limit int) []NamedCount {
	return ranked(intsToInt64(m), limit)
}

// siteUsage works out the Usage view for the last n days. firstSignIns are the
// milliseconds at which each account first signed in (0 where it was not recorded).
func (s *usageStore) overview(n int, firstSignIns []int64) SiteUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	today := s.clock().UTC()
	out := SiteUsage{Since: s.since, Range: n, Days: make([]SiteUsageDay, 0, n)}
	devices, countries, sources := map[string]int{}, map[string]int{}, map[string]int{}
	langs := map[string]int64{}
	var signedSecs, guestSecs int64
	var signedVisits, guestVisits, ran, genie int
	for i := n - 1; i >= 0; i-- {
		key := today.AddDate(0, 0, -i).Format("2006-01-02")
		e := SiteUsageDay{Date: key}
		if d := s.days[key]; d != nil {
			e.Signed, e.Guests, e.SignedSecs, e.GuestSecs = d.SignedVisitors, d.GuestVisitors, d.SignedSecs, d.GuestSecs
			signedSecs += d.SignedSecs
			guestSecs += d.GuestSecs
			signedVisits += d.SignedVisits
			guestVisits += d.GuestVisits
			ran += d.GuestRan
			genie += d.GuestGenie
			out.GuestVisitorDays += d.GuestVisitors
			for k, v := range d.Devices {
				devices[k] += v
			}
			for k, v := range d.Countries {
				countries[k] += v
			}
			for k, v := range d.Sources {
				sources[k] += v
			}
			for k, v := range d.Langs {
				langs[k] += v
			}
		}
		out.Days = append(out.Days, e)
	}
	var sumS, sumG int
	for _, d := range out.Days {
		sumS += d.Signed
		sumG += d.Guests
	}
	out.AvgSigned, out.AvgGuests = sumS/n, sumG/n
	if signedVisits > 0 {
		out.AvgSignedVisit = int(signedSecs / int64(signedVisits))
	}
	if guestVisits > 0 {
		out.AvgGuestVisit = int(guestSecs / int64(guestVisits))
	}
	if v := signedVisits + guestVisits; v > 0 {
		out.AvgVisitSecs = int((signedSecs + guestSecs) / int64(v))
	}
	out.Languages = ranked(langs, 6)
	out.Devices = countList(devices, 4)
	out.Countries = countList(countries, 5)
	out.Sources = countList(sources, 6)

	// the accounts that joined in the range
	cutoff := today.AddDate(0, 0, -(n-1)).Truncate(24*time.Hour).UnixNano() / 1e6
	for _, ms := range firstSignIns {
		if ms >= cutoff {
			out.NewAccounts++
		}
	}
	opened := out.GuestVisitorDays
	out.Funnel = []FunnelStep{
		{Label: "Opened the site", Count: opened},
		{Label: "Ran some code", Count: ran},
		{Label: "Used Genie", Count: genie},
		{Label: "Signed up", Count: out.NewAccounts},
	}
	for i := range out.Funnel {
		if opened > 0 {
			out.Funnel[i].Pct = out.Funnel[i].Count * 100 / opened
		}
	}
	if ran > 0 {
		out.GuestSignupPct = float64(out.NewAccounts*1000/ran) / 10
	}
	// the caller fills in Back7Pct and Cohorts from the account list (cohorts)
	out.Back7Pct = -1
	out.Cohorts = []Cohort{}
	out.TrackedAccounts = len(s.users)
	return out
}

// Join is an account and when it first signed in, for working out who came back.
type Join struct {
	Uid string
	At  int64 // milliseconds
}

// cohorts groups accounts by the week they first signed in and says how many of
// each week were active again within a day, a week and a month. A figure is -1
// when the week is too recent to tell, or began before tracking did.
func (s *usageStore) cohorts(joins []Join, weeks int) ([]Cohort, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	today := s.clock().UTC().Truncate(24 * time.Hour)
	since, _ := time.Parse("2006-01-02", s.since)
	type counts struct{ size, el, ret [3]int }
	byWeek := map[string]*counts{}
	var all7 [2]int
	limits := [3]int{1, 7, 30}
	for _, j := range joins {
		if j.At <= 0 {
			continue
		}
		day := time.Unix(0, j.At*1e6).UTC().Truncate(24 * time.Hour)
		monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
		key := monday.Format("2006-01-02")
		c := byWeek[key]
		if c == nil {
			c = &counts{}
			byWeek[key] = c
		}
		c.size[0]++
		if day.Before(since) {
			continue // joined before tracking began: we cannot tell
		}
		u := s.users[j.Uid]
		for i, n := range limits {
			if day.AddDate(0, 0, n).After(today) {
				continue // too recent for this horizon
			}
			c.el[i]++
			back := false
			if u != nil {
				for k := 1; k <= n; k++ {
					if u.Days[day.AddDate(0, 0, k).Format("2006-01-02")] > 0 {
						back = true
						break
					}
				}
			}
			if back {
				c.ret[i]++
			}
			if n == 7 {
				all7[0]++
				if back {
					all7[1]++
				}
			}
		}
	}
	keys := make([]string, 0, len(byWeek))
	for k := range byWeek {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	if len(keys) > weeks {
		keys = keys[:weeks]
	}
	out := make([]Cohort, 0, len(keys))
	for _, k := range keys {
		c := byWeek[k]
		pct := func(i int) int {
			if c.el[i] == 0 {
				return -1
			}
			return c.ret[i] * 100 / c.el[i]
		}
		out = append(out, Cohort{Week: k, Size: c.size[0], D1: pct(0), D7: pct(1), D30: pct(2)})
	}
	back7 := -1
	if all7[0] > 0 {
		back7 = all7[1] * 100 / all7[0]
	}
	return out, back7
}
