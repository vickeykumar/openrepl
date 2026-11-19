package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func freshHealth(t *testing.T) *genieHealthStore {
	t.Helper()
	return &genieHealthStore{models: map[string]*modelHealth{}}
}

func TestHealthOutcomesNameWhyACallFailed(t *testing.T) {
	cases := map[int]string{200: "ok", 302: "ok", 402: "no_credit", 429: "limit", 404: "no_host", 500: "host_error", 503: "host_error", 400: "refused", 401: "refused"}
	for code, want := range cases {
		if got := healthOutcomeOfStatus(code); got != want {
			t.Errorf("status %d: %q, want %q", code, got, want)
		}
	}
	if healthOutcomeOfError(context.DeadlineExceeded) != "timeout" || healthOutcomeOfError(errors.New("connection reset")) != "network" {
		t.Error("a deadline is a timeout and any other error is the network")
	}
	_ = http.StatusOK
}

func TestHealthReportCountsFailuresAndTimesPerModel(t *testing.T) {
	g := freshHealth(t)
	now := time.Now()
	m := chatModel{ID: "a/b", Name: "Model B"}
	for i := 0; i < 8; i++ {
		g.note(m, "ok", 300*time.Millisecond, 2*time.Second, now)
	}
	g.note(m, "limit", 100*time.Millisecond, 100*time.Millisecond, now)
	g.note(m, "host_error", 100*time.Millisecond, 100*time.Millisecond, now)
	rep := g.report(24, 20, 8000, now)
	if len(rep.Rows) != 1 {
		t.Fatalf("rows: %+v", rep.Rows)
	}
	r := rep.Rows[0]
	if r.Calls != 10 || r.Errors != 2 || r.Reasons["limit"] != 1 || r.Reasons["host_error"] != 1 {
		t.Fatalf("counts: %+v", r)
	}
	if r.ErrPct != 20 || len(r.Alerts) != 1 || r.Alerts[0] != "many calls fail" || rep.Alerting != 1 {
		t.Fatalf("20%% of 10 calls is at the limit: %+v", r)
	}
	if r.P50Ms != 500 || r.P95Ms != 500 {
		t.Errorf("eight calls of 300 ms fall in the 500 ms bucket: p50 %d p95 %d", r.P50Ms, r.P95Ms)
	}
}

func TestHealthAFewCallsSayNothingAndASlowModelIsFlagged(t *testing.T) {
	g := freshHealth(t)
	now := time.Now()
	few := chatModel{ID: "few"}
	g.note(few, "timeout", 0, 30*time.Second, now)
	g.note(few, "timeout", 0, 30*time.Second, now)
	slow := chatModel{ID: "slow", Name: "Slow"}
	for i := 0; i < 6; i++ {
		g.note(slow, "ok", 20*time.Second, 25*time.Second, now)
	}
	rep := g.report(24, 20, 8000, now)
	byID := map[string]healthRow{}
	for _, r := range rep.Rows {
		byID[r.Model] = r
	}
	if len(byID["few"].Alerts) != 0 {
		t.Errorf("two calls are too few to flag: %+v", byID["few"])
	}
	s := byID["slow"]
	if len(s.Alerts) != 1 || s.Alerts[0] != "slow to start answering" || s.P95Ms != 32000 {
		t.Errorf("slow: %+v", s)
	}
	if rep.Rows[0].Model != "slow" {
		t.Errorf("the model that needs a look comes first: %v", rep.Rows[0].Model)
	}
	// slower than the last bound is -1
	g2 := freshHealth(t)
	for i := 0; i < 6; i++ {
		g2.note(slow, "ok", 50*time.Second, 55*time.Second, now)
	}
	if r := g2.report(24, 20, 8000, now).Rows[0]; r.P95Ms != -1 || len(r.Alerts) != 1 {
		t.Errorf("over the last bound: %+v", r)
	}
}

func TestHealthKeepsOnlyTheWindowAndForgetsOldHours(t *testing.T) {
	g := freshHealth(t)
	now := time.Now()
	m := chatModel{ID: "m"}
	g.note(m, "ok", time.Second, time.Second, now.Add(-30*time.Hour))
	g.note(m, "ok", time.Second, time.Second, now)
	if r := g.report(24, 20, 8000, now).Rows; len(r) != 1 || r[0].Calls != 1 {
		t.Errorf("the last 24 hours hold one call: %+v", r)
	}
	if r := g.report(48, 20, 8000, now).Rows; len(r) != 1 || r[0].Calls != 2 {
		t.Errorf("the last 48 hours hold two: %+v", r)
	}
	g.note(m, "ok", time.Second, time.Second, now.Add(60*time.Hour))
	g.mu.Lock()
	n := len(g.models["m"].hours)
	g.mu.Unlock()
	if n != 1 {
		t.Errorf("hours older than %d are dropped: %d kept", healthHours, n)
	}
}

func TestTheAdminSeesTheGenieHealth(t *testing.T) {
	_, h := adminTestServer(t)
	old := genieHealth
	genieHealth = freshHealth(t)
	defer func() { genieHealth = old }()
	now := time.Now()
	for i := 0; i < 5; i++ {
		genieHealth.note(chatModel{ID: "x/y", Name: "X"}, "limit", 0, time.Second, now)
	}
	var r healthReply
	decode(t, get(h, "/admin/genie-health?hours=6"), &r)
	if r.Hours != 6 || len(r.Rows) != 1 || r.Rows[0].Errors != 5 || r.Alerting != 1 || r.ErrPct != defaultHealthErrPct || r.SlowMs != defaultHealthSlowMs {
		t.Fatalf("reply: %+v", r)
	}
	decode(t, get(h, "/admin/genie-health?hours=999"), &r)
	if r.Hours != 24 {
		t.Errorf("a window outside 1 to 48 is 24: %d", r.Hours)
	}
}

func TestTheHealthLimitsAreChecked(t *testing.T) {
	s := SiteSettings{GenieHealthErrPct: 101}
	if _, err := s.normalize(); err == nil {
		t.Error("101 percent is refused")
	}
	s = SiteSettings{GenieHealthSlowMs: 100}
	if _, err := s.normalize(); err == nil {
		t.Error("100 ms is refused")
	}
	if e, sl := healthLimits(SiteSettings{GenieHealthErrPct: 5, GenieHealthSlowMs: 2000}); e != 5 || sl != 2000 {
		t.Errorf("limits: %d %d", e, sl)
	}
	if e, sl := healthLimits(SiteSettings{}); e != defaultHealthErrPct || sl != defaultHealthSlowMs {
		t.Errorf("defaults: %d %d", e, sl)
	}
}

func TestAutoNeedsTwoModelsThatAreOnAndDifferent(t *testing.T) {
	ok := GenieSettings{AutoOn: true, AutoSmall: "gpt-6-luna", AutoStrong: "gpt-4o-mini"}
	if err := ok.normalizeAuto(); err != nil {
		t.Fatalf("two models of the site: %v", err)
	}
	for name, g := range map[string]GenieSettings{
		"no small":    {AutoOn: true, AutoStrong: "gpt-4o-mini"},
		"not a model": {AutoOn: true, AutoSmall: "nope", AutoStrong: "gpt-4o-mini"},
		"the same":    {AutoOn: true, AutoSmall: "gpt-4o-mini", AutoStrong: "gpt-4o-mini"},
		"one is off":  {AutoOn: true, AutoSmall: "gpt-6-luna", AutoStrong: "gpt-4o-mini", DisabledModels: []string{"gpt-4o-mini"}},
		"bad default": {AutoOn: true, AutoSmall: "gpt-6-luna", AutoStrong: "gpt-4o-mini", AutoDefault: "some"},
	} {
		if err := g.normalizeAuto(); err == nil {
			t.Errorf("%s is refused", name)
		}
	}
	off := GenieSettings{AutoSmall: "nope"}
	if err := off.normalizeAuto(); err != nil || off.autoPublic() != nil {
		t.Errorf("Auto off checks nothing and tells the page nothing: %v", err)
	}
	if p := ok.autoPublic(); p == nil || p.Default != "new" || p.Small != "gpt-6-luna" {
		t.Errorf("public: %+v", p)
	}
	if (GenieSettings{AutoOn: true, AutoSmall: "a", AutoStrong: "b", AutoDefault: "all"}).autoPublic().Default != "all" {
		t.Error("all is kept")
	}
}

func TestTheHealthCardCountsHowAutoDid(t *testing.T) {
	rows := []healthRow{{Model: "s", Name: "Small", Calls: 40, Errors: 2, ErrPct: 5}, {Model: "b", Name: "Big", Calls: 10}}
	h := autoHealthOf(&autoPublic{Small: "s", Strong: "b"}, rows, 4)
	if h.Small.Calls != 40 || h.Strong.Name != "Big" || h.Stronger != 4 || h.AskedPct != 10 {
		t.Errorf("auto health: %+v", h)
	}
	if autoHealthOf(nil, rows, 3) != nil {
		t.Error("nothing when Auto is off")
	}
	if h := autoHealthOf(&autoPublic{Small: "x", Strong: "y"}, rows, 0); h.Small.Name != "x" || h.AskedPct != 0 {
		t.Errorf("a model with no calls: %+v", h)
	}
}

func TestAnAutoStrongerEventIsKept(t *testing.T) {
	e, ok := eventFrom(map[string]interface{}{"event": "auto_stronger", "model": "gpt-6-luna", "sid": "abc123XYZ"}, time.Now())
	if !ok || e.Event != "auto_stronger" {
		t.Fatalf("event: %+v %v", e, ok)
	}
}
