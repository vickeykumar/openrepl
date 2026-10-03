package utils

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestChildEnvironKeepsWhatAProgramNeedsAndDropsWhatBelongsToTheServer(t *testing.T) {
	in := []string{
		"PATH=/usr/bin:/bin", "HOME=/root", "LANG=C.UTF-8", "TZ=Etc/UTC", "GOPATH=/opt/gotty/", "PYTHONPATH=/x",
		"JAVA_HOME=/usr/lib/jvm", "LD_LIBRARY_PATH=/usr/lib", "SSH_AUTH_SOCK=/tmp/agent", "EMPTYVALUE=",
		// the server's own settings
		"OPENREPL_OPENAI_API_KEY=sk-secret", "OPENREPL_ENV=dev", "OPENREPL_ADMIN_EMAILS=a@example.com",
		"OPENREPL_FIREBASE_CONFIG={}", "openrepl_lowercase=1",
		"GOTTY_WORKER_TOKEN=tok", "GOTTY_CREDENTIAL=user:pass", "GOTTY_PORT=8080", "GOTTY_TLS_KEY=/k",
		// secrets of any other name
		"GITHUB_TOKEN=ghp_x", "AWS_SECRET_ACCESS_KEY=s", "AWS_ACCESS_KEY_ID=k", "DB_PASSWORD=p", "MY_PASSWD=p",
		"SERVICE_CREDENTIALS=c", "STRIPE_API_KEY=s", "SOME_APIKEY=s", "TLS_PRIVATE_KEY=s", "Github_Token=lower",
	}
	got := ChildEnviron(in)
	want := in[:10]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	for _, kv := range got {
		if IsServerEnv(strings.SplitN(kv, "=", 2)[0]) {
			t.Errorf("%s survived", kv)
		}
	}
}

func TestChildEnvironDoesNotChangeItsInput(t *testing.T) {
	in := []string{"A=1", "OPENREPL_X=2", "B=3"}
	_ = ChildEnviron(in)
	if !reflect.DeepEqual(in, []string{"A=1", "OPENREPL_X=2", "B=3"}) {
		t.Fatalf("input changed: %v", in)
	}
	if got := ChildEnviron(nil); len(got) != 0 {
		t.Fatalf("nil: %v", got)
	}
	// an entry without "=" is judged by its name too
	if got := ChildEnviron([]string{"GOTTY_ODD", "ODD"}); !reflect.DeepEqual(got, []string{"ODD"}) {
		t.Fatalf("%v", got)
	}
}

func TestExpandInOnlyKnowsTheGivenEnvironment(t *testing.T) {
	os.Setenv("OPENREPL_TEST_SERVER_ONLY", "server-secret")
	defer os.Unsetenv("OPENREPL_TEST_SERVER_ONLY")
	env := []string{"HOME=/home/guest", "PATH=/usr/bin", "X=first", "X=second"}
	for in, want := range map[string]string{
		"PATH=$PATH:/opt/bin":            "PATH=/usr/bin:/opt/bin",
		"D=${HOME}/work":                 "D=/home/guest/work",
		"X=$X":                           "X=second", // a later entry wins
		"Y=$NOT_SET":                     "Y=",
		"K=$OPENREPL_TEST_SERVER_ONLY":   "K=", // the server's own environment is not consulted
		"K=${OPENREPL_TEST_SERVER_ONLY}": "K=",
		"PLAIN=value":                    "PLAIN=value",
		"TWO=$HOME:$PATH":                "TWO=/home/guest:/usr/bin",
		"PREFIXED=$HOMELESS":             "PREFIXED=", // a longer name is another variable
		"":                               "",
	} {
		if got := ExpandIn(env, in); got != want {
			t.Errorf("ExpandIn(%q) = %q, want %q", in, got, want)
		}
	}
	// and os.ExpandEnv would have given the secret away: this is what it replaces
	if os.ExpandEnv("K=$OPENREPL_TEST_SERVER_ONLY") != "K=server-secret" {
		t.Fatal("test set-up: the server variable is not visible to os.ExpandEnv")
	}
}
