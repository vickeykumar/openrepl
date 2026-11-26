package ideusage

import (
	"strings"
	"testing"
	"time"
)

// the days of an account: Report, the days at the limit, the guests' day, and the history kept after a restart (the index and the records)
func TestTheReportHasTheDaysOfAllAccountsAndTheGuestsByDay(t *testing.T) {
	st := &memStore{}
	m, now := setup(t, Defaults())
	m.SetStore(st)
	a := Identity{Key: "ka", UID: "ua", Signed: true}
	b := Identity{Key: "kb", UID: "ub", Signed: true}
	for i := 0; i < 5; i++ { // five days: a is at the limit on days 3 and 4, b uses a little
		m.Add(a, Terminal, int64(40+i*80)*MB)
		m.Add(b, Genie, 3*MB)
		if i == 2 {
			m.Add(guest, Terminal, 7*MB)
			m.Add(Identity{Key: "guest-2", Guest: true}, Files, 1*MB)
		}
		*now = now.Add(24 * time.Hour)
	}
	rep := m.Report(7)
	if rep.Period != 7 || len(rep.Days) != 7 || rep.Day != "2026-11-30" {
		t.Fatalf("days: %+v", rep.Day)
	}
	if len(rep.Accounts) != 2 || rep.Accounts[0].UID != "ua" || rep.Accounts[1].UID != "ub" {
		t.Fatalf("accounts: %+v", rep.Accounts)
	}
	ra := rep.Accounts[0]
	if ra.Total != (40+120+200+280+360)*MB || ra.LimitDays != 3 || ra.Run != 3 || ra.Biggest != "terminal" || len(ra.Series) != 7 || ra.Avg != ra.Total/7 {
		t.Fatalf("account a: %+v", ra)
	}
	if ra.Peak != 360*MB || ra.PeakDay != "2026-11-29" || ra.Today != 0 {
		t.Fatalf("peak: %+v", ra)
	}
	if rep.AtLimit != 1 || rep.LimitAcctDays != 3 || rep.Counted != 2 {
		t.Fatalf("totals: %+v", rep)
	}
	if rep.Busiest.Total != 363*MB || rep.Busiest.Day != "2026-11-29" || rep.Busiest.Part != "terminal" {
		t.Fatalf("busiest: %+v", rep.Busiest)
	}
	var guests int64
	var sessions int
	for _, d := range rep.Days {
		guests += d.Guests
		sessions += d.Sessions
	}
	if rep.Guests != 8*MB || guests != 8*MB || sessions != 2 {
		t.Fatalf("guests: %d %d sessions %d", rep.Guests, guests, sessions)
	}
	// a shorter period leaves out the older days
	if r3 := m.Report(2); len(r3.Days) != 2 || r3.Accounts[0].Total != 360*MB {
		t.Fatalf("2 days: %+v", r3.Accounts)
	}
}

func TestTheHistoryOfAnAccountIsKnownAfterARestart(t *testing.T) {
	st := &memStore{}
	m, now := setup(t, Defaults())
	m.SetStore(st)
	a := Identity{Key: "ka", UID: "ua", Signed: true}
	m.Add(a, Terminal, 60*MB)
	*now = now.Add(24 * time.Hour)
	m.Add(a, Genie, 20*MB)
	m.Save(true)
	if _, ok := st.data[indexKey]; !ok {
		t.Fatal("the index of the accounts was not saved")
	}
	// a new meter (the server restarted): nobody was seen yet, but the records are there
	m2 := New()
	m2.SetClock(func() time.Time { return *now })
	m2.SetStore(st)
	rep := m2.Report(30)
	if len(rep.Accounts) != 1 || rep.Accounts[0].UID != "ua" || rep.Accounts[0].Total != 80*MB || rep.Accounts[0].Today != 20*MB {
		t.Fatalf("after a restart: %+v", rep.Accounts)
	}
	v, ok := m2.Account("ua", 7)
	if !ok || len(v.Days) != 7 || v.Days[6].Parts["genie"] != 20*MB || v.Days[5].Parts["terminal"] != 60*MB {
		t.Fatalf("one account: %v %+v", ok, v.Days)
	}
	if _, ok := m2.Account("nobody", 7); ok {
		t.Fatal("an account that was never counted was found")
	}
	// the account is seen again today: it is not counted twice
	m2.Add(Identity{Key: "ka", UID: "ua", Signed: true}, Files, 5*MB)
	if rep := m2.Report(30); rep.Accounts[0].Total != 85*MB || len(rep.Accounts) != 1 {
		t.Fatalf("seen again: %+v", rep.Accounts)
	}
}

func TestHistoryIsKeptForTheDaysTheSettingSays(t *testing.T) {
	c := Defaults()
	c.HistoryDays = 30
	st := &memStore{}
	m, now := setup(t, c)
	m.SetStore(st)
	a := Identity{Key: "ka", UID: "ua", Signed: true}
	m.Add(a, Terminal, 1*MB)
	*now = now.Add(45 * 24 * time.Hour)
	m.Add(a, Terminal, 1*MB) // the old day goes into the history and is dropped at once
	rep := m.Report(90)
	if rep.Period != 30 || rep.HistoryDays != 30 || rep.Accounts[0].Total != 1*MB {
		t.Fatalf("30 days kept: period %d %+v", rep.Period, rep.Accounts)
	}
}

func TestTerminalStopsAreCountedForAnAccountAndADay(t *testing.T) {
	m, _ := setup(t, Defaults())
	a := Identity{Key: "ka", UID: "ua", Signed: true}
	m.Add(a, Terminal, 1*MB)
	for i := 0; i < 3; i++ {
		m.NoteStop(a, false)
	}
	m.NoteStop(a, true)
	m.NoteStop(guest, true)                                                     // a guest is not counted
	m.NoteStop(Identity{Key: "kx", UID: "ux", Admin: true, Signed: true}, true) // an admin is exempt
	r := m.Report(1).Accounts[0]
	if r.Paused != 3 || r.Ended != 1 {
		t.Fatalf("stops: %+v", r)
	}
}

func TestAccountsThatNeedALook(t *testing.T) {
	c := Defaults()
	m, now := setup(t, c)
	add := func(uid string, mb int64, part Part) {
		m.Add(Identity{Key: "k" + uid, UID: uid, Signed: true}, part, mb*MB)
	}
	// 14 days before today: l is at the limit for the last 3 days, j is usually small, quiet is small
	for d := 0; d < 14; d++ {
		add("j", 10, Terminal)
		add("quiet", 5, Genie)
		if d >= 11 {
			add("l", 210, Terminal)
		}
		*now = now.Add(24 * time.Hour)
	}
	add("l", 210, Terminal)    // today, a 4th day in a row
	add("j", 120, Preview)     // 12 times the usual 10 MB, mostly preview
	add("n", 185, Terminal)    // 92% of 200 MB
	add("small", 15, Terminal) // a jump, but under 20 MB: never flagged
	add("watchme", 3, Genie)
	s := Identity{Key: "ks", UID: "s", Signed: true}
	m.Add(s, Terminal, 1*MB)
	for i := 0; i < 4; i++ {
		m.NoteStop(s, false)
	}
	for i := 0; i < 2; i++ {
		m.NoteStop(s, true)
	}
	flags := m.Flags([]string{"watchme", "ghost"})
	kinds := map[string]string{}
	for _, f := range flags {
		kinds[f.UID+":"+f.Kind] = f.Text
	}
	if !strings.Contains(kinds["l:limit"], "4 days in a row") || !strings.Contains(kinds["l:limit"], "210 MB today, mostly terminal") {
		t.Errorf("limit: %q", kinds["l:limit"])
	}
	if !strings.Contains(kinds["j:jump"], "12 times its usual day (usual 10 MB, today 120 MB), mostly preview") {
		t.Errorf("jump: %q", kinds["j:jump"])
	}
	if !strings.Contains(kinds["n:near"], "At 92% of the limit") {
		t.Errorf("near: %q", kinds["n:near"])
	}
	if !strings.Contains(kinds["s:stops"], "paused 4 times and ended 2 times") {
		t.Errorf("stops: %q", kinds["s:stops"])
	}
	if _, ok := kinds["small:jump"]; ok {
		t.Error("a small day was flagged as a jump")
	}
	if _, ok := kinds["quiet:jump"]; ok {
		t.Error("a quiet account was flagged")
	}
	if !strings.Contains(kinds["watchme:watched"], "On your watch list. 3.0 MB today, normal.") || !strings.Contains(kinds["ghost:watched"], "Nothing counted today") {
		t.Errorf("watched: %q %q", kinds["watchme:watched"], kinds["ghost:watched"])
	}
	// the worst first: stops, then the limit, then the jump
	if flags[0].Kind != "stops" || flags[1].Kind != "limit" || flags[2].Kind != "jump" {
		t.Errorf("order: %+v", flags)
	}
	// the thresholds are the settings
	c.FlagLimitDays, c.FlagJump, c.FlagNearPct, c.FlagStops = 10, 50, 99, 50
	ConfigFunc = func() Config { return c }
	if f := m.Flags(nil); len(f) != 0 {
		t.Errorf("with high thresholds: %+v", f)
	}
}

func TestBytesForASentence(t *testing.T) {
	for n, want := range map[int64]string{0: "0 KB", 1: "1 KB", 31 * 1024: "31 KB", MB: "1.0 MB", 212 * MB: "212 MB", 1300 * MB: "1.27 GB"} {
		if got := Bytes(n); got != want {
			t.Errorf("%d: %q want %q", n, got, want)
		}
	}
}

func TestRateSaysWhenItHeldAFrame(t *testing.T) {
	r := NewRate()
	var now time.Time = at("2026-11-25T10:00:00Z")
	r.now = func() time.Time { return now }
	r.sleep = func(d time.Duration) { now = now.Add(d) }
	if p, err := r.WaitPaused(5, 10, 10*time.Second, 5*time.Second); p || err != nil {
		t.Fatalf("room: %v %v", p, err)
	}
	if p, err := r.WaitPaused(8, 10, 10*time.Second, 5*time.Second); !p || err != ErrTooFast {
		t.Fatalf("no room for long: %v %v", p, err)
	}
}
