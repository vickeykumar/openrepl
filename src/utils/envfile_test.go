package utils

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// clearEnv unsets the variables and puts them back when the test is over.
func clearEnv(t *testing.T, names ...string) {
	t.Helper()
	saved := map[string]*string{}
	for _, n := range names {
		if v, ok := os.LookupEnv(n); ok {
			v := v
			saved[n] = &v
		} else {
			saved[n] = nil
		}
		os.Unsetenv(n)
	}
	savedLast := LastEnvFile
	t.Cleanup(func() {
		LastEnvFile = savedLast
		for n, v := range saved {
			if v == nil {
				os.Unsetenv(n)
			} else {
				os.Setenv(n, *v)
			}
		}
	})
}

func writeEnvFile(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "envfile")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := filepath.Join(dir, ".env")
	if err := ioutil.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseEnvFile(t *testing.T) {
	pairs, problems := ParseEnvFile([]byte(`# a comment

OPENREPL_ENV=dev
export OPENREPL_HOST=example.com
  GOTTY_PORT = 8081
OPENREPL_ADMIN_EMAILS="a@example.com, b@example.com"   # two admins
OPENREPL_X='single $quoted \n value'
OPENREPL_Y="line1\nline2 \"q\" \\ \$HOME"
OPENREPL_EMPTY=
OPENREPL_HASH=abc#def
OPENREPL_COMMENTED=value # not part of it
OPENREPL_EQ=a=b=c
export	OPENREPL_TAB=tab
`))
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	got := map[string]string{}
	for _, p := range pairs {
		got[p.Name] = p.Value
	}
	want := map[string]string{
		"OPENREPL_ENV":          "dev",
		"OPENREPL_HOST":         "example.com",
		"GOTTY_PORT":            "8081",
		"OPENREPL_ADMIN_EMAILS": "a@example.com, b@example.com",
		"OPENREPL_X":            `single $quoted \n value`,
		"OPENREPL_Y":            "line1\nline2 \"q\" \\ $HOME",
		"OPENREPL_EMPTY":        "",
		"OPENREPL_HASH":         "abc#def",
		"OPENREPL_COMMENTED":    "value",
		"OPENREPL_EQ":           "a=b=c",
		"OPENREPL_TAB":          "tab",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestParseEnvFileHandlesWindowsLineEndingsAndABOM(t *testing.T) {
	pairs, problems := ParseEnvFile([]byte("\ufeffOPENREPL_A=1\r\nOPENREPL_B=\"two\"\r\n"))
	if len(problems) != 0 || len(pairs) != 2 || pairs[0].Value != "1" || pairs[1].Value != "two" {
		t.Fatalf("%v %v", pairs, problems)
	}
}

func TestParseEnvFileTheLastDuplicateWinsInTheFirstPlace(t *testing.T) {
	pairs, _ := ParseEnvFile([]byte("OPENREPL_A=1\nOPENREPL_B=2\nOPENREPL_A=3\n"))
	if len(pairs) != 2 || pairs[0].Name != "OPENREPL_A" || pairs[0].Value != "3" || pairs[1].Name != "OPENREPL_B" {
		t.Fatalf("%v", pairs)
	}
}

func TestParseEnvFileReportsLinesWithoutRepeatingThem(t *testing.T) {
	secret := "sk-very-secret"
	_, problems := ParseEnvFile([]byte("OPENREPL_OK=1\njust some text " + secret + "\nOPENREPL_Q=\"" + secret + "\nBAD NAME=" + secret + "\n1BAD=" + secret + "\nOPENREPL_AFTER='" + secret + "' trailing\n"))
	lines := []int{}
	for _, p := range problems {
		lines = append(lines, p.Line)
		if strings.Contains(p.What, secret) {
			t.Fatalf("a problem repeats the line: %q", p.What)
		}
	}
	if !reflect.DeepEqual(lines, []int{2, 3, 4, 5, 6}) {
		t.Fatalf("problem lines = %v: %v", lines, problems)
	}
}

func TestLoadEnvFileSetsOnlyWhatTheEnvironmentLacksAndOnlyOpenreplAndGottyNames(t *testing.T) {
	clearEnv(t, "OPENREPL_A", "OPENREPL_B", "OPENREPL_C", "GOTTY_PORT", "OPENREPL_T_NOT_SET")
	os.Setenv("OPENREPL_B", "from-the-environment") // wins over the file
	os.Setenv("OPENREPL_C", "")                     // empty counts as not set
	oldPath := os.Getenv("PATH")
	p := writeEnvFile(t, "OPENREPL_A=from-file\nOPENREPL_B=from-file\nOPENREPL_C=from-file\nGOTTY_PORT=8199\nPATH=/evil\nLD_PRELOAD=/evil.so\nHOME=/nowhere\n", 0600)

	res, err := LoadEnvFile(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("OPENREPL_A") != "from-file" || os.Getenv("GOTTY_PORT") != "8199" || os.Getenv("OPENREPL_C") != "from-file" {
		t.Fatalf("not loaded: A=%q PORT=%q C=%q", os.Getenv("OPENREPL_A"), os.Getenv("GOTTY_PORT"), os.Getenv("OPENREPL_C"))
	}
	if os.Getenv("OPENREPL_B") != "from-the-environment" {
		t.Fatalf("the environment lost to the file: %q", os.Getenv("OPENREPL_B"))
	}
	if os.Getenv("PATH") != oldPath || os.Getenv("LD_PRELOAD") != "" || os.Getenv("HOME") == "/nowhere" {
		t.Fatalf("the file changed a variable that is not its to set: PATH=%q LD_PRELOAD=%q HOME=%q", os.Getenv("PATH"), os.Getenv("LD_PRELOAD"), os.Getenv("HOME"))
	}
	if !reflect.DeepEqual(res.Loaded, []string{"OPENREPL_A", "OPENREPL_C", "GOTTY_PORT"}) ||
		!reflect.DeepEqual(res.Kept, []string{"OPENREPL_B"}) ||
		!reflect.DeepEqual(res.Ignored, []string{"PATH", "LD_PRELOAD", "HOME"}) || res.Path != p {
		t.Fatalf("result = %+v", res)
	}
	os.Unsetenv("LD_PRELOAD")
}

func TestLoadEnvFileAFileThatDoesNotExist(t *testing.T) {
	clearEnv(t)
	missing := filepath.Join(os.TempDir(), "no-such-dir-envfile", ".env")
	if res, err := LoadEnvFile(missing, false); err != nil || res.Path != "" {
		t.Fatalf("the default file is allowed to be missing: %+v %v", res, err)
	}
	if _, err := LoadEnvFile(missing, true); err == nil || !strings.Contains(err.Error(), "cannot read env file") {
		t.Fatalf("a file the operator named must exist: %v", err)
	}
	dir := filepath.Dir(writeEnvFile(t, "", 0600))
	if _, err := LoadEnvFile(dir, true); err == nil {
		t.Fatal("a directory was accepted")
	}
	if res, err := LoadEnvFile(dir, false); err != nil || res.Path != "" {
		t.Fatalf("a directory as the default is skipped: %+v %v", res, err)
	}
}

func TestLoadEnvFileBadLines(t *testing.T) {
	clearEnv(t, "OPENREPL_GOOD", "OPENREPL_SECRET_LINE")
	body := "OPENREPL_GOOD=yes\nthis is not a setting sk-hush\nOPENREPL_SECRET_LINE=\"sk-hush\n"
	p := writeEnvFile(t, body, 0600)

	// named by the operator: an error that gives the line numbers and nothing else
	_, err := LoadEnvFile(p, true)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "line 3") || strings.Contains(err.Error(), "sk-hush") {
		t.Fatalf("error = %v", err)
	}
	if os.Getenv("OPENREPL_GOOD") != "" {
		t.Fatal("a file with errors was partly loaded after all")
	}
	// the default file may be shared with other tools: the good lines are used
	res, err := LoadEnvFile(p, false)
	if err != nil || os.Getenv("OPENREPL_GOOD") != "yes" || !reflect.DeepEqual(res.Loaded, []string{"OPENREPL_GOOD"}) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestLoadEnvFileExpandsTheHomeDirectory(t *testing.T) {
	clearEnv(t, "OPENREPL_FROM_HOME", "HOME")
	home, err := ioutil.TempDir("", "home")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	if err := ioutil.WriteFile(filepath.Join(home, ".env"), []byte("OPENREPL_FROM_HOME=yes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Setenv("HOME", home)
	res, err := LoadEnvFile(EnvFileDefault, false)
	if err != nil || os.Getenv("OPENREPL_FROM_HOME") != "yes" || res.Path != filepath.Join(home, ".env") {
		t.Fatalf("%+v %v", res, err)
	}
	if expandHome("/abs/path") != "/abs/path" || expandHome("rel/~/x") != "rel/~/x" || expandHome("~") != home {
		t.Fatalf("expandHome: %q %q %q", expandHome("/abs/path"), expandHome("rel/~/x"), expandHome("~"))
	}
}

func TestTheSummarySaysWhatTheEnvFileDidWithoutValues(t *testing.T) {
	clearEnv(t, "OPENREPL_SUM_A", "OPENREPL_SUM_B", EnvAdminEmails, EnvOpenAIKey, EnvOpenRouterKey, EnvMongoURI, EnvMongoDB, EnvSecret, EnvHost, EnvMode, EnvFirebaseConfig)
	os.Setenv("OPENREPL_SUM_B", "x")
	p := writeEnvFile(t, "OPENREPL_SUM_A=hush-value\nOPENREPL_SUM_B=hush-value\nPATH=/x\nOPENREPL_OPENAI_API_KEY=sk-hush\n", 0600)
	if _, err := LoadEnvFile(p, true); err != nil {
		t.Fatal(err)
	}
	got := configSummary(false)
	for _, want := range []string{"env file=" + p, "2 loaded", "1 kept because the environment already had them", "1 ignored, not OPENREPL_ or GOTTY_: PATH", "openai key: set (from env)"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary lacks %q: %s", want, got)
		}
	}
	if strings.Contains(got, "hush") {
		t.Fatalf("the summary shows a value: %s", got)
	}
	LastEnvFile = EnvFileResult{}
	if got := configSummary(false); !strings.Contains(got, "env file=none") {
		t.Errorf("no env file: %s", got)
	}
}

// A service account key as one line of JSON survives the env file: in single
// quotes, or unquoted, but not in double quotes, which would turn the \n inside
// the private key into line breaks.
func TestAServiceAccountKeyInAnEnvFile(t *testing.T) {
	json := `{"type":"service_account","project_id":"p","private_key":"-----BEGIN PRIVATE KEY-----\nAAA\n-----END PRIVATE KEY-----\n","client_email":"a@p.iam"}`
	for name, line := range map[string]string{
		"single quotes": "OPENREPL_FIRESTORE_CREDENTIALS='" + json + "'",
		"unquoted":      "OPENREPL_FIRESTORE_CREDENTIALS=" + json,
	} {
		pairs, problems := ParseEnvFile([]byte(line + "\n"))
		if len(problems) != 0 || len(pairs) != 1 || pairs[0].Value != json {
			t.Errorf("%s: %+v %+v", name, pairs, problems)
		}
	}
	pairs, _ := ParseEnvFile([]byte(`OPENREPL_FIRESTORE_CREDENTIALS="` + strings.ReplaceAll(json, `"`, `\"`) + `"` + "\n"))
	if len(pairs) == 1 && pairs[0].Value == json {
		t.Error("double quotes keep the \\n of the private key as two characters; the test's premise is wrong")
	}
}
