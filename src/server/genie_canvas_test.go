package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type canvasEnv struct {
	s    *Server
	c    *http.Cookie
	home string
}

func canvasSetup(t *testing.T, site SiteSettings) *canvasEnv {
	t.Helper()
	s, _ := adminTestServer(t)
	if err := SaveSiteSettings(site); err != nil {
		t.Fatal(err)
	}
	c := sessionCookie(t, "uid-ann", "ann@example.com")
	probe := httptest.NewRequest("POST", "/genie_canvas", nil)
	probe.AddCookie(c)
	_, home := s.requestIdentity(httptest.NewRecorder(), probe)
	if home == "" {
		t.Skip("no workspace for the test user")
	}
	os.MkdirAll(home, 0o755)
	os.RemoveAll(filepath.Join(home, canvasDirName))
	chatsRateMu.Lock()
	chatsRate = map[string][]time.Time{}
	chatsRateMu.Unlock()
	t.Cleanup(func() { os.RemoveAll(filepath.Join(home, canvasDirName)) })
	return &canvasEnv{s: s, c: c, home: home}
}

func (e *canvasEnv) call(t *testing.T, body string, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/genie_canvas"+query, strings.NewReader(body))
	req.AddCookie(e.c)
	w := httptest.NewRecorder()
	e.s.handleGenieCanvas(w, req)
	var out map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *canvasEnv) ok(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	code, out := e.call(t, body, "")
	if code != 200 {
		t.Fatalf("%s: %d %v", body, code, out)
	}
	return out
}

func boardsOf(st map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	for _, b := range st["boards"].([]interface{}) {
		out = append(out, b.(map[string]interface{}))
	}
	return out
}

func TestABoardIsDrawnPlacedAndReadBack(t *testing.T) {
	e := canvasSetup(t, SiteSettings{})
	st := e.ok(t, `{"op":"state"}`)
	if len(boardsOf(st)) != 0 {
		t.Fatalf("a new canvas has no board: %v", st)
	}
	e.ok(t, `{"op":"put","id":"pricing","title":"Pricing","html":"<h1>Pricing</h1>","by":"genie"}`)
	st = e.ok(t, `{"op":"put","id":"about","title":"About","html":"<h1>About</h1>","by":"genie"}`)
	bs := boardsOf(st)
	if len(bs) != 2 {
		t.Fatalf("two boards: %v", st)
	}
	a, b := bs[0], bs[1]
	if a["x"] == b["x"] && a["y"] == b["y"] {
		t.Errorf("two boards were put on the same spot: %v %v", a, b)
	}
	if b["x"].(float64) < a["x"].(float64)+a["w"].(float64)+canvasGap && b["y"] == a["y"] {
		t.Errorf("the second board is closer than the gap: %v %v", a, b)
	}
	r := e.ok(t, `{"op":"read","id":"pricing"}`)
	if r["html"] != "<h1>Pricing</h1>" {
		t.Errorf("read: %v", r)
	}
}

func TestUndoAndRedoMoveAlongTheVersionsOfOneBoard(t *testing.T) {
	e := canvasSetup(t, SiteSettings{})
	e.ok(t, `{"op":"put","id":"a","html":"one","by":"genie"}`)
	e.ok(t, `{"op":"put","id":"a","html":"two","by":"user","label":"you changed the heading"}`)
	e.ok(t, `{"op":"undo","id":"a"}`)
	if r := e.ok(t, `{"op":"read","id":"a"}`); r["html"] != "one" {
		t.Errorf("after Undo: %v", r)
	}
	e.ok(t, `{"op":"redo","id":"a"}`)
	if r := e.ok(t, `{"op":"read","id":"a"}`); r["html"] != "two" {
		t.Errorf("after Redo: %v", r)
	}
	e.ok(t, `{"op":"undo","id":"a"}`)
	e.ok(t, `{"op":"put","id":"a","html":"three","by":"genie"}`)
	if code, _ := e.call(t, `{"op":"redo","id":"a"}`, ""); code != http.StatusConflict {
		t.Errorf("a new version replaces the undone ones, so there is nothing to redo: %d", code)
	}
	if code, _ := e.call(t, `{"op":"undo","id":"missing"}`, ""); code != http.StatusNotFound {
		t.Errorf("undo of a board that is not there: %d", code)
	}
	v := e.ok(t, `{"op":"versions","id":"a"}`)
	if len(v["versions"].([]interface{})) != 2 {
		t.Errorf("versions: %v", v)
	}
	e.ok(t, `{"op":"restore","id":"a","n":1}`)
	if r := e.ok(t, `{"op":"read","id":"a"}`); r["html"] != "one" {
		t.Errorf("restore: %v", r)
	}
}

func TestTheLimitsOfTheCanvasHold(t *testing.T) {
	e := canvasSetup(t, SiteSettings{})
	big := strings.Repeat("x", maxBoardBytes+1)
	body, _ := json.Marshal(map[string]interface{}{"op": "put", "id": "big", "html": big})
	if code, _ := e.call(t, string(body), ""); code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
		t.Errorf("a board over the limit: %d", code)
	}
	if code, _ := e.call(t, `{"op":"put","id":"Bad Name","html":"x"}`, ""); code != http.StatusBadRequest {
		t.Errorf("a bad name: %d", code)
	}
	for i := 0; i < maxCanvasBoards; i++ {
		id := "b" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		e.ok(t, `{"op":"put","id":"`+id+`","html":"x","w":100,"h":100}`)
	}
	if code, out := e.call(t, `{"op":"put","id":"one-more","html":"x"}`, ""); code != http.StatusConflict {
		t.Errorf("board number %d: %d %v", maxCanvasBoards+1, code, out)
	}
}

func TestMovingAndSizingABoardIsNotAVersion(t *testing.T) {
	e := canvasSetup(t, SiteSettings{})
	e.ok(t, `{"op":"put","id":"a","html":"x"}`)
	st := e.ok(t, `{"op":"layout","boards":{"a":{"x":120,"y":40,"w":500,"h":300}}}`)
	b := boardsOf(st)[0]
	if b["x"] != 120.0 || b["w"] != 500.0 || b["head"] != 1.0 {
		t.Errorf("layout: %v", b)
	}
	st = e.ok(t, `{"op":"layout","boards":{"a":{"w":5}}}`)
	if boardsOf(st)[0]["w"] != float64(canvasMinSide) {
		t.Errorf("a board is never thinner than %d: %v", canvasMinSide, boardsOf(st)[0])
	}
}

func TestPinsBelongToABoardAndAreMarkedWhenSent(t *testing.T) {
	e := canvasSetup(t, SiteSettings{})
	e.ok(t, `{"op":"put","id":"a","html":"x"}`)
	if code, _ := e.call(t, `{"op":"pin","pin":{"board":"nope","x":0.5,"y":0.5,"text":"hi"}}`, ""); code != http.StatusBadRequest {
		t.Errorf("a pin on no board: %d", code)
	}
	st := e.ok(t, `{"op":"pin","pin":{"board":"a","x":2,"y":-1,"text":"make this red","el":"button.buy"}}`)
	pins := st["pins"].([]interface{})
	if len(pins) != 1 {
		t.Fatalf("pins: %v", st)
	}
	p := pins[0].(map[string]interface{})
	if p["x"] != 1.0 || p["y"] != 0.0 || p["sent"] != false {
		t.Errorf("a pin stays on its board and starts unsent: %v", p)
	}
	st = e.ok(t, `{"op":"pinsent","ids":["`+p["id"].(string)+`"]}`)
	if st["pins"].([]interface{})[0].(map[string]interface{})["sent"] != true {
		t.Errorf("sent: %v", st["pins"])
	}
	st = e.ok(t, `{"op":"delete","id":"a"}`)
	if len(st["pins"].([]interface{})) != 0 {
		t.Errorf("a pin goes with its board: %v", st["pins"])
	}
}

func TestDuplicateKeepsTheOriginalAndTheLogTellsWhatChanged(t *testing.T) {
	e := canvasSetup(t, SiteSettings{})
	e.ok(t, `{"op":"put","id":"a","title":"A","html":"x","by":"genie"}`)
	e.ok(t, `{"op":"put","id":"a","html":"y","by":"user","label":"you changed the heading on A"}`)
	out := e.ok(t, `{"op":"duplicate","id":"a"}`)
	if out["id"] != "a-2" {
		t.Errorf("duplicate: %v", out)
	}
	st := e.ok(t, `{"op":"state"}`)
	if len(boardsOf(st)) != 2 {
		t.Errorf("two boards: %v", st)
	}
	log := st["log"].([]interface{})
	found := false
	for _, l := range log {
		m := l.(map[string]interface{})
		if m["by"] == "user" && m["text"] == "you changed the heading on A" {
			found = true
		}
	}
	if !found {
		t.Errorf("the user's change is in the log: %v", log)
	}
	rev := st["rev"].(float64)
	again := e.ok(t, `{"op":"state","since":`+jsonNumber(rev)+`}`)
	if again["unchanged"] != true {
		t.Errorf("same revision: %v", again)
	}
}

func TestTheCanvasCanBeSwitchedOffAndAForkHasNone(t *testing.T) {
	e := canvasSetup(t, SiteSettings{CanvasOff: true})
	if code, out := e.call(t, `{"op":"state"}`, ""); code != http.StatusForbidden {
		t.Errorf("switched off: %d %v", code, out)
	}
	e = canvasSetup(t, SiteSettings{})
	if code, out := e.call(t, `{"op":"state"}`, "?jid=abc"); code != http.StatusForbidden {
		t.Errorf("a fork: %d %v", code, out)
	}
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
