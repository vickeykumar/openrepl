package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type chatsEnv struct {
	s    *Server
	c    *http.Cookie
	home string
}

func chatsSetup(t *testing.T, g GenieSettings) *chatsEnv {
	t.Helper()
	s, _ := adminTestServer(t)
	if err := SaveSiteSettings(SiteSettings{Genie: g}); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, "uid-ann", "ann@example.com")
	probe := httptest.NewRequest("POST", "/genie_chats", nil)
	probe.AddCookie(c)
	_, home := s.requestIdentity(httptest.NewRecorder(), probe)
	if home == "" {
		t.Skip("no workspace for the test user")
	}
	os.MkdirAll(home, 0o755)
	os.RemoveAll(filepath.Join(home, chatsDirName))
	chatsRateMu.Lock()
	chatsRate = map[string][]time.Time{}
	chatsRateMu.Unlock()
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, chatsDirName)) })
	return &chatsEnv{s: s, c: c, home: home}
}

func (e *chatsEnv) call(t *testing.T, body string, c *http.Cookie, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/genie_chats"+query, strings.NewReader(body))
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.s.handleGenieChats(w, req)
	var out map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *chatsEnv) ok(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	code, out := e.call(t, body, e.c, "")
	if code != 200 {
		t.Fatalf("%s: %d %v", body, code, out)
	}
	return out
}

const metaLine = `{"t":"meta","id":"k3x9","created":"2026-11-17T14:52:00+05:30","context":"chat"}`

func TestAChatIsStartedWithItsMetaAndGrowsByLines(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	st := e.ok(t, `{"op":"state"}`)
	if st["enabled"] != true || len(st["chats"].([]interface{})) != 0 || len(st["open"].([]interface{})) != 0 {
		t.Fatalf("an empty workspace: %v", st)
	}
	// a new chat must start with its meta line
	if code, _ := e.call(t, `{"op":"append","id":"k3x9","lines":[{"t":"user","text":"hi"}]}`, e.c, ""); code != http.StatusBadRequest {
		t.Errorf("a chat without a meta: %d", code)
	}
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"title","text":"loop prints 0"},{"t":"user","at":"14:52","text":"why does my loop print 0","file":"main.py","language":"Python","ts":1}]}`)
	e.ok(t, `{"op":"append","id":"k3x9","lines":[{"t":"answer","at":"14:52","model":"gpt-4o-mini","tokens":412,"text":"The variable is set again","ts":2},{"t":"task","at":"15:10","text":"Add checks","steps":6,"checked":"pytest -q","changed":"auth.py +9"}]}`)
	got := e.ok(t, `{"op":"read","id":"k3x9"}`)
	lines := got["lines"].([]interface{})
	if len(lines) != 5 || lines[3].(map[string]interface{})["t"] != "answer" || lines[3].(map[string]interface{})["tokens"].(float64) != 412 {
		t.Fatalf("the lines: %v", lines)
	}
	// it is one file in the hidden folder, a line for each thing
	b, err := os.ReadFile(filepath.Join(e.home, ".genie", "chats", "k3x9.jsonl"))
	if err != nil || strings.Count(string(b), "\n") != 5 {
		t.Fatalf("the file: %q %v", b, err)
	}
	st = e.ok(t, `{"op":"state"}`)
	chats := st["chats"].([]interface{})
	c := chats[0].(map[string]interface{})
	if len(chats) != 1 || c["id"] != "k3x9" || c["title"] != "loop prints 0" || c["messages"].(float64) != 2 || c["language"] != "Python" || c["context"] != "chat" {
		t.Errorf("the summary: %v", c)
	}
}

func TestOnlyAllowedLinesAndKeysAreKept(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"user","text":"x","evil":"<script>","nested":{"a":1}}]}`)
	b, _ := os.ReadFile(filepath.Join(e.home, ".genie", "chats", "k3x9.jsonl"))
	if strings.Contains(string(b), "evil") || strings.Contains(string(b), "nested") {
		t.Errorf("a key that is not allowed was kept: %s", b)
	}
	for name, body := range map[string]string{
		"an unknown kind":   `{"op":"append","id":"k3x9","lines":[{"t":"script","text":"x"}]}`,
		"a bad id":          `{"op":"append","id":"../x","lines":[` + metaLine + `]}`,
		"a meta of another": `{"op":"append","id":"zzzz","lines":[` + metaLine + `]}`,
		"no lines":          `{"op":"append","id":"k3x9","lines":[]}`,
		"too many lines":    `{"op":"append","id":"k3x9","lines":[` + strings.Repeat(`{"t":"user","text":"x"},`, 21) + `{"t":"user","text":"x"}]}`,
		"a huge message":    `{"op":"append","id":"k3x9","lines":[{"t":"user","text":"` + strings.Repeat("x", maxChatLineChars+1) + `"}]}`,
		"a zero byte":       `{"op":"append","id":"k3x9","lines":[{"t":"user","text":"a\u0000b"}]}`,
		"not json":          `{"op":`,
		"another op":        `{"op":"format"}`,
		"read a bad id":     `{"op":"read","id":"../../etc/passwd"}`,
		"read a missing":    `{"op":"read","id":"nope1"}`,
	} {
		code, _ := e.call(t, body, e.c, "")
		want := http.StatusBadRequest
		if name == "read a missing" {
			want = http.StatusNotFound
		}
		if code != want {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestALineCutShortByACrashIsSkipped(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"user","text":"one"}]}`)
	f, _ := os.OpenFile(filepath.Join(e.home, ".genie", "chats", "k3x9.jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"t":"answer","text":"cut sho`)
	f.Close()
	got := e.ok(t, `{"op":"read","id":"k3x9"}`)
	if n := len(got["lines"].([]interface{})); n != 2 {
		t.Errorf("the lines that can be read: %d", n)
	}
}

func TestTheOpenTabsAreKeptInOrderAndLimitedAndDroppedWhenTheChatIsGone(t *testing.T) {
	e := chatsSetup(t, GenieSettings{ChatHistoryTabs: 2})
	for _, id := range []string{"aaaa", "bbbb", "cccc"} {
		e.ok(t, fmt.Sprintf(`{"op":"append","id":"%s","lines":[{"t":"meta","id":"%s"},{"t":"user","text":"hi"}]}`, id, id))
	}
	out := e.ok(t, `{"op":"tabs","open":["cccc","aaaa","bbbb","aaaa","../x"]}`)
	if got := fmt.Sprint(out["open"]); got != "[cccc aaaa]" {
		t.Errorf("the tabs written: %s", got)
	}
	if st := e.ok(t, `{"op":"state"}`); fmt.Sprint(st["open"]) != "[cccc aaaa]" {
		t.Errorf("the tabs read: %v", st["open"])
	}
	// a chat that is deleted is not a tab any more
	e.ok(t, `{"op":"delete","id":"cccc"}`)
	if st := e.ok(t, `{"op":"state"}`); fmt.Sprint(st["open"]) != "[aaaa]" {
		t.Errorf("after a delete: %v", st["open"])
	}
	// a tab whose file is gone is dropped when read
	os.Remove(filepath.Join(e.home, ".genie", "chats", "aaaa.jsonl"))
	if st := e.ok(t, `{"op":"state"}`); fmt.Sprint(st["open"]) != "[]" {
		t.Errorf("a missing file: %v", st["open"])
	}
}

func TestAChatOverItsSizeLosesItsOldestMessagesAndKeepsItsHead(t *testing.T) {
	e := chatsSetup(t, GenieSettings{ChatHistoryKB: 20})
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"title","text":"big"}]}`)
	var last map[string]interface{}
	for i := 0; i < 12; i++ {
		last = e.ok(t, fmt.Sprintf(`{"op":"append","id":"k3x9","lines":[{"t":"user","text":"m%d %s"}]}`, i, strings.Repeat("x", 3000)))
	}
	if last["trimmed"] != true && last["size"].(float64) > 20*1024 {
		t.Errorf("not trimmed: %v", last)
	}
	b, _ := os.ReadFile(filepath.Join(e.home, ".genie", "chats", "k3x9.jsonl"))
	if len(b) > 20*1024 {
		t.Errorf("the file is %d bytes", len(b))
	}
	got := e.ok(t, `{"op":"read","id":"k3x9"}`)["lines"].([]interface{})
	if got[0].(map[string]interface{})["t"] != "meta" || got[1].(map[string]interface{})["t"] != "title" {
		t.Errorf("the head was lost: %v", got[:2])
	}
	if text := got[len(got)-1].(map[string]interface{})["text"].(string); !strings.HasPrefix(text, "m11 ") {
		t.Errorf("the newest message was lost: %.20s", text)
	}
}

func TestTheOldestClosedChatsGoWhenThereAreTooManyAndAnOpenOneNever(t *testing.T) {
	e := chatsSetup(t, GenieSettings{ChatHistoryChats: 3})
	ids := []string{"aaaa", "bbbb", "cccc", "dddd"}
	for i, id := range ids {
		e.ok(t, fmt.Sprintf(`{"op":"append","id":"%s","lines":[{"t":"meta","id":"%s"},{"t":"user","text":"hi"}]}`, id, id))
		p := filepath.Join(e.home, ".genie", "chats", id+".jsonl")
		os.Chtimes(p, time.Now(), time.Now().Add(time.Duration(i-10)*time.Hour)) // aaaa is the oldest
		if id == "aaaa" {
			e.ok(t, `{"op":"tabs","open":["aaaa"]}`) // and it is open
		}
	}
	e.ok(t, `{"op":"append","id":"eeee","lines":[{"t":"meta","id":"eeee"},{"t":"user","text":"new"}]}`)
	have := map[string]bool{}
	for _, c := range e.ok(t, `{"op":"state"}`)["chats"].([]interface{}) {
		have[c.(map[string]interface{})["id"].(string)] = true
	}
	if !have["aaaa"] || !have["eeee"] || have["bbbb"] {
		t.Errorf("the chats left: %v", have)
	}
}

func TestChatsPastTheirDaysGoUnlessOpen(t *testing.T) {
	e := chatsSetup(t, GenieSettings{ChatHistoryDays: 30})
	for _, id := range []string{"aaaa", "bbbb"} {
		e.ok(t, fmt.Sprintf(`{"op":"append","id":"%s","lines":[{"t":"meta","id":"%s"},{"t":"user","text":"hi"}]}`, id, id))
		old := time.Now().Add(-40 * 24 * time.Hour)
		os.Chtimes(filepath.Join(e.home, ".genie", "chats", id+".jsonl"), old, old)
	}
	e.ok(t, `{"op":"tabs","open":["bbbb"]}`)
	have := map[string]bool{}
	for _, c := range e.ok(t, `{"op":"state"}`)["chats"].([]interface{}) {
		have[c.(map[string]interface{})["id"].(string)] = true
	}
	if have["aaaa"] || !have["bbbb"] {
		t.Errorf("after 30 days: %v", have)
	}
}

func TestDeleteExportAndSwitchingSavingOff(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"title","text":"loop prints 0"},{"t":"user","at":"14:52","text":"why 0"},{"t":"answer","at":"14:52","model":"m","text":"because"},{"t":"task","at":"15:10","text":"Add checks","steps":6,"checked":"pytest -q","changed":"auth.py +9"}]}`)
	md := e.ok(t, `{"op":"export","id":"k3x9"}`)["markdown"].(string)
	for _, want := range []string{"# loop prints 0", "**You** 14:52", "why 0", "**Genie** 14:52 m", "because", "Agent task", "6 steps", "pytest -q"} {
		if !strings.Contains(md, want) {
			t.Errorf("the Markdown lacks %q: %s", want, md)
		}
	}
	if all := e.ok(t, `{"op":"exportAll"}`)["markdown"].(string); !strings.Contains(all, "loop prints 0") {
		t.Errorf("exportAll: %s", all)
	}
	// saving off: nothing more is written, what is there stays
	e.ok(t, `{"op":"off","off":true}`)
	if code, out := e.call(t, `{"op":"append","id":"k3x9","lines":[{"t":"user","text":"more"}]}`, e.c, ""); code != http.StatusConflict {
		t.Errorf("append while off: %d %v", code, out)
	}
	if st := e.ok(t, `{"op":"state"}`); st["off"] != true || len(st["chats"].([]interface{})) != 1 {
		t.Errorf("off: %v", st)
	}
	e.ok(t, `{"op":"off","off":false}`)
	e.ok(t, `{"op":"delete","id":"k3x9"}`)
	if st := e.ok(t, `{"op":"state"}`); len(st["chats"].([]interface{})) != 0 {
		t.Errorf("after delete: %v", st["chats"])
	}
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`]}`)
	e.ok(t, `{"op":"deleteAll"}`)
	if st := e.ok(t, `{"op":"state"}`); len(st["chats"].([]interface{})) != 0 {
		t.Errorf("after deleteAll: %v", st["chats"])
	}
}

func TestAPersonIsLimitedToSomeCallsAMinute(t *testing.T) {
	now := time.Now()
	for i := 0; i < chatsPerMinute; i++ {
		if !chatsAllowed("rate-user", now) {
			t.Fatalf("refused call %d", i)
		}
	}
	if chatsAllowed("rate-user", now) || !chatsAllowed("other", now) || !chatsAllowed("rate-user", now.Add(61*time.Second)) {
		t.Error("the limit of a minute")
	}
}

func TestWhoMayReachTheChats(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	// a guest has a workspace too, unless the admin keeps it to signed-in users
	if code, _ := e.call(t, `{"op":"state"}`, nil, ""); code != 200 {
		t.Errorf("a guest: %d", code)
	}
	// a viewer of a shared session names the owner's workspace: refused, and nothing is read
	if code, out := e.call(t, `{"op":"state"}`, e.c, "?jid=abc"); code != http.StatusForbidden || out["error"].(map[string]interface{})["code"] != "not_owner" {
		t.Errorf("a viewer: %d %v", code, out)
	}
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{ChatHistoryNoGuests: true}})
	if code, out := e.call(t, `{"op":"state"}`, nil, ""); code != http.StatusForbidden || out["error"].(map[string]interface{})["code"] != "guests_off" {
		t.Errorf("a guest when guests are off: %d %v", code, out)
	}
	if code, _ := e.call(t, `{"op":"state"}`, e.c, ""); code != 200 {
		t.Errorf("a signed-in user when guests are off: %d", code)
	}
	SaveSiteSettings(SiteSettings{Genie: GenieSettings{ChatHistoryOff: true}})
	if code, out := e.call(t, `{"op":"state"}`, e.c, ""); code != http.StatusForbidden || out["error"].(map[string]interface{})["code"] != "off" {
		t.Errorf("switched off: %d %v", code, out)
	}
	if req := httptest.NewRequest("GET", "/genie_chats", nil); true {
		w := httptest.NewRecorder()
		e.s.handleGenieChats(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET: %d", w.Code)
		}
	}
}

func TestTheChatFolderIsNeverALinkOutOfTheWorkspace(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(e.home, ".genie")); err != nil {
		t.Skip("no symbolic links here")
	}
	code, _ := e.call(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`]}`, e.c, "")
	if code == 200 {
		t.Error("a chat was written through a link out of the workspace")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was written outside: %v", entries)
	}
}

func TestTheChatHistorySettingsAreCheckedSavedAndShown(t *testing.T) {
	_, h := adminTestServer(t)
	body := `{"colorOfTheDay":false,"announcement":{"text":"","level":"info"},"maintenance":{"enabled":false,"message":""},"disabledLanguages":[],"genie":{"disabled":false,"chatHistoryNoGuests":true,"chatHistoryChats":30,"chatHistoryTabs":6,"chatHistoryKB":100,"chatHistoryDays":90}}`
	if w := post(h, "/admin/settings", body); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	g := GetSiteSettings().Genie
	if !g.ChatHistoryNoGuests || g.chatsMax() != 30 || g.chatsTabs() != 6 || g.chatsBytes() != 100*1024 || g.ChatHistoryDays != 90 {
		t.Errorf("in use: %+v", g)
	}
	for was, bad := range map[string]string{`"chatHistoryChats":30`: `"chatHistoryChats":500`, `"chatHistoryTabs":6`: `"chatHistoryTabs":99`, `"chatHistoryKB":100`: `"chatHistoryKB":5`, `"chatHistoryDays":90`: `"chatHistoryDays":-1`} {
		if w := post(h, "/admin/settings", strings.Replace(body, was, bad, 1)); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	pub := httptest.NewRecorder()
	handleSettingsJS(pub, httptest.NewRequest("GET", "/settings.js", nil))
	if !strings.Contains(pub.Body.String(), `"chatHistory":true`) || !strings.Contains(pub.Body.String(), `"chatHistoryGuests":false`) || !strings.Contains(pub.Body.String(), `"chatHistoryTabs":6`) {
		t.Errorf("settings.js: %s", pub.Body.String())
	}
}

func TestAsciiJSON(t *testing.T) {
	in := map[string]interface{}{"text": "café — \U0001F600 \"q\" <b>"}
	b := asciiJSON(in)
	for _, c := range b {
		if c >= 0x80 {
			t.Fatalf("not ascii: %s", b)
		}
	}
	var back map[string]interface{}
	if err := json.Unmarshal(b, &back); err != nil || back["text"] != in["text"] {
		t.Fatalf("round trip: %v %v %s", err, back, b)
	}
}

// A running agent task writes a checkpoint after each step (the page's Resume bar reads the last one): the file keeps one, and it
// is the newest, so that a task of many steps does not grow the chat and nothing else in it moves.
func TestACheckpointReplacesTheOneBeforeItAndTheRestOfTheChatStaysAsItWas(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"user","text":"build the shop","ts":1},{"t":"checkpoint","text":"build the shop","steps":0,"outcome":"running","ts":2}]}`)
	e.ok(t, `{"op":"append","id":"k3x9","lines":[{"t":"answer","text":"On it","ts":3},{"t":"checkpoint","text":"build the shop","steps":1,"changed":"shop/index.html","file":"shop/index.html","outcome":"running","ts":4,"evil":"x"}]}`)
	e.ok(t, `{"op":"append","id":"k3x9","lines":[{"t":"checkpoint","text":"build the shop","steps":2,"outcome":"running","ts":5}]}`)
	b, _ := os.ReadFile(filepath.Join(e.home, ".genie", "chats", "k3x9.jsonl"))
	if n := strings.Count(string(b), `"t":"checkpoint"`); n != 1 {
		t.Fatalf("%d checkpoints: %s", n, b)
	}
	lines := e.ok(t, `{"op":"read","id":"k3x9"}`)["lines"].([]interface{})
	kinds := []string{}
	for _, l := range lines {
		kinds = append(kinds, l.(map[string]interface{})["t"].(string))
	}
	if strings.Join(kinds, ",") != "meta,user,answer,checkpoint" {
		t.Errorf("the lines are %v", kinds)
	}
	last := lines[3].(map[string]interface{})
	if last["steps"].(float64) != 2 || last["outcome"] != "running" {
		t.Errorf("the checkpoint is not the newest: %v", last)
	}
	if strings.Contains(string(b), "evil") {
		t.Error("a key that is not allowed was kept")
	}
	// a checkpoint is not a message: the list counts the user's words and the answers only
	c := e.ok(t, `{"op":"state"}`)["chats"].([]interface{})[0].(map[string]interface{})
	if c["messages"].(float64) != 2 {
		t.Errorf("messages: %v", c["messages"])
	}
	// the end of the task replaces it with its outcome
	e.ok(t, `{"op":"append","id":"k3x9","lines":[{"t":"checkpoint","text":"build the shop","steps":4,"outcome":"done","ts":6}]}`)
	lines = e.ok(t, `{"op":"read","id":"k3x9"}`)["lines"].([]interface{})
	if got := lines[len(lines)-1].(map[string]interface{}); got["outcome"] != "done" || len(lines) != 4 {
		t.Errorf("after the end: %v", lines)
	}
	// a checkpoint may not start a chat, and the Markdown of the chat does not hold it
	if code, _ := e.call(t, `{"op":"append","id":"zz99","lines":[{"t":"checkpoint","text":"x"}]}`, e.c, ""); code != http.StatusBadRequest {
		t.Errorf("a chat that starts with a checkpoint: %d", code)
	}
	md := e.ok(t, `{"op":"export","id":"k3x9"}`)["markdown"].(string)
	if strings.Contains(md, "running") || strings.Contains(md, "checkpoint") {
		t.Errorf("the export holds the checkpoint: %s", md)
	}
}

func TestCheckpointsSentTogetherKeepOnlyTheLast(t *testing.T) {
	e := chatsSetup(t, GenieSettings{})
	e.ok(t, `{"op":"append","id":"k3x9","lines":[`+metaLine+`,{"t":"user","text":"go","ts":1}]}`)
	e.ok(t, `{"op":"append","id":"k3x9","lines":[{"t":"checkpoint","text":"go","steps":1,"outcome":"running","ts":2},{"t":"answer","text":"ok","ts":3},{"t":"checkpoint","text":"go","steps":2,"outcome":"running","ts":4}]}`)
	b, _ := os.ReadFile(filepath.Join(e.home, ".genie", "chats", "k3x9.jsonl"))
	if n := strings.Count(string(b), `"t":"checkpoint"`); n != 1 || !strings.Contains(string(b), `"steps":2`) || !strings.Contains(string(b), `"t":"answer"`) {
		t.Errorf("the file: %s", b)
	}
}
