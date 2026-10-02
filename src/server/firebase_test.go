package server

import (
	"encoding/base64"
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

// The built-in config is the production project and has to stay exactly what
// it was: it is base64 in the source, which makes a typo easy to miss.
func TestBuiltInFirebaseConfigIsTheProductionProject(t *testing.T) {
	firebaseEnv(t, "")
	js := string(firebaseConfigJS())
	if _, err := base64.StdEncoding.DecodeString(builtinFirebaseConfig); err != nil {
		t.Fatalf("the built-in config is not valid base64: %v", err)
	}
	for _, want := range []string{
		"const firebaseconfig = {",
		`authDomain: "openrepl-app.firebaseapp.com"`,
		`projectId: "openrepl-app"`,
		`databaseURL: "https://openrepl-app-default-rtdb.firebaseio.com"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("the built-in config lacks %s:\n%s", want, js)
		}
	}
	if !strings.Contains(js, `apiKey: "AIza`) {
		t.Error("the built-in config has no api key")
	}
}

func TestFirebaseConfigFromTheEnvironmentReplacesTheBuiltInOne(t *testing.T) {
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

// The built-in config is base64 of a snippet. Giving that very text to
// OPENREPL_FIREBASE_CONFIG has to work, and has to give the same project.
func TestTheBuiltInConfigIsAcceptedInTheEnvironment(t *testing.T) {
	firebaseEnv(t, builtinFirebaseConfig)
	cfg, set, err := utils.FirebaseConfigFromEnv()
	if err != nil || !set {
		t.Fatalf("%v %v", set, err)
	}
	if cfg["projectId"] != "openrepl-app" || cfg["authDomain"] != "openrepl-app.firebaseapp.com" ||
		cfg["databaseURL"] != "https://openrepl-app-default-rtdb.firebaseio.com" || !strings.HasPrefix(cfg["apiKey"], "AIza") {
		t.Fatalf("%v", cfg)
	}
	if js := string(firebaseConfigJS()); !strings.Contains(js, `"projectId": "openrepl-app"`) {
		t.Fatalf("%s", js)
	}
}
