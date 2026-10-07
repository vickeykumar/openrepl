package persist

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These need the Firestore emulator: FIRESTORE_EMULATOR_HOST=host:port is set
// by whoever runs them (docker run mtlynch/firestore-emulator). Each test uses
// a project of its own.
func emulatorOnly(t *testing.T) string {
	t.Helper()
	host := os.Getenv("OPENREPL_TEST_FIRESTORE_EMULATOR")
	if host == "" {
		t.Skip("OPENREPL_TEST_FIRESTORE_EMULATOR is not set")
	}
	return host
}

func useEmulator(t *testing.T) {
	t.Helper()
	host := emulatorOnly(t)
	t.Setenv("FIRESTORE_EMULATOR_HOST", host)
	t.Setenv("OPENREPL_FIRESTORE_PROJECT", fmt.Sprintf("proj-%d", time.Now().UnixNano()))
	t.Setenv("OPENREPL_MONGODB_URI", "")
	t.Setenv("OPENREPL_FIRESTORE_CREDENTIALS", "")
	ResetRemote()
	Configure(true)
	t.Cleanup(func() { Configure(false); ResetRemote() })
}

func TestFirestoreStore(t *testing.T) {
	useEmulator(t)
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Backend() != "firestore" || Remote() != "firestore" {
		t.Fatalf("backend %q remote %q", s.Backend(), Remote())
	}
	exercise(t, s)
	// keys that are not valid document ids come back as they went in
	for _, k := range []string{"a/b", "..", "__x__", "~tilde", "uid with space", strings.Repeat("k", 800)} {
		if err := s.Store([]byte(k), []byte("v:"+k[:1])); err != nil {
			t.Fatalf("store %q: %v", k, err)
		}
		if v, err := s.Fetch([]byte(k)); err != nil || string(v) != "v:"+k[:1] {
			t.Fatalf("fetch %q: %q %v", k, v, err)
		}
	}
	found := map[string]bool{}
	s.Each(func(k, v []byte) bool { found[string(k)] = true; return true })
	for _, k := range []string{"a/b", "..", "__x__", "~tilde", "uid with space"} {
		if !found[k] {
			t.Errorf("Each lost the key %q", k)
		}
	}
	if err := s.Delete([]byte("never-there")); err != ErrNotFound {
		t.Fatalf("deleting a missing key: %v", err)
	}
}

func TestFirestoreManyRecordsAreListedAcrossPages(t *testing.T) {
	useEmulator(t)
	s, _ := Open(filepath.Join(t.TempDir(), "many.db"))
	for i := 0; i < 650; i++ {
		if err := s.Store([]byte(fmt.Sprintf("k%04d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	if err := s.Each(func(k, v []byte) bool { n++; return true }); err != nil || n != 650 {
		t.Fatalf("listed %d (%v)", n, err)
	}
}

func TestFirestoreCopyOnce(t *testing.T) {
	useEmulator(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	Configure(false)
	local, _ := Open(path)
	local.Store([]byte("SESSION_KEY"), []byte("old-secret"))
	local.Store([]byte("user1"), []byte(`{"name":"a"}`))
	local.Commit()
	local.Close()
	Configure(true)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.Fetch([]byte("SESSION_KEY")); err != nil || string(v) != "old-secret" {
		t.Fatalf("the copy: %q %v", v, err)
	}
	s.Delete([]byte("SESSION_KEY"))
	s2, _ := Open(path)
	if _, err := s2.Fetch([]byte("SESSION_KEY")); err == nil {
		t.Fatal("the file was copied a second time")
	}
}

// fakeFirestore answers every request with an error for the first `failures`
// requests, then with an empty result, and counts the requests.
func fakeFirestore(t *testing.T, failures int) (host string, requests *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		if n <= failures {
			w.WriteHeader(500)
			fmt.Fprint(w, `{"error":{"code":500,"status":"INTERNAL","message":"not today"}}`)
			return
		}
		fmt.Fprint(w, `[{"readTime":"2026-01-01T00:00:00Z"}]`)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &n
}

func startInit(t *testing.T, mongoURI, firestoreHost string) {
	t.Helper()
	t.Setenv("OPENREPL_MONGODB_URI", mongoURI)
	t.Setenv("FIRESTORE_EMULATOR_HOST", firestoreHost)
	t.Setenv("OPENREPL_FIRESTORE_CREDENTIALS", "")
	t.Setenv("OPENREPL_FIRESTORE_PROJECT", "p")
	fastInit(t)
	ResetRemote()
	Configure(true)
	t.Cleanup(func() { Configure(false); ResetRemote() })
}

func TestFirestoreIsTriedThreeTimesAndWinsIfTheLastTryWorks(t *testing.T) {
	host, n := fakeFirestore(t, 2)
	startInit(t, "", host)
	if got := Init(); got != "firestore" {
		t.Fatalf("chosen %q after %d requests", got, *n)
	}
	if *n != 3 {
		t.Fatalf("%d requests, want 3 (two failures, then the one that worked)", *n)
	}
	if _, notes := Chosen(); notes != "" {
		t.Fatalf("notes: %q", notes)
	}
	if Init(); *n != 3 {
		t.Fatalf("a second Init tested again: %d requests", *n)
	}
}

func TestFirestoreThatNeverAnswersMeansFilesAfterThreeTries(t *testing.T) {
	host, n := fakeFirestore(t, 1000)
	startInit(t, "", host)
	if got := Init(); got != "unqlite" {
		t.Fatalf("chosen %q", got)
	}
	if *n != 3 {
		t.Fatalf("%d requests, want exactly 3", *n)
	}
	if _, notes := Chosen(); !strings.Contains(notes, "Firestore of the project p did not answer after 3 tries") {
		t.Fatalf("notes: %q", notes)
	}
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil || s.Backend() != "unqlite" {
		t.Fatalf("open: %v %v", s, err)
	}
}

func TestMongoThatIsDownFallsBackToFirestore(t *testing.T) {
	host, n := fakeFirestore(t, 0)
	startInit(t, "mongodb://127.0.0.1:1/", host)
	if got := Init(); got != "firestore" {
		t.Fatalf("chosen %q", got)
	}
	if *n != 1 {
		t.Fatalf("%d Firestore requests, want 1", *n)
	}
	if _, notes := Chosen(); !strings.Contains(notes, "MongoDB did not answer after 3 tries") {
		t.Fatalf("notes: %q", notes)
	}
}

func TestMongoThatIsDownAndNoFirestoreMeansFiles(t *testing.T) {
	startInit(t, "mongodb://127.0.0.1:1/", "")
	if got := Init(); got != "unqlite" {
		t.Fatalf("chosen %q", got)
	}
}

func TestMongoThatAnswersIsNeverBypassedByFirestore(t *testing.T) {
	uri := os.Getenv("OPENREPL_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("OPENREPL_TEST_MONGODB_URI is not set")
	}
	host, n := fakeFirestore(t, 0)
	startInit(t, uri, host)
	t.Setenv("OPENREPL_MONGODB_DB", fmt.Sprintf("openrepl_test_%d", time.Now().UnixNano()))
	if got := Init(); got != "mongodb" {
		t.Fatalf("chosen %q", got)
	}
	if *n != 0 {
		t.Fatalf("Firestore was asked %d times although MongoDB answered", *n)
	}
}

func TestAWorkerTestsNothingAndKeepsFiles(t *testing.T) {
	host, n := fakeFirestore(t, 0)
	startInit(t, "mongodb://127.0.0.1:1/", host)
	Configure(false)
	if got := Init(); got != "unqlite" || Remote() != "" || *n != 0 {
		t.Fatalf("a worker chose %q (%d Firestore requests)", got, *n)
	}
}

func TestNothingConfiguredMeansFiles(t *testing.T) {
	startInit(t, "", "")
	if Remote() != "" {
		t.Fatalf("remote %q", Remote())
	}
}

// ---- credentials ----------------------------------------------------------------

func testKeyPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestCredentialsCanBeJSONBase64OrAFile(t *testing.T) {
	_, pemText := testKeyPEM(t)
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "my-proj", "client_email": "svc@my-proj.iam.gserviceaccount.com", "private_key": pemText,
	})
	file := filepath.Join(t.TempDir(), "key.json")
	os.WriteFile(file, raw, 0600)
	for name, in := range map[string]string{
		"json": string(raw), "base64": base64.StdEncoding.EncodeToString(raw), "file": file,
		"json in single quotes": "'" + string(raw) + "'", "json with spaces around": "  " + string(raw) + "\n",
	} {
		a, err := parseCredentials(in)
		if err != nil || a.ProjectID != "my-proj" || a.TokenURI != "https://oauth2.googleapis.com/token" {
			t.Errorf("%s: %+v %v", name, a, err)
		}
	}
	if _, err := parseCredentials("/etc/secrets/missing.json"); err == nil || !strings.Contains(err.Error(), `no readable file at "/etc/secrets/missing.json"`) {
		t.Errorf("a path that is not there: %v", err)
	}
	if _, err := parseCredentials("sk-this-is-secret-0123456789"); err == nil || strings.Contains(err.Error(), "this-is-secret") || !strings.Contains(err.Error(), "28 characters, starting with") {
		t.Errorf("a value that is nothing: %v", err)
	}
	for name, in := range map[string]string{
		"nonsense": "not credentials", "json without a key": `{"project_id":"p"}`, "bad json": `{`,
	} {
		if _, err := parseCredentials(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestTheServiceAccountSignsAValidJWTAndTheTokenIsReused(t *testing.T) {
	key, pemText := testKeyPEM(t)
	calls := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || len(parts) != 3 {
			http.Error(w, `{"error":"invalid_request"}`, 400)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c map[string]interface{}
		json.Unmarshal(claims, &c)
		if c["iss"] != "svc@p.iam" || c["scope"] != "https://www.googleapis.com/auth/datastore" || c["aud"] == "" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		fmt.Fprintf(w, `{"access_token":"tok-%d","expires_in":3600}`, calls)
	}))
	defer tokenSrv.Close()

	f := &Firestore{http: http.DefaultClient, project: "p", rsaKey: key,
		acct: &serviceAccount{ClientEmail: "svc@p.iam", PrivateKey: pemText, ProjectID: "p", TokenURI: tokenSrv.URL}}
	a, err := f.accessToken()
	if err != nil || a != "tok-1" {
		t.Fatalf("%q %v", a, err)
	}
	if b, _ := f.accessToken(); b != "tok-1" || calls != 1 {
		t.Fatalf("the token was not reused: %q after %d calls", b, calls)
	}
	f.expires = time.Now() // about to end: a new one
	if c, _ := f.accessToken(); c != "tok-2" {
		t.Fatalf("not renewed: %q", c)
	}

	// a refused key is an error that says so
	f.acct.ClientEmail = "someone-else"
	f.expires = time.Time{}
	if _, err := f.accessToken(); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("a refused key: %v", err)
	}
}

func TestDocumentIDs(t *testing.T) {
	for _, k := range []string{"uid-1", "worker-pin:abc", "u:123", "blog title with spaces", "SESSION_KEY"} {
		if docID([]byte(k)) != k {
			t.Errorf("%q was changed", k)
		}
	}
	for _, k := range []string{"", "a/b", ".", "..", "__name__", "~x", string([]byte{0xff, 0x00}), strings.Repeat("x", 701)} {
		id := docID([]byte(k))
		if id == k || !strings.HasPrefix(id, "~") || strings.Contains(id, "/") {
			t.Errorf("%q became %q", k, id)
		}
		if string(keyOfDocID(id)) != k {
			t.Errorf("%q does not come back from %q", k, id)
		}
	}
}
