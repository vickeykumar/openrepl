package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"cookie"
	"utils"
)

// Usage numbers for the dashboard's charts: how many visitors started a
// terminal each day and in which language. They are counted where terminals
// are admitted (wrapControls), so a gateway counts the terminals of its
// workers too.

var statsFile = utils.GOTTY_PATH + "/admin-stats.json"

const (
	statsKeepDays  = 60
	statsSaveEvery = 30 * time.Second
	statsMaxSeen   = 20000 // visitors remembered for today, to count each once
)

// dayStats is one day's numbers.
type dayStats struct {
	Visitors  int            `json:"visitors"`
	Terminals map[string]int `json:"terminals"` // by language ("default" is the plain shell)
	// Seen holds a keyed hash of each of today's visitors so that one who
	// starts several terminals is counted once. It is kept for the current
	// day only, and holds no address or account name.
	Seen []string `json:"seen,omitempty"`

	seen map[string]bool
}

type statsStore struct {
	mu       sync.Mutex
	loaded   bool
	days     map[string]*dayStats
	lastSave time.Time
	now      func() time.Time
}

func (s *statsStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *statsStore) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.days = map[string]*dayStats{}
	data, err := ioutil.ReadFile(statsFile)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &s.days); err != nil {
		log.Println("stats: ignoring invalid ", statsFile, ": ", err)
		s.days = map[string]*dayStats{}
		return
	}
	for _, d := range s.days {
		d.seen = map[string]bool{}
		for _, h := range d.Seen {
			d.seen[h] = true
		}
		if d.Terminals == nil {
			d.Terminals = map[string]int{}
		}
	}
}

func (s *statsStore) save(now time.Time) {
	for day, d := range s.days {
		if day != now.UTC().Format("2006-01-02") {
			d.Seen = nil
		}
	}
	data, err := json.Marshal(s.days)
	if err != nil {
		return
	}
	if err := os.MkdirAll(utils.GOTTY_PATH, 0755); err != nil {
		return
	}
	tmp := statsFile + ".tmp"
	if ioutil.WriteFile(tmp, data, 0600) == nil {
		os.Rename(tmp, statsFile)
	}
	s.lastSave = now
}

// record counts a terminal started by visitor in the language.
func (s *statsStore) record(visitor, lang string) {
	if lang == "" {
		lang = "default"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.clock().UTC()
	day := now.Format("2006-01-02")
	d := s.days[day]
	if d == nil {
		d = &dayStats{Terminals: map[string]int{}, seen: map[string]bool{}}
		s.days[day] = d
	}
	d.Terminals[lang]++
	if visitor != "" && !d.seen[visitor] && len(d.seen) < statsMaxSeen {
		d.seen[visitor] = true
		d.Seen = append(d.Seen, visitor)
		d.Visitors++
	} else if visitor != "" && !d.seen[visitor] {
		d.Visitors++ // over the memory cap: counted again rather than dropped
	}
	// forget old days
	if len(s.days) > statsKeepDays {
		keys := make([]string, 0, len(s.days))
		for k := range s.days {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys[:len(keys)-statsKeepDays] {
			delete(s.days, k)
		}
	}
	if now.Sub(s.lastSave) >= statsSaveEvery {
		s.save(now)
	}
}

// flush writes the numbers to disk now.
func (s *statsStore) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		s.save(s.clock().UTC())
	}
}

// StatsDay is one entry of the dashboard's chart.
type StatsDay struct {
	Date      string         `json:"date"`
	Visitors  int            `json:"visitors"`
	Terminals int            `json:"terminals"`
	ByLang    map[string]int `json:"byLanguage"`
}

// recent returns the last n days, oldest first, empty days included.
func (s *statsStore) recent(n int) []StatsDay {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	today := s.clock().UTC()
	out := make([]StatsDay, 0, n)
	for i := n - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("2006-01-02")
		e := StatsDay{Date: day, ByLang: map[string]int{}}
		if d := s.days[day]; d != nil {
			e.Visitors = d.Visitors
			for lang, c := range d.Terminals {
				e.ByLang[lang] = c
				e.Terminals += c
			}
		}
		out = append(out, e)
	}
	return out
}

// visitorHash names a visitor without keeping who they are: a keyed hash of
// the account or, for a guest, the address, different on every day.
func visitorHash(r *http.Request, day string) string {
	id := cookie.Get_Uid(r)
	if id == "" {
		id = "ip:" + clientIP(r)
	}
	mac := hmac.New(sha256.New, cookie.SECRET_KEY)
	mac.Write([]byte(day + "|" + id))
	return hex.EncodeToString(mac.Sum(nil)[:8])
}

func (server *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	days := server.admin.stats.recent(30)
	totals := map[string]int{}
	for _, d := range days {
		for lang, c := range d.ByLang {
			totals[lang] += c
		}
	}
	adminJSON(w, http.StatusOK, map[string]interface{}{
		"days":      days,
		"languages": totals,
	})
}
