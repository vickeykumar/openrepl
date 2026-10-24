package server

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"persist"
)

func freshTokens(t *testing.T) {
	t.Helper()
	old := tokenTotals
	tokenTotals = newTokenLedger()
	t.Cleanup(func() { tokenTotals = old })
}

func TestUsageIsReadFromAWholeAnswerAndFromAStream(t *testing.T) {
	whole := `{"id":"x","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":310,"completion_tokens":930,"total_tokens":1240,"cost":0.00123}}`
	u, ok := extractUsage([]byte(whole))
	if !ok || u.In != 310 || u.Out != 930 || !u.HasCost || u.Cost != 0.00123 {
		t.Fatalf("whole: %+v %v", u, ok)
	}
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"h\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":34}}\n\n" +
		"data: [DONE]\n\n"
	u, ok = extractUsage([]byte(stream))
	if !ok || u.In != 12 || u.Out != 34 || u.HasCost {
		t.Fatalf("stream: %+v %v", u, ok)
	}
	for name, body := range map[string]string{
		"no usage":         `{"choices":[{"message":{"content":"hi"}}]}`,
		"a stream without": "data: {\"choices\":[]}\n\ndata: [DONE]\n\n",
		"not json":         "hello",
		"empty":            "",
	} {
		if u, ok := extractUsage([]byte(body)); ok {
			t.Errorf("%s: found %+v", name, u)
		}
	}
}

func TestTheTapPassesEverythingOnAndKeepsACopyUpToItsLimit(t *testing.T) {
	tap := &bodyTap{r: strings.NewReader("0123456789"), limit: 20}
	got, _ := io.ReadAll(tap)
	if string(got) != "0123456789" || tap.over || tap.buf.String() != "0123456789" {
		t.Fatalf("small: %q over=%v copy=%q", got, tap.over, tap.buf.String())
	}
	tap = &bodyTap{r: strings.NewReader(strings.Repeat("x", 50)), limit: 20}
	got, _ = io.ReadAll(tap)
	if len(got) != 50 || !tap.over || tap.buf.Len() != 0 {
		t.Fatalf("large: passed %d, over=%v, copy %d", len(got), tap.over, tap.buf.Len())
	}
}

func TestTheMonthStartsAgainAndAllTimeDoesNot(t *testing.T) {
	var r tokenRecord
	oct := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	r.add("m", usageCount{In: 100, Out: 50}, oct)
	r.add("m", usageCount{In: 10, Out: 5, Cost: 0.5, HasCost: true}, oct)
	if got := r.sum(true); got != (totals{110, 55, 2}) {
		t.Fatalf("october: %+v", got)
	}
	// looking in November: the month is empty, all time is not, and nothing was changed
	seen := r.forMonth("2026-11")
	if seen.sum(true) != (totals{}) || seen.sum(false) != (totals{110, 55, 2}) || r.Month != "2026-10" {
		t.Fatalf("november view: %+v %+v", seen.sum(true), seen.sum(false))
	}
	r.add("m", usageCount{In: 1, Out: 2}, nov)
	if r.Month != "2026-11" || r.sum(true) != (totals{1, 2, 1}) || r.sum(false) != (totals{111, 57, 3}) {
		t.Fatalf("after november: %+v %+v %+v", r.Month, r.sum(true), r.sum(false))
	}
	if r.Models["m"].MCost != 0 || r.Models["m"].ACost != 0.5 {
		t.Errorf("the reported cost: %+v", r.Models["m"])
	}
}

func TestCostIsTheReportedOneOrTheTablesPrice(t *testing.T) {
	priced := modelTokens{MIn: 2_000_000, MOut: 500_000, MAns: 10, AIn: 4_000_000, AOut: 1_000_000, AAns: 20}
	price := modelPrice{In: 2, Out: 8}
	if c, partial := priced.cost(true, price); c != 8 || partial {
		t.Errorf("month: %v %v", c, partial)
	}
	if c, partial := priced.cost(false, price); c != 16 || partial {
		t.Errorf("all time: %v %v", c, partial)
	}
	if c, partial := priced.cost(true, modelPrice{}); c != 0 || !partial {
		t.Errorf("no price: %v %v", c, partial)
	}
	reported := modelTokens{MIn: 10, MOut: 10, MAns: 2, MCost: 0.19, MCostAns: 2}
	if c, partial := reported.cost(true, modelPrice{}); c != 0.19 || partial {
		t.Errorf("reported: %v %v", c, partial)
	}
	if c, partial := (modelTokens{}).cost(true, modelPrice{}); c != 0 || partial {
		t.Errorf("nothing used is not partial: %v %v", c, partial)
	}
	rec := tokenRecord{Models: map[string]*modelTokens{"a": &priced, "b": &reported}}
	if c, partial := rec.cost(true, map[string]modelPrice{"a": price}); c < 8.18 || c > 8.20 || partial {
		t.Errorf("record: %v %v", c, partial)
	}
}

func TestAVisitIsCountedUnderItsIdAndForgotten(t *testing.T) {
	freshTokens(t)
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	tokenTotals.now = func() time.Time { return now }
	tokenTotals.add(tokensVisitorsKey, "v1", "m", usageCount{In: 10, Out: 5})
	tokenTotals.add(tokensVisitorsKey, "v1", "m", usageCount{In: 1, Out: 1})
	tokenTotals.add(tokensVisitorsKey, "v2", "m", usageCount{In: 100, Out: 50})
	if got := tokenTotals.visit("v1"); got != (totals{11, 6, 2}) {
		t.Errorf("v1: %+v", got)
	}
	if got := tokenTotals.visit("nobody"); got != (totals{}) || tokenTotals.visit("") != (totals{}) {
		t.Errorf("a visit that was not counted: %+v", got)
	}
	// the visitors are counted together
	if got := tokenTotals.visitors().sum(false); got != (totals{111, 56, 3}) {
		t.Errorf("visitors: %+v", got)
	}
	// a day later the visit is forgotten when another starts, the visitors' total stays
	now = now.Add(25 * time.Hour)
	tokenTotals.add(tokensVisitorsKey, "v3", "m", usageCount{In: 1})
	if got := tokenTotals.visit("v1"); got != (totals{}) {
		t.Errorf("an old visit was kept: %+v", got)
	}
	if got := tokenTotals.visitors().sum(false); got.In != 112 {
		t.Errorf("visitors after a day: %+v", got)
	}
	// an answer without a count adds nothing
	before := tokenTotals.visitors().sum(false)
	tokenTotals.add(tokensVisitorsKey, "v3", "m", usageCount{})
	if tokenTotals.visitors().sum(false) != before {
		t.Error("an empty usage was counted")
	}
}

func TestTotalsSurviveARestartAndAreListed(t *testing.T) {
	freshTokens(t)
	db, err := persist.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	old := tokens_db_handle
	tokens_db_handle = db
	t.Cleanup(func() { tokens_db_handle = old; db.Close() })

	tokenTotals.add(tokensUserPrefix+"uid-ann", "", "m", usageCount{In: 100, Out: 40})
	tokenTotals.add(tokensUserPrefix+"uid-bob", "", "m", usageCount{In: 7, Out: 3})
	// the server starts again: nothing in memory, the database has it
	tokenTotals = newTokenLedger()
	if got := tokenTotals.user("uid-ann").sum(false); got != (totals{100, 40, 1}) {
		t.Fatalf("ann after a restart: %+v", got)
	}
	every := tokenTotals.every()
	if len(every) != 2 || every["uid-bob"].sum(true).In != 7 {
		t.Fatalf("listed: %+v", every)
	}
}

func TestAStreamAsksForUsageAndOpenRouterForTheCost(t *testing.T) {
	adminTestServer(t)
	ask := func(body string) map[string]interface{} {
		out, _, err := sanitizeChatBody([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]interface{}
		json.Unmarshal(out, &got)
		return got
	}
	got := ask(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if so, _ := got["stream_options"].(map[string]interface{}); so["include_usage"] != true {
		t.Errorf("a stream does not ask for usage: %v", got)
	}
	if got = ask(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`); got["stream_options"] != nil {
		t.Errorf("a whole answer asks for stream options: %v", got)
	}
	got = ask(`{"model":"google/gemma-4-31b-it","messages":[{"role":"user","content":"hi"}]}`)
	if u, _ := got["usage"].(map[string]interface{}); u["include"] != true || got["stream_options"] != nil {
		t.Errorf("OpenRouter: %v", got)
	}
}

func TestAnAnswerIsCountedForTheUserOrForTheVisitor(t *testing.T) {
	freshTokens(t)
	c, _ := agentSetup(t, GenieSettings{})
	url, _, _ := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":300,"completion_tokens":100}}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })
	const chat = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`

	// a signed-in user
	w := agentCall(t, chat, c, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"hi"`) {
		t.Fatalf("the answer was changed: %d %s", w.Code, w.Body.String())
	}
	agentCall(t, chat, c, "")
	if got := tokenTotals.user("uid-ann").sum(true); got != (totals{600, 200, 2}) {
		t.Fatalf("the user's month: %+v", got)
	}
	u := usageCall(t, c)
	if u.Tokens == nil || u.Tokens.Month == nil || u.Tokens.Month.In != 600 || u.Tokens.All.Out != 200 || u.Tokens.Visit != nil || u.Tokens.Cost != nil {
		t.Fatalf("the user's panel: %+v", u.Tokens)
	}

	// a visitor: the first answer gives the session an anonymous id, the next ones count under it
	w = agentCall(t, chat, nil, "")
	var gid *http.Cookie
	for _, ck := range w.Result().Cookies() {
		gid = ck
	}
	if gid == nil {
		t.Fatal("the visitor was not given a session cookie")
	}
	agentCall(t, chat, gid, "")
	if got := tokenTotals.visitors().sum(false); got.Answers != 2 || got.In != 600 {
		t.Fatalf("visitors: %+v", got)
	}
	v := usageCall(t, gid)
	if v.Tokens == nil || v.Tokens.Visit == nil || v.Tokens.Visit.Answers < 1 || v.Tokens.Month != nil {
		t.Fatalf("the visitor's panel: %+v", v.Tokens)
	}
	// a refused answer costs nothing
	url2, _, _ := fakeUpstream(t, 400, `{"error":{"message":"bad"},"usage":{"prompt_tokens":9,"completion_tokens":9}}`)
	openaiEndpoint = url2
	agentCall(t, chat, c, "")
	if got := tokenTotals.user("uid-ann").sum(true); got.Answers != 2 {
		t.Errorf("a refused request was counted: %+v", got)
	}
}

func TestTheCostShowsToUsersOnlyWhenAnAdminAllowsIt(t *testing.T) {
	freshTokens(t)
	c, _ := agentSetup(t, GenieSettings{ShowCostToUsers: true, ModelPrices: map[string]modelPrice{modelMini: {In: 0.15, Out: 0.6}}})
	tokenTotals.add(tokensUserPrefix+"uid-ann", "", modelMini, usageCount{In: 1_000_000, Out: 1_000_000})
	u := usageCall(t, c)
	if u.Tokens == nil || u.Tokens.Cost == nil || u.Tokens.Cost.Month < 0.74 || u.Tokens.Cost.Month > 0.76 || u.Tokens.Cost.Partial {
		t.Fatalf("with the switch on: %+v", u.Tokens)
	}
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{}}); err != nil {
		t.Fatal(err)
	}
	if u := usageCall(t, c); u.Tokens == nil || u.Tokens.Cost != nil {
		t.Fatalf("with the switch off: %+v", u.Tokens)
	}
}

func TestThePricesAreChecked(t *testing.T) {
	for name, g := range map[string]GenieSettings{
		"a model that is not one": {ModelPrices: map[string]modelPrice{"gpt-9": {In: 1}}},
		"negative":                {ModelPrices: map[string]modelPrice{modelMini: {In: -1}}},
		"too much":                {ModelPrices: map[string]modelPrice{modelMini: {Out: 1e9}}},
	} {
		if _, err := (SiteSettings{Genie: g}).normalize(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	s, err := SiteSettings{Genie: GenieSettings{ModelPrices: map[string]modelPrice{modelMini: {In: 0.15, Out: 0.6}, modelLuna: {}}}}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Genie.ModelPrices) != 1 || s.Genie.ModelPrices[modelMini].Out != 0.6 {
		t.Errorf("an empty price was kept: %v", s.Genie.ModelPrices)
	}
}

func TestTheDashboardListsUsageAndDownloadsIt(t *testing.T) {
	freshTokens(t)
	_, h := adminTestServer(t)
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{ModelPrices: map[string]modelPrice{modelMini: {In: 1, Out: 2}}}}); err != nil {
		t.Fatal(err)
	}
	tokenTotals.add(tokensUserPrefix+"uid-ann", "", modelMini, usageCount{In: 2_000_000, Out: 1_000_000})
	tokenTotals.add(tokensUserPrefix+"uid-ann", "", modelGemma, usageCount{In: 10, Out: 5, Cost: 0.19, HasCost: true})
	tokenTotals.add(tokensUserPrefix+"uid-bob", "", modelMini, usageCount{In: 1000, Out: 1000})
	tokenTotals.add(tokensVisitorsKey, "v1", modelMini, usageCount{In: 500_000, Out: 0})

	var r genieUsageReply
	decode(t, get(h, "/admin/genie-usage"), &r)
	if r.Range != "month" || len(r.Users) != 2 || r.Users[0].UID != "uid-ann" || !r.PricesSet {
		t.Fatalf("reply: %+v", r)
	}
	ann := r.Users[0]
	if ann.Answers != 2 || len(ann.Models) != 2 || ann.Models[0].ID != modelMini || ann.Cost < 4.18 || ann.Cost > 4.20 || ann.Partial {
		t.Fatalf("ann: %+v", ann)
	}
	if r.Visitors.Answers != 1 || r.Visitors.In != 500_000 || r.Total.Answers != 4 {
		t.Fatalf("visitors %+v total %+v", r.Visitors, r.Total)
	}
	var all genieUsageReply
	decode(t, get(h, "/admin/genie-usage?range=all"), &all)
	if all.Range != "all" || len(all.Users) != 2 {
		t.Fatalf("all: %+v", all)
	}

	w := get(h, "/admin/genie-usage?format=csv")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || !strings.Contains(w.Header().Get("Content-Disposition"), "genie-usage-month.csv") {
		t.Fatalf("csv: %d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	if !strings.HasPrefix(body, "user,email,model,answers,tokens_sent,tokens_written,cost_usd,cost_is_partial") || !strings.Contains(body, "Visitors,,") || !strings.Contains(body, "uid-ann") {
		t.Errorf("csv body:\n%s", body)
	}
	denied := adminCall{method: "POST", path: "/admin/genie-usage", admin: true, header: true}.do(h)
	if denied.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", denied.Code)
	}
}
