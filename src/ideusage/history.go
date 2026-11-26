package ideusage

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// The history of all signed-in accounts, and the accounts that need a look (canvas "IDE data usage: daily history and top users").
// The records were already kept (up to HistoryDays of daily counts for each signed-in account); this reads them for an admin.

// Stops is how often a terminal was paused, and how often a run was ended, for printing too fast, in one day.
type Stops struct {
	Paused int `json:"p,omitempty"`
	Ended  int `json:"e,omitempty"`
}

// coldRec is the saved record of an account that is not in memory, read for the history and kept for a while.
type coldRec struct {
	r  record
	at time.Time
}

const (
	coldTTL      = 10 * time.Minute
	maxColdReads = 500 // records read from the store in one call: the rest come on the next call, from the cache
	// FlagMinJump is the smallest day a jump is flagged for: a small account doubling is no news.
	FlagMinJump = 20 * MB
)

// NoteStop counts a pause or an ended run of a terminal today, for the account.
func (m *Meter) NoteStop(id Identity, ended bool) {
	c := cfg()
	if !c.On || id.Key == "" || id.Guest || id.UID == "" || (id.Admin && c.AdminsExempt) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.acctFor(id, c)
	if a.Stops == nil {
		a.Stops = map[string]Stops{}
	}
	d := dayKey(m.now(), c.ResetMinutes)
	s := a.Stops[d]
	if ended {
		s.Ended++
	} else {
		s.Paused++
	}
	a.Stops[d] = s
	a.changed = true
}

// acctDays is one signed-in account across days.
type acctDays struct {
	key, uid string
	days     map[string]Counts
	stops    map[string]Stops
}

// roster is every signed-in account the meter knows: those in memory, and those that have a saved record (the index), read from the store.
func (m *Meter) roster(period int) []acctDays {
	c := cfg()
	now := m.now()
	from := dayKey(now.AddDate(0, 0, -(period-1)), c.ResetMinutes)
	today := dayKey(now, c.ResetMinutes)

	// which records must be read: in the index, with a day in the period, and not in memory with its record loaded
	m.mu.Lock()
	type want struct {
		uid, last string
	}
	var wants []want
	inMem := map[string]bool{}
	for _, a := range m.accts {
		if !a.Guest && a.UID != "" && a.loaded {
			inMem[a.UID] = true
		}
	}
	for uid, last := range m.index {
		if inMem[uid] || last < from {
			continue
		}
		if cr := m.cold[uid]; cr != nil && now.Sub(cr.at) < coldTTL {
			continue
		}
		wants = append(wants, want{uid, last})
	}
	// a memory account whose record was not read yet needs its record too
	for _, a := range m.accts {
		if !a.Guest && a.UID != "" && !a.loaded && m.index[a.UID] != "" {
			if cr := m.cold[a.UID]; cr == nil || now.Sub(cr.at) >= coldTTL {
				wants = append(wants, want{a.UID, m.index[a.UID]})
			}
		}
	}
	st := m.store
	m.mu.Unlock()
	sort.Slice(wants, func(i, j int) bool { return wants[i].last > wants[j].last })
	if len(wants) > maxColdReads {
		wants = wants[:maxColdReads]
	}
	read := map[string]*coldRec{}
	if st != nil {
		for _, w := range wants {
			data, ok := st.Get(userPrefix + w.uid)
			if !ok {
				continue
			}
			var r record
			if json.Unmarshal(data, &r) == nil {
				read[w.uid] = &coldRec{r: r, at: now}
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for uid, cr := range read {
		m.cold[uid] = cr
	}
	for uid, cr := range m.cold { // forget old ones
		if now.Sub(cr.at) > 4*coldTTL {
			delete(m.cold, uid)
		}
	}
	var out []acctDays
	seen := map[string]bool{}
	for k, a := range m.accts {
		if a.Guest || a.UID == "" || seen[a.UID] {
			continue
		}
		seen[a.UID] = true
		ad := acctDays{key: k, uid: a.UID, days: map[string]Counts{}, stops: map[string]Stops{}}
		if cr := m.cold[a.UID]; cr != nil && !a.loaded {
			for d, cs := range cr.r.Hist {
				ad.days[d] = cs
			}
			for d, s := range cr.r.Stops {
				ad.stops[d] = s
			}
			if cr.r.Day != "" {
				ad.days[cr.r.Day] = cr.r.Used
			}
		}
		for d, cs := range a.Hist {
			ad.days[d] = cs
		}
		for d, s := range a.Stops {
			ad.stops[d] = s
		}
		if a.Day != "" && (a.Used.Total() > 0 || len(ad.days) == 0) {
			cur := ad.days[a.Day]
			if !a.loaded {
				for i := range cur {
					cur[i] += a.Used[i]
				}
				ad.days[a.Day] = cur
			} else {
				ad.days[a.Day] = a.Used
			}
		}
		out = append(out, ad)
	}
	for uid, cr := range m.cold {
		if seen[uid] || cr.r.Day == "" && len(cr.r.Hist) == 0 {
			continue
		}
		seen[uid] = true
		ad := acctDays{uid: uid, days: map[string]Counts{}, stops: map[string]Stops{}}
		for d, cs := range cr.r.Hist {
			ad.days[d] = cs
		}
		for d, s := range cr.r.Stops {
			ad.stops[d] = s
		}
		if cr.r.Day != "" {
			ad.days[cr.r.Day] = cr.r.Used
		}
		out = append(out, ad)
	}
	_ = today
	return out
}

// AcctRow is one account in the table of a period.
type AcctRow struct {
	Key       string           `json:"key,omitempty"`
	UID       string           `json:"uid"`
	Name      string           `json:"name,omitempty"`
	Email     string           `json:"email,omitempty"`
	Total     int64            `json:"total"`
	Avg       int64            `json:"avg"`    // the total over the days of the period
	Series    []int64          `json:"series"` // a total for each day, oldest first
	LimitDays int              `json:"limitDays"`
	Run       int              `json:"run"` // the longest run of days at the limit in the period
	Parts     map[string]int64 `json:"parts"`
	Biggest   string           `json:"biggest"`
	Today     int64            `json:"today"`
	Peak      int64            `json:"peak"`
	PeakDay   string           `json:"peakDay,omitempty"`
	Limit     int64            `json:"limit"`
	Paused    int              `json:"paused"` // today
	Ended     int              `json:"ended"`  // today
	Used      int              `json:"usedDays"`
}

// DayRow is one day of all signed-in accounts together, and of the guests.
type DayRow struct {
	Day      string           `json:"day"`
	Total    int64            `json:"total"`
	Parts    map[string]int64 `json:"parts"`
	Guests   int64            `json:"guests"`
	Sessions int              `json:"guestSessions"`
}

// Busiest is the day signed-in accounts were sent the most.
type Busiest struct {
	Day   string `json:"day"`
	Total int64  `json:"total"`
	Part  string `json:"part"`
}

// Report is what the Data usage tab shows for a period.
type Report struct {
	Day           string    `json:"day"` // today
	Period        int       `json:"period"`
	HistoryDays   int       `json:"historyDays"`
	Limit         int64     `json:"limit"`
	Days          []DayRow  `json:"days"`
	Accounts      []AcctRow `json:"accounts"`
	Sent          int64     `json:"sent"`   // to signed-in accounts
	Guests        int64     `json:"guests"` // to guests, combined, never listed
	Counted       int       `json:"counted"`
	AtLimit       int       `json:"atLimit"`       // accounts that reached the limit in the period
	LimitAcctDays int       `json:"limitAcctDays"` // account-days at the limit
	Busiest       Busiest   `json:"busiest"`
}

func biggestPart(cs Counts) string {
	best, bn := Terminal, int64(-1)
	for _, p := range Parts {
		if cs[p] > bn {
			best, bn = p, cs[p]
		}
	}
	if bn <= 0 {
		return ""
	}
	return best.String()
}

// dayList is the days of a period, oldest first, today last.
func dayList(now time.Time, c Config, period int) []string {
	out := make([]string, 0, period)
	for i := period - 1; i >= 0; i-- {
		out = append(out, dayKey(now.AddDate(0, 0, -i), c.ResetMinutes))
	}
	return out
}

func rowOf(ad acctDays, days []string, c Config) AcctRow {
	r := AcctRow{Key: ad.key, UID: ad.uid, Series: make([]int64, len(days)), Limit: c.UserBytes}
	var parts Counts
	run, longest := 0, 0
	for i, d := range days {
		cs := ad.days[d]
		t := cs.Total()
		r.Series[i] = t
		for k, n := range cs {
			parts[k] += n
		}
		r.Total += t
		if t > 0 {
			r.Used++
		}
		if t > r.Peak {
			r.Peak, r.PeakDay = t, d
		}
		if c.UserBytes > 0 && t >= c.UserBytes {
			r.LimitDays++
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	r.Run = longest
	r.Parts = parts.Map()
	r.Biggest = biggestPart(parts)
	if len(days) > 0 {
		r.Avg = r.Total / int64(len(days))
		today := days[len(days)-1]
		r.Today = ad.days[today].Total()
		r.Paused, r.Ended = ad.stops[today].Paused, ad.stops[today].Ended
	}
	return r
}

// Report is the history of all signed-in accounts for the last `period` days (1 to the days kept), today last, with the accounts by what they
// were sent, most first. The guests are one number for each day, never a list.
func (m *Meter) Report(period int) Report {
	c := cfg()
	if period < 1 {
		period = 1
	}
	keep := c.HistoryDays
	if keep < 1 {
		keep = keepDays
	}
	if period > keep {
		period = keep
	}
	ros := m.roster(period)
	now := m.now()
	days := dayList(now, c, period)
	rep := Report{Day: days[len(days)-1], Period: period, HistoryDays: keep, Limit: c.UserBytes, Days: make([]DayRow, len(days))}
	for i, d := range days {
		rep.Days[i] = DayRow{Day: d, Parts: Counts{}.Map()}
	}
	dayParts := make([]Counts, len(days))
	for _, ad := range ros {
		r := rowOf(ad, days, c)
		if r.Total == 0 {
			continue
		}
		if r.Total >= MB {
			rep.Counted++
		}
		if r.LimitDays > 0 {
			rep.AtLimit++
			rep.LimitAcctDays += r.LimitDays
		}
		for i, d := range days {
			cs := ad.days[d]
			for k, n := range cs {
				dayParts[i][k] += n
			}
		}
		rep.Accounts = append(rep.Accounts, r)
	}
	m.mu.Lock()
	for i, d := range days {
		rep.Days[i].Total = dayParts[i].Total()
		rep.Days[i].Parts = dayParts[i].Map()
		g := m.guest[d]
		rep.Days[i].Guests = g.Total()
		rep.Days[i].Sessions = m.guestSess[d]
		rep.Sent += rep.Days[i].Total
		rep.Guests += rep.Days[i].Guests
		if rep.Days[i].Total > rep.Busiest.Total {
			rep.Busiest = Busiest{Day: d, Total: rep.Days[i].Total, Part: biggestPart(dayParts[i])}
		}
	}
	m.mu.Unlock()
	sort.Slice(rep.Accounts, func(i, j int) bool {
		if rep.Accounts[i].Total != rep.Accounts[j].Total {
			return rep.Accounts[i].Total > rep.Accounts[j].Total
		}
		return rep.Accounts[i].UID < rep.Accounts[j].UID
	})
	return rep
}

// Flag is an account worth opening today.
type Flag struct {
	UID   string `json:"uid"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Kind  string `json:"kind"` // stops, limit, jump, near, watched
	Text  string `json:"text"`
	Today int64  `json:"today"`
}

// Bytes is a size for a sentence: KB under a megabyte, MB, GB.
func Bytes(n int64) string {
	switch {
	case n <= 0:
		return "0 KB"
	case n < MB:
		k := (n + 512) / 1024
		if k < 1 {
			k = 1
		}
		return fmt.Sprintf("%d KB", k)
	case n >= 1024*MB:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1024*MB))
	case n >= 10*MB:
		return fmt.Sprintf("%d MB", (n+MB/2)/MB)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/float64(MB))
}

var flagOrder = map[string]int{"stops": 0, "limit": 1, "jump": 2, "near": 3, "watched": 4}

// Flags is the accounts to open today, by the thresholds of the settings: at the limit several days in a row, a day many times the usual,
// close to the limit, a terminal stopped many times, and the accounts on the watch list. watch is the uids on it.
func (m *Meter) Flags(watch []string) []Flag {
	c := cfg()
	if !c.On {
		return nil
	}
	now := m.now()
	days := dayList(now, c, 15) // today and the 14 days before
	ros := m.roster(15)
	watched := map[string]bool{}
	for _, w := range watch {
		watched[w] = true
	}
	var out []Flag
	for _, ad := range ros {
		r := rowOf(ad, days, c)
		today := r.Today
		part := ""
		if len(days) > 0 {
			part = biggestPart(ad.days[days[len(days)-1]])
		}
		mostly := ""
		if part != "" {
			mostly = ", mostly " + part
		}
		got := false
		add := func(kind, text string) {
			out = append(out, Flag{UID: ad.uid, Kind: kind, Text: text, Today: today})
			got = true
		}
		if c.FlagStops > 0 && r.Paused+r.Ended > c.FlagStops {
			add("stops", fmt.Sprintf("A terminal was paused %d times and ended %d times for printing too fast.", r.Paused, r.Ended))
		}
		if c.FlagLimitDays > 0 && c.UserBytes > 0 {
			// the run of days at the limit that ends today, or yesterday when today is not there yet
			end := len(days) - 1
			if r.Series[end] < c.UserBytes {
				end--
			}
			run := 0
			for i := end; i >= 0 && r.Series[i] >= c.UserBytes; i-- {
				run++
			}
			if run >= c.FlagLimitDays {
				add("limit", fmt.Sprintf("At the limit %d days in a row. %s today%s.", run, Bytes(today), mostly))
			}
		}
		if c.FlagJump > 0 && today >= FlagMinJump {
			var sum int64
			for i := 0; i < len(r.Series)-1; i++ {
				sum += r.Series[i]
			}
			usual := sum / 14
			if usual > 0 && today >= int64(c.FlagJump)*usual {
				add("jump", fmt.Sprintf("%d times its usual day (usual %s, today %s)%s.", today/usual, Bytes(usual), Bytes(today), mostly))
			}
		}
		if c.FlagNearPct > 0 && c.UserBytes > 0 && today < c.UserBytes && today*100 >= c.UserBytes*int64(c.FlagNearPct) {
			add("near", fmt.Sprintf("At %d%% of the limit, with the day still going.", today*100/c.UserBytes))
		}
		if !got && watched[ad.uid] {
			add("watched", fmt.Sprintf("On your watch list. %s today, normal.", Bytes(today)))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if flagOrder[out[i].Kind] != flagOrder[out[j].Kind] {
			return flagOrder[out[i].Kind] < flagOrder[out[j].Kind]
		}
		return out[i].Today > out[j].Today
	})
	// a watched account that has no count today is still on the list
	have := map[string]bool{}
	for _, f := range out {
		have[f.UID] = true
	}
	for _, w := range watch {
		if !have[w] {
			out = append(out, Flag{UID: w, Kind: "watched", Text: "On your watch list. Nothing counted today.", Today: 0})
		}
	}
	return out
}

// AcctView is one account over a period, with the parts of each day.
type AcctView struct {
	AcctRow
	Days []DayRow `json:"days"`
}

// Account is the history of one signed-in account for the last `period` days, or false when nothing is known of it.
func (m *Meter) Account(uid string, period int) (AcctView, bool) {
	c := cfg()
	if period < 1 {
		period = 1
	}
	keep := c.HistoryDays
	if keep < 1 {
		keep = keepDays
	}
	if period > keep {
		period = keep
	}
	days := dayList(m.now(), c, period)
	for _, ad := range m.roster(period) {
		if ad.uid != uid {
			continue
		}
		v := AcctView{AcctRow: rowOf(ad, days, c), Days: make([]DayRow, len(days))}
		for i, d := range days {
			cs := ad.days[d]
			v.Days[i] = DayRow{Day: d, Total: cs.Total(), Parts: cs.Map()}
		}
		return v, true
	}
	return AcctView{}, false
}
