package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"utils"
)

func firebaseEnv(t *testing.T, value string) {
	t.Helper()
	old, had := os.LookupEnv(utils.EnvFirebaseConfig)
	if value == "" {
		os.Unsetenv(utils.EnvFirebaseConfig)
	} else {
		os.Setenv(utils.EnvFirebaseConfig, value)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(utils.EnvFirebaseConfig, old)
		} else {
			os.Unsetenv(utils.EnvFirebaseConfig)
		}
	})
}

// There is no built-in project: a server started without OPENREPL_FIREBASE_CONFIG serves no config of any project, and says so in
// the console of the page, instead of quietly using the production one.
func TestNoFirebaseConfigIsServedWithoutTheEnvironment(t *testing.T) {
	firebaseEnv(t, "")
	js := string(firebaseConfigJS())
	if !strings.Contains(js, "const firebaseconfig = null;") || !strings.Contains(js, "OPENREPL_FIREBASE_CONFIG") {
		t.Errorf("config.js without a Firebase config:\n%s", js)
	}
	for _, leak := range []string{"openrepl-app", "AIza", "apiKey", "firebaseio.com"} {
		if strings.Contains(js, leak) {
			t.Errorf("a project is still built in: %q in\n%s", leak, js)
		}
	}
}

func TestFirebaseConfigFromTheEnvironmentIsServed(t *testing.T) {
	firebaseEnv(t, `{"apiKey":"AIzaSyDevKey1234567890","authDomain":"my-dev.firebaseapp.com","projectId":"my-dev-project"}`)
	js := string(firebaseConfigJS())
	if strings.Contains(js, "openrepl-app") {
		t.Fatalf("the production project is still in the page:\n%s", js)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(js), "const firebaseconfig = "), ";")
	var cfg map[string]string
	if err := json.Unmarshal([]byte(body), &cfg); err != nil || cfg["projectId"] != "my-dev-project" || cfg["authDomain"] != "my-dev.firebaseapp.com" {
		t.Fatalf("%v %v\n%s", cfg, err, js)
	}
}

func TestAWrongFirebaseConfigStopsTheServerFromStarting(t *testing.T) {
	firebaseEnv(t, `{"apiKey":"AIzaSyDevKey1234567890","authDomain":"my-dev.firebaseapp.com"}`)
	o := &Options{Mode: ModeStandalone, RelocateAfter: "2m"}
	err := o.Validate()
	if err == nil || !strings.Contains(err.Error(), "projectId") {
		t.Fatalf("Validate = %v, want an error that names the missing field", err)
	}
	firebaseEnv(t, `{"apiKey":"AIzaSyDevKey1234567890","authDomain":"my-dev.firebaseapp.com","projectId":"my-dev-project"}`)
	if err := o.Validate(); err != nil {
		t.Fatalf("a good config: %v", err)
	}
	firebaseEnv(t, "")
	if err := o.Validate(); err != nil {
		t.Fatalf("no config: %v", err)
	}
}
