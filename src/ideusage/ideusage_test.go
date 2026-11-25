package ideusage

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu   sync.Mutex
	data map[string][]byte
	puts int
}

func (s *memStore) Get(k string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[k]
	return v, ok
}

func (s *memStore) Put(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[k] = v
	s.puts++
	return nil
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func setup(t *testing.T, c Config) (*Meter, *time.Time) {
	t.Helper()
	old := ConfigFunc
	ConfigFunc = func() Config { return c }
	t.Cleanup(func() { ConfigFunc = old })
	now := at("2026-11-25T10:00:00Z")
	m := New()
	m.SetClock(func() time.Time { return now })
	return m, &now
}

var (
	user  = Identity{Key: "ethaig2f", UID: "u1", Signed: true}
	guest = Identity{Key: "guest-abc", Guest: true}
)

func TestCountsByPartAndLimits(t *testing.T) {
	m, _ := setup(t, Defaults())
	m.Add(user, Terminal, 150*MB)
	st := m.Add(user, Genie, 30*MB)
	if st.Used != 180*MB || st.Pct != 90 || !st.Warn || st.AtLimit {
		t.Fatalf("after 180 MB: %+v", st)
	}
	if st.Parts["terminal"] != 150*MB || st.Parts["genie"] != 30*MB {
		t.Fatalf("parts: %+v", st.Parts)
	}
	st = m.Add(user, Editor, 25*MB)
	if !st.AtLimit || st.Warn || st.Pct != 100 || st.Scope != "day" {
		t.Fatalf("at the limit: %+v", st)
	}
	g := m.Add(guest, Terminal, 50*MB)
	if !g.AtLimit || g.Scope != "session" || g.Limit != 50*MB || g.ResetsAt != 0 {
		t.Fatalf("guest: %+v", g)
	}
}

func TestReserveIsSpentOnlyPastTheLimit(t *testing.T) {
	m, _ := setup(t, Defaults())
	m.Add(user, Terminal, 199*MB)
	if m.ReserveLeft(user) != 2*MB {
		t.Fatal("reserve touched before the limit")
	}
	m.Add(user, Terminal, 1*MB) // reaches the limit: still a counted byte, not a reserve one
	m.Add(user, Editor, 1*MB)
	if got := m.ReserveLeft(user); got != 1*MB {
		t.Fatalf("reserve left %d", got)
	}
	m.Add(user, Editor, 5*MB)
	if m.ReserveLeft(user) != 0 {
		t.Fatal("reserve cannot go below 0")
	}
}

func TestDayChangesAtTheResetMinute(t *testing.T) {
	c := Defaults()
	c.ResetMinutes = 330 // 05:30 UTC
	m, now := setup(t, c)
	*now = at("2026-11-25T05:00:00Z")
	m.Add(user, Terminal, 10*MB)
	*now = at("2026-11-25T05:29:00Z")
	if m.StateOf(user).Used != 10*MB {
		t.Fatal("same day before the reset minute")
	}
	*now = at("2026-11-25T05:31:00Z")
	if m.StateOf(user).Used != 0 {
		t.Fatal("a new day after the reset minute")
	}
	m.Add(user, Terminal, 1*MB)
	h := m.History("ethaig2f", 2)
	if h[0].Total != 10*MB || h[1].Total != 1*MB {
		t.Fatalf("history: %+v", h)
	}
	st := m.StateOf(user)
	if want := at("2026-11-26T05:30:00Z").UnixMilli(); st.ResetsAt != want {
		t.Fatalf("resets at %d want %d", st.ResetsAt, want)
	}
}

func TestResetAtMidnightUTCByDefault(t *testing.T) {
	m, _ := setup(t, Defaults())
	if want := at("2026-11-26T00:00:00Z").UnixMilli(); m.StateOf(user).ResetsAt != want {
		t.Fatalf("resets at %d want %d", m.StateOf(user).ResetsAt, want)
	}
}

func TestGuestSessionEndsWhenLeftAlone(t *testing.T) {
	m, now := setup(t, Defaults())
	m.Add(guest, Terminal, 40*MB)
	*now = now.Add(guestIdle + time.Minute)
	if m.StateOf(guest).Used != 0 {
		t.Fatal("an old guest session still counted")
	}
	m.Add(guest, Terminal, 1*MB)
	if m.StateOf(guest).Used != 1*MB {
		t.Fatal("a new session starts again")
	}
}

func TestOffExemptAndPartsOff(t *testing.T) {
	c := Defaults()
	c.On = false
	m, _ := setup(t, c)
	if st := m.Add(user, Terminal, 500*MB); st.Used != 0 || st.AtLimit {
		t.Fatalf("metering off still counts: %+v", st)
	}
	c = Defaults()
	c.Counted[Preview] = false
	ConfigFunc = func() Config { return c }
	m.Add(user, Preview, 100*MB)
	if m.StateOf(user).Used != 0 {
		t.Fatal("a part that is switched off is counted")
	}
	admin := Identity{Key: "adminhome", UID: "a", Admin: true, Signed: true}
	if st := m.Add(admin, Terminal, 900*MB); st.AtLimit || !st.Exempt || st.Used != 0 {
		t.Fatalf("admin: %+v", st)
	}
	c.AdminsExempt = false
	if st := m.Add(admin, Terminal, 900*MB); !st.AtLimit {
		t.Fatalf("a counted admin: %+v", st)
	}
}

func TestResetByHandAndSummary(t *testing.T) {
	m, _ := setup(t, Defaults())
	m.Add(user, Terminal, 10*MB)
	m.Add(Identity{Key: "second", UID: "u2", Signed: true}, Genie, 250*MB)
	m.Add(guest, Terminal, 5*MB)
	s := m.Summarize(5)
	if s.SignedCount != 2 || s.Total != 265*MB || s.GuestBytes != 5*MB || s.GuestsNow != 1 || s.AtLimit != 1 {
		t.Fatalf("summary: %+v", s)
	}
	if len(s.Top) != 2 || s.Top[0].UID != "u2" {
		t.Fatalf("top: %+v", s.Top)
	}
	if !m.Reset(m.KeyOfUID("u2")) || m.StateOf(Identity{Key: "second", UID: "u2", Signed: true}).Used != 0 {
		t.Fatal("reset by hand")
	}
}

func TestRecordsSurviveThroughTheStoreAndAreThrottled(t *testing.T) {
	st := &memStore{}
	m, now := setup(t, Defaults())
	m.SetStore(st)
	m.Add(user, Terminal, 7*MB)
	m.Save(false) // changed, never saved: saved now
	if st.puts == 0 {
		t.Fatal("not saved")
	}
	n := st.puts
	m.Add(user, Terminal, 1*MB)
	*now = now.Add(time.Minute)
	m.Save(false)
	if st.puts != n {
		t.Fatal("saved again within the throttle")
	}
	m.Save(true)
	if st.puts == n {
		t.Fatal("a forced save did not save")
	}
	// a new process reads the record when the account is seen again, and adds what it counted meanwhile
	m2 := New()
	m2.SetClock(func() time.Time { return *now })
	m2.SetStore(st)
	m2.Add(Identity{Key: "ethaig2f"}, Terminal, 1*MB) // a preview address: no uid yet
	got := m2.Add(user, Genie, 2*MB)
	if got.Used != 11*MB {
		t.Fatalf("after reload: %+v", got)
	}
}

func TestGuestsAreNeverSavedByName(t *testing.T) {
	st := &memStore{}
	m, _ := setup(t, Defaults())
	m.SetStore(st)
	m.Add(guest, Terminal, 3*MB)
	m.Save(true)
	for k := range st.data {
		if k != guestKey {
			t.Fatalf("a guest left a record: %s", k)
		}
	}
	if len(st.data) != 1 {
		t.Fatal("the guests' daily total was not saved")
	}
}

func TestRateWaitsThenGivesUp(t *testing.T) {
	r := NewRate()
	now := at("2026-11-25T10:00:00Z")
	r.now = func() time.Time { return now }
	r.sleep = func(d time.Duration) { now = now.Add(d) }
	cap, win, grace := int64(20*MB), 10*time.Second, 5*time.Second
	if err := r.Wait(15*MB, cap, win, grace); err != nil {
		t.Fatal(err)
	}
	// 15 + 10 is over 20: it pauses, and the window (10 s) is longer than the grace (5 s): the run ends
	start := now
	if err := r.Wait(10*MB, cap, win, grace); err != ErrTooFast {
		t.Fatalf("want ErrTooFast, got %v", err)
	}
	if d := now.Sub(start); d < grace || d > grace+time.Second {
		t.Fatalf("paused for %v", d)
	}
	// a burst that settles: with a long enough grace the window empties and the frame goes
	if err := r.Wait(10*MB, cap, win, 30*time.Second); err != nil {
		t.Fatalf("a settled burst was refused: %v", err)
	}
}

func TestRateOneBigFrameThroughAnEmptyWindow(t *testing.T) {
	r := NewRate()
	if err := r.Wait(50*MB, 20*MB, 10*time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
}

// the gate

type gateRec struct {
	mu     sync.Mutex
	frames []int64
	firsts []byte
	stopAt int // refuse the nth data frame (1 based), 0 never
	n      int
}

func (g *gateRec) Frame(first byte, size int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	g.frames = append(g.frames, size)
	g.firsts = append(g.firsts, first)
	if g.stopAt > 0 && g.n == g.stopAt {
		return ErrTooFast
	}
	return nil
}

func (g *gateRec) End(error) []byte { return TextFrame([]byte("END")) }

func readAll(c net.Conn, done chan []byte) {
	var b bytes.Buffer
	buf := make([]byte, 4096)
	for {
		n, err := c.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			done <- b.Bytes()
			return
		}
	}
}

func pipeGate(g Gate) (net.Conn, chan []byte) {
	a, b := net.Pipe()
	done := make(chan []byte, 1)
	go readAll(b, done)
	return GateConn(a, g), done
}

func TestGatePassesAnswerAndFramesAsTheyCame(t *testing.T) {
	g := &gateRec{}
	c, done := pipeGate(g)
	var want bytes.Buffer
	hs := []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n\r\n")
	f1 := TextFrame([]byte("1hello"))
	f2 := TextFrame(append([]byte("1"), bytes.Repeat([]byte("x"), 300)...)) // a 16 bit length
	f3 := CloseFrame(1000, "bye")
	want.Write(hs)
	want.Write(f1)
	want.Write(f2)
	want.Write(f3)
	all := want.Bytes()
	// written in odd pieces: the stream must come out the same
	for i := 0; i < len(all); i += 7 {
		j := i + 7
		if j > len(all) {
			j = len(all)
		}
		if _, err := c.Write(all[i:j]); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	got := <-done
	if !bytes.Equal(got, all) {
		t.Fatalf("stream changed: got %d bytes, want %d", len(got), len(all))
	}
	if len(g.frames) != 2 || g.frames[0] != int64(len(f1)) || g.frames[1] != int64(len(f2)) || g.firsts[0] != '1' {
		t.Fatalf("frames seen: %v %q (a close frame is not a data frame)", g.frames, g.firsts)
	}
}

func TestGateEndsTheStreamAtAFrameStart(t *testing.T) {
	g := &gateRec{stopAt: 2}
	c, done := pipeGate(g)
	f1 := TextFrame([]byte("1one"))
	f2 := TextFrame([]byte("1two"))
	f3 := TextFrame([]byte("1three"))
	if _, err := c.Write(f1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(append(append([]byte{}, f2...), f3...)); err == nil {
		t.Fatal("the refused frame was written")
	}
	if _, err := c.Write(f3); err == nil {
		t.Fatal("a later write went through after the end")
	}
	got := <-done
	want := append(append([]byte{}, f1...), TextFrame([]byte("END"))...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGateWithAFrameSplitInsideItsHeader(t *testing.T) {
	g := &gateRec{}
	c, done := pipeGate(g)
	f := TextFrame(append([]byte("1"), bytes.Repeat([]byte("y"), 70000)...)) // a 64 bit length
	for _, piece := range [][]byte{f[:1], f[1:3], f[3:11], f[11:12], f[12:]} {
		if _, err := c.Write(piece); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	if got := <-done; !bytes.Equal(got, f) {
		t.Fatal("a frame split in its header came out changed")
	}
	if len(g.frames) != 1 || g.frames[0] != int64(len(f)) {
		t.Fatalf("frames: %v", g.frames)
	}
}
