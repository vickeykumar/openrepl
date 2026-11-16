package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"appstore"
)

// Every action of the admin's catalogue route, with the fake installer: nothing is installed by adding, importing or listing; an invalid entry is
// refused; the export can be imported again.
func TestTheAdminCatalogueRouteDoesEachActionAndInstallsNothingUnlessAsked(t *testing.T) {
	svc := testStoreService(t, appstore.Fake{Fail: map[string]bool{"nothere": true}})
	srv := &Server{options: &Options{}}
	do := func(method, body string) (int, map[string]interface{}) {
		r := httptest.NewRequest(method, "http://openrepl.test/admin/store/catalogue", strings.NewReader(body))
		r.Header.Set("Origin", "http://openrepl.test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.handleAdminCatalogue(w, r)
		var out map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, out := do("GET", ""); code != 200 || out["entries"] == nil || out["packs"] == nil || out["disk"] == nil || out["settings"] == nil {
		t.Fatalf("the list: %d %v", code, out)
	}
	entry := `{"id":"zzlib","name":"zzlib","summary":"s","kind":"lib","repls":["python"],"level":"optional","manager":"pixi","install":"approval","check":"python-import","checkArg":"zzlib","docs":"https://example.org/d"}`
	if code, out := do("POST", `{"action":"add","entry":`+entry+`}`); code != 200 {
		t.Fatalf("add: %d %v", code, out)
	}
	svc.Wait()
	if len(svc.Apps()) != 0 {
		t.Fatalf("adding an entry must not install it: %+v", svc.Apps())
	}
	if code, _ := do("POST", `{"action":"add","entry":{"id":"evil","name":"evil","summary":"s","kind":"lib","repls":["python"],"level":"core","manager":"pixi","install":"anyone","check":"sh -c id"}}`); code != http.StatusBadRequest {
		t.Errorf("a shell check was accepted: %d", code)
	}
	if code, _ := do("POST", `{"action":"add","entry":{"id":"x","name":"x","summary":"s","kind":"lib","repls":["cobol"],"level":"core","manager":"pixi","install":"anyone"}}`); code != http.StatusBadRequest {
		t.Errorf("an unknown REPL was accepted: %d", code)
	}
	if code, out := do("POST", `{"action":"lookup","name":"zzlib","kind":"lib"}`); code != 200 || out["solves"] != true || out["left"] == nil {
		t.Fatalf("lookup: %d %v", code, out)
	}
	if code, out := do("POST", `{"action":"lookup","name":"nothere","kind":"lib"}`); code != 200 || out["solves"] != false {
		t.Fatalf("lookup of a name that does not solve: %d %v", code, out)
	}
	if code, out := do("POST", `{"action":"bulkdry","kind":"lib","names":["zznew","nothere"]}`); code != 200 || len(out["rows"].([]interface{})) != 2 {
		t.Fatalf("bulkdry: %d %v", code, out)
	}
	if code, out := do("POST", `{"action":"bulkadd","kind":"lib","repls":["python"],"level":"optional","names":["zznew","nothere"]}`); code != 200 || out["added"].(float64) != 1 {
		t.Fatalf("bulkadd: %d %v", code, out)
	}
	if _, ok := svc.Entry("zznew"); !ok {
		t.Error("the name that solved is in the catalogue")
	}
	if _, ok := svc.Entry("nothere"); ok {
		t.Error("the name that did not solve is not")
	}
	if code, _ := do("POST", `{"action":"packsave","pack":{"id":"mine","name":"Mine","repls":["python"],"entries":["zzlib","zznew"]}}`); code != 200 {
		t.Fatalf("packsave: %d", code)
	}
	if code, out := do("POST", `{"action":"packinstall","id":"mine"}`); code != 200 || len(out["started"].([]interface{})) != 2 {
		t.Fatalf("packinstall: %d %v", code, out)
	}
	svc.Wait()
	if code, out := do("POST", `{"action":"install","id":"zzlib"}`); code != http.StatusBadRequest {
		t.Errorf("installing what is installed: %d %v", code, out)
	}
	// export, then import into a fresh catalogue (a dry run first)
	r := httptest.NewRequest("GET", "http://openrepl.test/admin/store/catalogue?export=1", nil)
	w := httptest.NewRecorder()
	srv.handleAdminCatalogue(w, r)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Disposition"), "openrepl-catalogue.json") || !strings.Contains(w.Body.String(), "zzlib") {
		t.Fatalf("export: %d %v", w.Code, w.Header())
	}
	svc2 := testStoreService(t, appstore.Fake{})
	body, _ := json.Marshal(map[string]interface{}{"action": "import", "dry": true, "data": json.RawMessage(w.Body.Bytes())})
	if code, out := do("POST", string(body)); code != 200 || out["report"].(map[string]interface{})["added"].(float64) < 100 {
		t.Fatalf("dry import: %d %v", code, out)
	}
	if _, ok := svc2.Entry("zzlib"); ok {
		t.Error("a dry import saved something")
	}
	body, _ = json.Marshal(map[string]interface{}{"action": "import", "dry": false, "data": json.RawMessage(w.Body.Bytes())})
	if code, _ := do("POST", string(body)); code != 200 {
		t.Fatalf("import: %d", code)
	}
	if _, ok := svc2.Entry("zzlib"); !ok {
		t.Error("the import did not add")
	}
	if code, out := do("POST", `{"action":"verify"}`); code != 200 || out["results"] == nil {
		t.Fatalf("verify: %d %v", code, out)
	}
	if code, _ := do("POST", `{"action":"remove","id":"zzlib"}`); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := do("POST", `{"action":"nonsense"}`); code != http.StatusBadRequest {
		t.Errorf("an unknown action: %d", code)
	}
}
