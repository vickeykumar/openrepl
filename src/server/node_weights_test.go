package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gateway"
)

func TestNodeWeightsAreCleanedAndRefuseNonsense(t *testing.T) {
	got, err := normalizeNodeWeights(map[string]int{"pi-1": 20, "local": 0, "vps": maxNodeWeight})
	if err != nil || got["pi-1"] != 20 || got["vps"] != maxNodeWeight {
		t.Fatalf("cleaned: %v %v", got, err)
	}
	if v, ok := got["local"]; !ok || v != 0 {
		t.Fatalf("a weight of 0 (routing only) must be kept: %v", got)
	}
	if got, err := normalizeNodeWeights(nil); err != nil || got != nil {
		t.Fatalf("nothing: %v %v", got, err)
	}
	for name, in := range map[string]map[string]int{
		"a negative weight":      {"pi-1": -1},
		"a weight above the max": {"pi-1": maxNodeWeight + 1},
		"a bad node id":          {"../x": 5},
		"a slash in the id":      {"a/b": 5},
	} {
		if _, err := normalizeNodeWeights(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAWeightIsSetAndRemovedWithoutChangingTheSettingsItCameFrom(t *testing.T) {
	var s SiteSettings
	s1 := s.withNodeWeight("pi-1", 25, true)
	s2 := s1.withNodeWeight("pi-2", 0, true)
	s3 := s2.withNodeWeight("pi-1", 0, false)
	if _, ok := s.NodeWeight("pi-1"); ok {
		t.Fatal("the original changed")
	}
	if w, ok := s1.NodeWeight("pi-1"); !ok || w != 25 {
		t.Fatalf("s1: %v", s1.NodeWeights)
	}
	if _, ok := s2.NodeWeight("pi-2"); !ok {
		t.Fatal("a weight of 0 was lost")
	}
	if _, ok := s3.NodeWeight("pi-1"); ok {
		t.Fatalf("removing did not remove: %v", s3.NodeWeights)
	}
	if w, _ := s1.NodeWeight("pi-1"); w != 25 {
		t.Fatal("removing changed an earlier copy")
	}
	if s2.withNodeWeight("pi-2", 0, false).withNodeWeight("pi-1", 0, false).NodeWeights != nil {
		t.Error("the last weight removed should leave nothing")
	}
}

func TestTheAuditLogSaysWhatChangedOnWhichNodeWeight(t *testing.T) {
	var a SiteSettings
	b := a.withNodeWeight("pi-1", 25, true).withNodeWeight("local", 0, true)
	got := strings.Join(nodeWeightChanges(a, b), "\n")
	for _, want := range []string{"set the weight of worker pi-1 to 25", "set the weight of the gateway to 0"} {
		if !strings.Contains(got, want) {
			t.Errorf("audit lacks %q: %s", want, got)
		}
	}
	c := b.withNodeWeight("pi-1", 30, true)
	if got := strings.Join(nodeWeightChanges(b, c), "\n"); !strings.Contains(got, "changed the weight of worker pi-1 from 25 to 30") {
		t.Errorf("a change: %s", got)
	}
	if got := strings.Join(nodeWeightChanges(c, b.withNodeWeight("pi-1", 0, false)), "\n"); !strings.Contains(got, "gave worker pi-1 its own weight back (was 30)") {
		t.Errorf("going back: %s", got)
	}
	if len(nodeWeightChanges(b, b)) != 0 {
		t.Error("no change was reported as one")
	}
}

func nodeWeightsTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, _ := adminTestServer(t)
	router := gateway.NewRouter(gateway.Config{
		Site: http.NotFoundHandler(), PathPrefix: "/", Secret: []byte("s"),
		Local:      gateway.LocalConfig{Weight: 10},
		NodeWeight: func(id string) (int, bool) { return GetSiteSettings().NodeWeight(id) },
	})
	router.AddBackend(&stubNode{id: "pi-1"})
	s.admin.router = router
	s.admin.prefix = "/"
	s.gatewayAdmin = http.NotFoundHandler()
	mux := http.NewServeMux()
	mux.Handle("/admin/workers/", s.adminAPI(http.HandlerFunc(s.handleGatewayAdmin)))
	mux.Handle("/admin/audit", s.adminAPI(http.HandlerFunc(s.handleAdminAudit)))
	return s, mux
}

func TestTheWeightRoute(t *testing.T) {
	_, h := nodeWeightsTestServer(t)

	var reply nodeWeightReply
	w := get(h, "/admin/workers/local/weight")
	decode(t, w, &reply)
	if w.Code != 200 || reply.Weight != 10 || reply.Configured != 10 || reply.Set || reply.Max != maxNodeWeight {
		t.Fatalf("the gateway's weight: %d %+v", w.Code, reply)
	}

	// set the gateway's own share: the router answers with it at once
	w = post(h, "/admin/workers/local/weight", `{"weight": 40}`)
	decode(t, w, &reply)
	if w.Code != 200 || reply.Weight != 40 || !reply.Set || reply.Configured != 10 {
		t.Fatalf("set 40 -> %d %+v", w.Code, reply)
	}
	if b, _ := nodeWeightsRouter(h); b != 40 {
		t.Fatalf("the router uses %d, want 40", b)
	}
	if GetSiteSettings().NodeWeights["local"] != 40 {
		t.Fatal("the weight was not saved")
	}

	// a weight of 0 is allowed while another node takes sessions
	if w = post(h, "/admin/workers/local/weight", `{"weight": 0}`); w.Code != 200 {
		t.Fatalf("set 0 -> %d %s", w.Code, w.Body.String())
	}
	// ... and not for the last one that does
	if w = post(h, "/admin/workers/pi-1/weight", `{"weight": 0}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "No node would take new sessions") {
		t.Fatalf("the last node to 0 -> %d %s", w.Code, w.Body.String())
	}
	if _, set := GetSiteSettings().NodeWeight("pi-1"); set {
		t.Fatal("a refused change was saved")
	}

	// back to the node's own
	w = post(h, "/admin/workers/local/weight", `{"weight": null}`)
	decode(t, w, &reply)
	if w.Code != 200 || reply.Set || reply.Weight != 10 {
		t.Fatalf("reset -> %d %+v", w.Code, reply)
	}
	post(h, "/admin/workers/local/weight", `{"weight": 7}`)
	if w = post(h, "/admin/workers/local/weight", `{"reset": true}`); w.Code != 200 || GetSiteSettings().NodeWeights != nil {
		t.Fatalf("reset: %d %v", w.Code, GetSiteSettings().NodeWeights)
	}

	for name, c := range map[string]struct{ path, body string }{
		"a negative weight":   {"/admin/workers/pi-1/weight", `{"weight": -3}`},
		"above the maximum":   {"/admin/workers/pi-1/weight", `{"weight": 1001}`},
		"not a number":        {"/admin/workers/pi-1/weight", `{"weight": "ten"}`},
		"not JSON":            {"/admin/workers/pi-1/weight", `weight=3`},
		"an id that is no id": {"/admin/workers/a%20b/weight", `{"weight": 3}`},
		"a node that is not":  {"/admin/workers/ghost/weight", `{"weight": 3}`},
	} {
		if w = post(h, c.path, c.body); w.Code < 400 || w.Code >= 500 {
			t.Errorf("%s -> %d, want a client error", name, w.Code)
		}
	}

	// the changes are in the audit log
	if w = get(h, "/admin/audit"); !strings.Contains(w.Body.String(), "the weight of the gateway") {
		t.Errorf("audit: %s", w.Body.String())
	}
}

// nodeWeightsRouter reads the weight the router's gateway backend uses now.
func nodeWeightsRouter(h http.Handler) (int, bool) {
	var v nodeWeightReply
	if err := json.Unmarshal(get(h, "/admin/workers/local/weight").Body.Bytes(), &v); err != nil {
		return -1, false
	}
	return v.Weight, v.Set
}

func TestSavingTheSettingsPageKeepsTheWeightsOfNodes(t *testing.T) {
	_, mux := adminTestServer(t)
	if err := SaveSiteSettings(SiteSettings{}.withNodeWeight("pi-1", 30, true)); err != nil {
		t.Fatal(err)
	}
	if w := post(mux, "/admin/settings", `{"disabledLanguages":["cpp"],"colorOfTheDay":true}`); w.Code != 200 {
		t.Fatalf("save -> %d %s", w.Code, w.Body.String())
	}
	if w, _ := GetSiteSettings().NodeWeight("pi-1"); w != 30 {
		t.Fatalf("the Settings page dropped the weight: %v", GetSiteSettings().NodeWeights)
	}
	post(mux, "/admin/settings", `{"nodeWeights":{"pi-9":5}}`)
	if _, set := GetSiteSettings().NodeWeight("pi-9"); set {
		t.Fatalf("the Settings page set a node weight: %v", GetSiteSettings().NodeWeights)
	}
}
