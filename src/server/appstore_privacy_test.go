package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appstore"
	"trusted"
	"utils"
)

// A user who is not an admin never receives sensitive information from /store/api (LLD 18): no node name or count, no CPU type, no
// per-node disk or state, no path on the server, no raw install or check log, no lock, no setting, and nothing of another user (name,
// request, reason, switch). Every route is asked, GET and POST, and the whole answer is searched.

// leakyService is a store with another user's data and a failed install whose log holds paths and the machine's name.
func leakyService(t *testing.T) (*appstore.Service, []string) {
	t.Helper()
	svc := testStoreService(t, appstore.Fake{Fail: map[string]bool{"pillow": true}}, "flask", "numpy", "pillow", "tool:zzcli")
	for _, n := range [][2]string{{"flask", "lib"}, {"numpy", "lib"}, {"zzcli", "tool"}} {
		svc.Install("bob-uid", n[0], n[1], false)
	}
	svc.Install("bob-uid", "pillow", "lib", false) // fails: its log names the machine and a folder
	svc.Wait()
	svc.Switch("bob-home", "flask-1.0.0", true)
	svc.Request("bob-uid", "ffmpeg", "tool", "BOB-SECRET-REASON")
	svc.Report("bob-uid", "pillow", "BOB-SECRET-NOTE")
	svc.SetDefault("root", "numpy", false)
	// a tool that does not start, for the failed switch-on
	os.WriteFile(filepath.Join(svc.Store().BundlePath("zzcli-1.0.0"), "bin", "zzcli"), []byte("#!/bin/sh\necho 'SECRET-LOG /store/apps/x libonig missing'\nexit 3\n"), 0755)
	host, _ := os.Hostname()
	secrets := []string{"BOB-SECRET-REASON", "BOB-SECRET-NOTE", "bob-uid", "bob-home", "SECRET-LOG", svc.Store().Dir, "linux-64", "linux-aarch64", "worker-secret-1"}
	if len(host) > 3 {
		secrets = append(secrets, host)
	}
	return svc, secrets
}

// forbiddenKeys must not be a key anywhere in an answer to a user.
var forbiddenKeys = map[string]bool{"node": true, "nodes": true, "log": true, "lock": true, "manifest": true, "envScript": true, "installer": true, "home": true,
	"settings": true, "reporters": true, "askers": true, "users": true, "path": true, "dir": true, "arch": true, "cpu": true, "disk": true, "reserve": true}

func scanKeys(t *testing.T, what string, v interface{}) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, val := range x {
			if forbiddenKeys[k] {
				t.Errorf("%s: the key %q must not be in an answer to a user", what, k)
			}
			scanKeys(t, what, val)
		}
	case []interface{}:
		for _, val := range x {
			scanKeys(t, what, val)
		}
	}
}

func userRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://openrepl.test/store/api/"+path, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	trusted.Set(r.Header, trusted.Identity{UID: "ann-uid", HomeID: "ann-home", Privilege: utils.GUEST})
	return r.WithContext(context.WithValue(r.Context(), trustedConnKey{}, true))
}

func TestNoRouteOfTheStoreAPIGivesAUserWhatOnlyAnAdminMaySee(t *testing.T) {
	old := utils.HOME_DIR
	utils.HOME_DIR = t.TempDir() + "/"
	defer func() { utils.HOME_DIR = old }()
	svc, secrets := leakyService(t)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"flask","latest_version":"3.0","summary":"A web framework","license":"BSD-3-Clause","versions":["3.0","2.3"]}`))
	}))
	defer hs.Close()
	oldAPI := condaForgeAPI
	condaForgeAPI = hs.URL + "/"
	defer func() { condaForgeAPI = oldAPI }()
	srv := &Server{options: &Options{}}
	routes := []struct{ method, path, body string }{
		{"GET", "list", ""}, {"GET", "notices", ""}, {"GET", "job?name=pillow", ""}, {"GET", "info?name=flask", ""}, {"GET", "search?q=flas", ""},
		{"GET", "search?q=flas&kind=lib", ""}, {"GET", "size?name=flask&kind=lib", ""}, {"GET", "plugins", ""},
		{"GET", "space", ""}, {"GET", "preview-plugins", ""}, {"POST", "plugin-switch", `{"id":"plugin:outline","on":false}`}, {"POST", "plugin-switch", `{"id":"plugin:eruda","on":true}`}, {"GET", "catalogue", ""}, {"GET", "catalogue?repl=python&level=core", ""}, {"GET", "catalogue?browser=1", ""}, {"GET", "catalogue?q=eruda&kind=plugin", ""},
		{"POST", "request", `{"name":"polars","kind":"lib","repl":"python","reason":"fast tables"}`},
		{"POST", "install", `{"name":"numpy","kind":"lib"}`}, {"POST", "install", `{"name":"pillow","kind":"lib"}`}, {"POST", "retry", `{"name":"pillow","kind":"lib"}`},
		{"POST", "request", `{"name":"ffmpeg","kind":"tool","reason":"mine"}`}, {"POST", "switch", `{"id":"flask-1.0.0","on":true}`},
		{"POST", "switch", `{"id":"zzcli-1.0.0","on":true}`}, // the check fails: a short plain message and nothing else
		{"POST", "switch", `{"id":"flask-1.0.0","on":false}`},
		{"POST", "report", `{"name":"pillow","note":"it fails"}`}, {"POST", "report", `{"name":"zzcli","note":"it does not start"}`},
		{"POST", "uninstall", `{"name":"flask"}`}, {"POST", "ack", `{"name":"ffmpeg"}`},
	}
	for _, rt := range routes {
		w := httptest.NewRecorder()
		srv.handleStoreAPI(w, userRequest(rt.method, rt.path, rt.body))
		body := w.Body.String()
		what := rt.method + " " + rt.path + " (" + http.StatusText(w.Code) + ")"
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s: the answer holds %q:\n%s", what, s, body)
			}
		}
		var j interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &j); err != nil {
			t.Errorf("%s: not JSON: %s", what, body)
		} else {
			scanKeys(t, what, j)
		}
		if rt.path == "list" && (w.Code != http.StatusOK || !strings.Contains(body, `"views"`) || !strings.Contains(body, `"name":"flask"`)) {
			t.Errorf("the list is not the list: %d %s", w.Code, body)
		}
		if w.Code == http.StatusNotFound || w.Code == http.StatusForbidden && rt.path != "install" && rt.path != "uninstall" {
			t.Errorf("%s: the route did not answer a signed-in user: %d %s", what, w.Code, body)
		}
	}
	// the failed switch-on said what a user may be told
	w := httptest.NewRecorder()
	srv.handleStoreAPI(w, userRequest("POST", "switch", `{"id":"zzcli-1.0.0","on":true}`))
	var fail struct {
		Error, Message string
		Wait           bool
	}
	json.Unmarshal(w.Body.Bytes(), &fail)
	if w.Code != http.StatusUnprocessableEntity || fail.Error != "verify" || !strings.Contains(fail.Message, "did not work for your terminals") {
		t.Errorf("the failed switch-on: %d %s", w.Code, w.Body.String())
	}
	// and the admin's failure list holds the detail: the node and the raw log
	reps := svc.Reports()
	found := false
	for _, r := range reps {
		if r.Name == "zzcli" && r.Node != "" && strings.Contains(r.Log, "SECRET-LOG") && r.Fails >= 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("the detail of the failed check must reach the admin's list: %+v", reps)
	}
	// the list a user gets has a plain reason for the failed install, the admin's has the log
	for _, v := range svc.Views("ann-uid", "ann-home") {
		if v.Name == "pillow" && (v.Log != "" || v.Node != "" || v.Err == "") {
			t.Errorf("a user's view of a failed install: %+v", v)
		}
	}
	full := false
	for _, v := range svc.ViewsFull("ann-uid", "ann-home") {
		if v.Name == "pillow" && v.Log != "" && v.Node != "" {
			full = true
		}
	}
	if !full {
		t.Error("an admin keeps the log and the node")
	}
}
