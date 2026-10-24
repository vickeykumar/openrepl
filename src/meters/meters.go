// Package meters holds the small counters behind the dashboard's live map
// (server/admin_architecture.go): how many requests, errors and operations
// there were in each 10-second stretch of the last 30 minutes, and a few
// gauges. They live in memory only, so a restart starts them again, and
// adding to one is a lock and an add: the packages that count (the site's
// handlers, the chat proxy, persist, filebrowser) do it without a second
// thought. They hold numbers and names of parts of the server, never a
// visitor's data.
package meters

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// BucketSeconds is how long one bucket is.
	BucketSeconds = 10
	// Buckets is how many are kept: 30 minutes.
	Buckets = 180
)

// Now is the clock; tests move it.
var Now = time.Now

func tick() int64 { return Now().Unix() / BucketSeconds }

// Series counts events per bucket.
type Series struct {
	mu    sync.Mutex
	vals  [Buckets]int64
	stamp [Buckets]int64 // the tick each slot holds, so an old slot is not read as new
}

// Add counts n more events now.
func (s *Series) Add(n int64) {
	t := tick()
	i := int(t % Buckets)
	s.mu.Lock()
	if s.stamp[i] != t {
		s.stamp[i], s.vals[i] = t, 0
	}
	s.vals[i] += n
	s.mu.Unlock()
}

// Sum is the events of the last seconds seconds (at most the 30 minutes kept).
func (s *Series) Sum(seconds int) int64 {
	n := (seconds + BucketSeconds - 1) / BucketSeconds
	if n > Buckets {
		n = Buckets
	}
	t := tick()
	var sum int64
	s.mu.Lock()
	for k := 0; k < n; k++ {
		i := int((t - int64(k)) % Buckets)
		if i < 0 {
			i += Buckets
		}
		if s.stamp[i] == t-int64(k) {
			sum += s.vals[i]
		}
	}
	s.mu.Unlock()
	return sum
}

// PerMinute is the rate over the last seconds seconds, in events a minute.
func (s *Series) PerMinute(seconds int) float64 {
	if seconds <= 0 {
		return 0
	}
	return float64(s.Sum(seconds)) * 60 / float64(seconds)
}

// Last returns the last n buckets, oldest first.
func (s *Series) Last(n int) []int64 {
	if n > Buckets {
		n = Buckets
	}
	t := tick()
	out := make([]int64, n)
	s.mu.Lock()
	for k := 0; k < n; k++ {
		at := t - int64(n-1-k)
		i := int(at % Buckets)
		if i < 0 {
			i += Buckets
		}
		if s.stamp[i] == at {
			out[k] = s.vals[i]
		}
	}
	s.mu.Unlock()
	return out
}

var (
	mu     sync.RWMutex
	series = map[string]*Series{}
	gauges = map[string]*int64{}
)

// Get returns the series of a name, made the first time.
func Get(name string) *Series {
	mu.RLock()
	s := series[name]
	mu.RUnlock()
	if s != nil {
		return s
	}
	mu.Lock()
	defer mu.Unlock()
	if s = series[name]; s == nil {
		s = &Series{}
		series[name] = s
	}
	return s
}

// Add counts n events of a name now.
func Add(name string, n int64) { Get(name).Add(n) }

func gauge(name string) *int64 {
	mu.RLock()
	g := gauges[name]
	mu.RUnlock()
	if g != nil {
		return g
	}
	mu.Lock()
	defer mu.Unlock()
	if g = gauges[name]; g == nil {
		g = new(int64)
		gauges[name] = g
	}
	return g
}

// GaugeAdd changes a gauge (something that goes up and down: terminals of a language).
func GaugeAdd(name string, n int64) { atomic.AddInt64(gauge(name), n) }

// GaugeValue is the value of a gauge, 0 if there is none.
func GaugeValue(name string) int64 { return atomic.LoadInt64(gauge(name)) }

// Gauges returns every gauge whose name starts with prefix, with the prefix
// taken off, sorted by name, and without those at 0.
func Gauges(prefix string) []Named {
	mu.RLock()
	defer mu.RUnlock()
	var out []Named
	for name, g := range gauges {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			if v := atomic.LoadInt64(g); v > 0 {
				out = append(out, Named{name[len(prefix):], v})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Named is one gauge.
type Named struct {
	Name  string
	Value int64
}

// Names of what is counted, so that the packages that count and the page that shows agree.
const (
	HTTPReq     = "http.req"      // requests to the site's pages and APIs
	HTTPErr     = "http.err"      // of them, answered with a server error
	ChatReq     = "chat.req."     // + the provider ("openai", "openrouter")
	ChatErr     = "chat.err."     // + the provider
	ChatMs      = "chat.ms."      // + the provider: the milliseconds the answers took, added up
	ChatTokens  = "chat.tokens"   // tokens the models reported
	DBOps       = "db.ops"        // reads and writes of the databases
	DBErr       = "db.err"        // of them, failed
	FileEvents  = "files.events"  // changes the file browsers reported
	FileWatches = "files.watches" // gauge: file browsers watching a folder now
	Repl        = "repl."         // gauge, + the program: terminals running it now
)
