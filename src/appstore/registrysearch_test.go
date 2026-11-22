package appstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// the registries answer from a stand-in server; nothing here goes to the internet
func fakeRegistries(t *testing.T) (calls *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if r.Header.Get("User-Agent") == "" {
			t.Error("crates.io wants a User-Agent")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "crates"):
			w.Write([]byte(`{"crates":[{"name":"serde","newest_version":"1.0.229","description":"A generic\n serialization framework"}]}`))
		case strings.Contains(r.URL.Path, "solr"):
			w.Write([]byte(`{"response":{"docs":[{"id":"com.google.guava:guava","latestVersion":"33.4.8-jre"},{"id":"broken","latestVersion":"1"},{"id":"a:b:c","latestVersion":"1"}]}}`))
		case strings.Contains(r.URL.Path, "v1/search"):
			w.Write([]byte(`{"items":[{"modulePath":"github.com/spf13/cobra","version":"v1.10.2","synopsis":"A commander"},{"modulePath":"github.com/spf13/cobra","version":"v1.0.0"},{"modulePath":"github.com/google/uuid/v5","version":"v5.0.0"}]}`))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	oc, om, op := cratesAPI, mavenAPI, pkgsiteAPI
	cratesAPI, mavenAPI, pkgsiteAPI = srv.URL+"/api/v1/crates", srv.URL+"/solr/select", srv.URL+"/v1/search"
	t.Cleanup(func() { cratesAPI, mavenAPI, pkgsiteAPI = oc, om, op })
	searchMu.Lock()
	searchCache = map[string]cachedFound{}
	searchMu.Unlock()
	return &n
}

func TestSearchOfCratesMavenAndGoModules(t *testing.T) {
	calls := fakeRegistries(t)
	ctx := context.Background()
	cr, err := searchCrates(ctx, "serde")
	if err != nil || len(cr) != 1 || cr[0].Name != "serde" || cr[0].Package != "serde" || cr[0].Kind != KindCrate || strings.Contains(cr[0].Summary, "\n") {
		t.Fatalf("crates: %+v %v", cr, err)
	}
	jar, err := searchMaven(ctx, "guava")
	if err != nil || len(jar) != 1 || jar[0].Name != "guava" || jar[0].Package != "com.google.guava:guava" || jar[0].Kind != KindJar {
		t.Fatalf("maven (an id that is not group:artifact is left out): %+v %v", jar, err)
	}
	gm, err := searchGo(ctx, "cobra")
	if err != nil || len(gm) != 2 || gm[0].Name != "cobra" || gm[0].Package != "github.com/spf13/cobra" || gm[1].Name != "uuid" || gm[1].Package != "github.com/google/uuid/v5" {
		t.Fatalf("go (a repeated module once, /v5 is not the name): %+v %v", gm, err)
	}
	before := *calls
	searchCrates(ctx, "serde")
	if *calls != before {
		t.Error("the same search twice asks the registry once")
	}
	// a word that is not a name is not sent anywhere
	if out, _ := searchCrates(ctx, "a b; rm"); out != nil || *calls != before {
		t.Error("an odd word must not reach the registry")
	}
}

func TestARegistryThatFailsIsAnErrorAndIsNotKept(t *testing.T) {
	fakeRegistries(t)
	oc := cratesAPI
	cratesAPI = "http://127.0.0.1:1/crates"
	defer func() { cratesAPI = oc }()
	if _, err := searchCrates(context.Background(), "nothing"); err == nil {
		t.Fatal("an unreachable registry must be an error")
	}
	searchMu.Lock()
	_, kept := searchCache["crate:nothing"]
	searchMu.Unlock()
	if kept {
		t.Error("an error must not be cached")
	}
}

func TestAptSearchReadsAptCache(t *testing.T) {
	out := "ripgrep - Faster grep\nlibfoo-dev - Development files for foo\nnot a line\nBad Name - x\n"
	got := foundFromAptCache(out)
	if len(got) != 2 || got[0].Name != "ripgrep" || got[0].Kind != KindApt || got[1].Name != "libfoo-dev" {
		t.Fatalf("%+v", got)
	}
	searchMu.Lock()
	searchCache = map[string]cachedFound{}
	searchMu.Unlock()
	var args []string
	res, err := searchApt(context.Background(), []string{"-o", "Dir::State=/x"}, "ripgrep", func(ctx context.Context, name string, a ...string) ([]byte, error) {
		args = append([]string{name}, a...)
		return []byte(out), nil
	})
	if err != nil || len(res) != 2 || strings.Join(args, " ") != "apt-cache -o Dir::State=/x search --names-only ripgrep" {
		t.Fatalf("%v %v %v", args, res, err)
	}
}

func TestGoShortName(t *testing.T) {
	for in, want := range map[string]string{"github.com/spf13/cobra": "cobra", "github.com/google/uuid/v5": "uuid", "golang.org/x/text": "text", "gopkg.in/yaml.v3": "yaml.v3"} {
		if got := goShortName(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

// a user asks for a crate that the catalogue does not have; the admin approves: the catalogue gets an entry, and the store name has the prefix
func TestARequestedCrateBecomesACatalogueEntryWhenApproved(t *testing.T) {
	svc, _ := newService(t, Fake{})
	if _, err := svc.RequestMirror("ann", KindCrate, "itoa", "itoa", "Fast integer printing", ""); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, v := range svc.Views("ann", "") {
		if v.Name == "crate-itoa" && v.State == "requested" && v.Summary == "Fast integer printing" {
			found = true
		}
	}
	if !found {
		t.Fatal("the request is not in the views")
	}
	if err := svc.Approve("admin", "crate-itoa"); err != nil {
		t.Fatal(err)
	}
	svc.Wait() // the install the approval starts must end before the folder is removed
	e, ok := svc.Entry("crate:itoa")
	if !ok || e.Package != "itoa" || e.Kind != CKCrate || e.CheckArg != "vendor" || e.PackageName() != "crate-itoa" {
		t.Fatalf("entry: %+v %v", e, ok)
	}
	// another package with the same short name is refused
	if _, err := svc.RequestMirror("bob", KindJar, "guava", "com.google.guava:guava", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddEntry("admin", Entry{ID: "jar:guava", Name: "guava", Summary: "x", Kind: CKJar, Level: "optional", Manager: "maven mirror", Install: "approval", Repls: []string{"java"}, Package: "other:guava"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Approve("admin", "jar-guava"); err == nil || !strings.Contains(err.Error(), "already the name") {
		t.Errorf("a name taken by another package: %v", err)
	}
	svc.Wait()
}

func TestADebianPackageIsRequestedApprovedAndListed(t *testing.T) {
	svc, _ := newService(t, Fake{})
	if _, err := svc.RequestFor("ann", "ripgrep", "apt", "", "I grep a lot"); err != nil {
		t.Fatal(err)
	}
	has := func(state string) bool {
		for _, v := range svc.Views("ann", "") {
			if v.Name == "ripgrep" && v.Kind == KindApt && v.State == state {
				return true
			}
		}
		return false
	}
	if !has("requested") {
		t.Fatal("the request must show for the asker")
	}
	if err := svc.Approve("admin", "apt:ripgrep"); err != nil {
		t.Fatal(err)
	}
	if !has("installed") || len(svc.Apt()) != 1 || svc.Apt()[0] != "ripgrep" {
		t.Fatalf("approved: apt list %v", svc.Apt())
	}
	if _, err := svc.RequestFor("ann", "ripgrep", "apt", "", ""); err != ErrInstalled {
		t.Errorf("a package that is on the list: %v", err)
	}
	if _, err := svc.RequestFor("ann", "Bad Name", "apt", "", ""); err == nil {
		t.Error("a bad name")
	}
}

// the search of everything asks every source, a few results of each, and a source that fails does not stop the others
type sourceStub struct {
	found []Found
	err   error
}

func (s sourceStub) Name() string { return "stub" }
func (s sourceStub) Resolve(ctx context.Context, name, version, kind string) (Resolved, error) {
	return Resolved{}, nil
}
func (s sourceStub) Install(ctx context.Context, name, version, kind, dest string, log io.Writer) error {
	return nil
}
func (s sourceStub) Search(ctx context.Context, q, kind string) ([]Found, error) {
	return s.found, s.err
}

func TestTheSearchOfEverythingAsksEverySource(t *testing.T) {
	many := func(kind string, n int) []Found {
		var out []Found
		for i := 0; i < n; i++ {
			out = append(out, Found{Name: fmt.Sprintf("%s%d", kind, i), Kind: kind})
		}
		return out
	}
	m := Multi{Conda: sourceStub{found: many(KindLib, 1)}, Node: sourceStub{found: many(KindNode, 1)}, Crate: sourceStub{found: many(KindCrate, 9)},
		Jar: sourceStub{err: errors.New("maven down")}, GoMod: sourceStub{found: many(KindGoMod, 2)}, Apt: sourceStub{found: many(KindApt, 2)}}
	out, err := m.Search(context.Background(), "x", "")
	counts := map[string]int{}
	for _, f := range out {
		counts[f.Kind]++
	}
	if err != nil || counts[KindLib] != 1 || counts[KindNode] != 1 || counts[KindCrate] != 6 || counts[KindGoMod] != 2 || counts[KindApt] != 2 || counts[KindJar] != 0 {
		t.Fatalf("%v %v", counts, err)
	}
	// one kind asks only its own source
	one, _ := m.Search(context.Background(), "x", KindGoMod)
	if len(one) != 2 {
		t.Errorf("a kind: %d results", len(one))
	}
}

// the Go modules of the App Store keep working with or without the internet: the mirrors always come first
func TestGoMirrorsKeepWorkingAndTheGoProxyFollowsOnlyWithTheInternet(t *testing.T) {
	v := Vars{GoProxy: []string{"file:///store/gomod-cobra/gomod/cache/download"}}
	get := func(env []string, k string) string {
		for _, e := range env {
			if strings.HasPrefix(e, k+"=") {
				return e[len(k)+1:]
			}
		}
		return ""
	}
	off := v.Apply([]string{"PATH=/bin"})
	if get(off, "GOPROXY") != "file:///store/gomod-cobra/gomod/cache/download,off" || get(off, "GOSUMDB") != "off" || get(off, "GOFLAGS") != "-mod=mod" {
		t.Errorf("without the internet only the mirrors: %v", off)
	}
	on := v.Apply([]string{"PATH=/bin", "HTTPS_PROXY=http://127.0.0.1:3128"})
	if get(on, "GOPROXY") != "file:///store/gomod-cobra/gomod/cache/download,https://proxy.golang.org" || get(on, "GOSUMDB") != "sum.golang.org" {
		t.Errorf("with the internet the mirrors first, then the Go module proxy: %v", on)
	}
	if sc := EnvScript(t.TempDir(), nil); strings.Contains(sc, "GOPROXY") {
		t.Error("no Go mirror, no GOPROXY")
	}
}
