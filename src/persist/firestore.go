package persist

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"utils"
)

// Firestore through its REST API, with nothing but the standard library: the
// official client brings gRPC and a long list of modules that this GOPATH build
// would have to carry. The server signs in as a service account (a JSON key the
// operator provides in OPENREPL_FIRESTORE_CREDENTIALS), with the scope
// https://www.googleapis.com/auth/datastore. The browser-side web config of a
// Firebase project cannot do this: it carries no secret, and Firestore's
// security rules (not a key) decide what it may read.
//
// Against the Firestore emulator (FIRESTORE_EMULATOR_HOST=host:port) no
// credentials are needed; that is what the tests and a local run use.

// ErrFSNotFound and ErrFSConflict are what Get and Put answer for a missing
// document and for a failed precondition (the document exists, does not exist,
// or changed since it was read).
var (
	ErrFSNotFound = errors.New("persist: firestore document not found")
	ErrFSConflict = errors.New("persist: firestore precondition failed")
)

type serviceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

// Firestore is a connection to one project's default database.
type Firestore struct {
	base    string // https://firestore.googleapis.com/v1 or the emulator's
	project string
	acct    *serviceAccount // nil for the emulator
	rsaKey  *rsa.PrivateKey
	http    *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Project is the Google Cloud project whose Firestore this is.
func (f *Firestore) Project() string { return f.project }

// parseCredentials reads the service account key from what the operator gave:
// the JSON itself, the JSON base64-encoded (easier in an env file), or the path
// of a file that holds it.
func parseCredentials(raw string) (*serviceAccount, error) {
	raw = strings.TrimSpace(raw)
	// a pair of quotes around it, as a docker --env-file keeps them
	if len(raw) >= 2 && (raw[0] == '\'' || raw[0] == '"') && raw[len(raw)-1] == raw[0] {
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	var data []byte
	switch {
	case strings.HasPrefix(raw, "{"):
		data = []byte(raw)
	default:
		if b, err := ioutil.ReadFile(raw); err == nil {
			data = b
		} else if b, err := base64.StdEncoding.DecodeString(raw); err == nil && bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
			data = b
		} else if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") || strings.HasPrefix(raw, ".") || strings.HasSuffix(raw, ".json") {
			// it looks like a path (not a secret): say which one, and that it is not there
			return nil, fmt.Errorf("there is no readable file at %q (the path is read inside the container or on the machine the server runs on)", raw)
		} else {
			// only the size and the first character, never more of the value
			first := ""
			if raw != "" {
				first = string([]rune(raw)[:1])
			}
			return nil, fmt.Errorf("the Firestore credentials (%d characters, starting with %q) are neither JSON, base64 of JSON, nor the path of a file", len(raw), first)
		}
	}
	var a serviceAccount
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, errors.New("the Firestore credentials are not valid JSON")
	}
	if a.ClientEmail == "" || a.PrivateKey == "" || a.ProjectID == "" {
		return nil, errors.New("the Firestore credentials are not a service account key (no client_email, private_key or project_id)")
	}
	if a.TokenURI == "" {
		a.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &a, nil
}

func parsePrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("the service account's private key is not PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, errors.New("the service account's private key is not RSA")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// newFirestore builds a client from the environment: the emulator when
// FIRESTORE_EMULATOR_HOST is set, else the service account in
// OPENREPL_FIRESTORE_CREDENTIALS. It returns nil, nil when neither is there:
// Firestore is not configured. It does not talk to Google.
func newFirestore() (*Firestore, error) {
	f := &Firestore{http: &http.Client{Timeout: 15 * time.Second}}
	raw := strings.TrimSpace(os.Getenv(utils.EnvFirestoreCredentials))
	if raw != "" {
		a, err := parseCredentials(raw)
		if err != nil {
			return nil, err
		}
		key, err := parsePrivateKey(a.PrivateKey)
		if err != nil {
			return nil, err
		}
		f.acct, f.rsaKey, f.project = a, key, a.ProjectID
	}
	if host := strings.TrimSpace(os.Getenv("FIRESTORE_EMULATOR_HOST")); host != "" {
		f.base = "http://" + host + "/v1"
		f.acct, f.rsaKey = nil, nil
		if p := strings.TrimSpace(os.Getenv(utils.EnvFirestoreProject)); p != "" {
			f.project = p
		}
		if f.project == "" {
			f.project = "openrepl-local"
		}
		return f, nil
	}
	if f.acct == nil {
		return nil, nil
	}
	f.base = "https://firestore.googleapis.com/v1"
	return f, nil
}

// accessToken returns an OAuth token for the service account, asking Google
// for a new one a minute before the old one ends.
func (f *Firestore) accessToken() (string, error) {
	if f.acct == nil {
		return "owner", nil // the emulator accepts this as the project owner
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.expires.Add(-time.Minute)) {
		return f.token, nil
	}
	now := time.Now()
	enc := func(v interface{}) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	unsigned := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(map[string]interface{}{
		"iss": f.acct.ClientEmail, "scope": "https://www.googleapis.com/auth/datastore",
		"aud": f.acct.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.rsaKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)}}
	resp, err := f.http.PostForm(f.acct.TokenURI, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	json.Unmarshal(body, &tok)
	if resp.StatusCode != 200 || tok.AccessToken == "" {
		return "", fmt.Errorf("Google refused the service account (%d %s %s)", resp.StatusCode, tok.Error, tok.Description)
	}
	f.token = tok.AccessToken
	f.expires = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	return f.token, nil
}

// Doc is a document: its fields as the REST API writes them, and its
// updateTime, which a later Put can require to be unchanged.
type Doc struct {
	Name       string                     `json:"name"`
	Fields     map[string]json.RawMessage `json:"fields"`
	UpdateTime string                     `json:"updateTime"`
}

// the encodings of a value in the REST API
func StringValue(s string) interface{} { return map[string]string{"stringValue": s} }
func IntValue(n int64) interface{}     { return map[string]string{"integerValue": fmt.Sprint(n)} }
func BytesValue(b []byte) interface{} {
	return map[string]string{"bytesValue": base64.StdEncoding.EncodeToString(b)}
}
func TimeValue(t time.Time) interface{} {
	return map[string]string{"timestampValue": t.UTC().Format(time.RFC3339Nano)}
}

// String, Int and Bytes read a field back; they return the zero value for one
// that is not there.
func (d *Doc) String(field string) string {
	var v struct {
		S string `json:"stringValue"`
	}
	json.Unmarshal(d.Fields[field], &v)
	return v.S
}

func (d *Doc) Int(field string) int64 {
	var v struct {
		I string `json:"integerValue"`
	}
	json.Unmarshal(d.Fields[field], &v)
	var n int64
	fmt.Sscan(v.I, &n)
	return n
}

func (d *Doc) Bytes(field string) []byte {
	var v struct {
		B string `json:"bytesValue"`
	}
	json.Unmarshal(d.Fields[field], &v)
	b, _ := base64.StdEncoding.DecodeString(v.B)
	return b
}

// ID is the document's id (the last part of its name).
func (d *Doc) ID() string {
	if i := strings.LastIndex(d.Name, "/"); i >= 0 {
		return d.Name[i+1:]
	}
	return d.Name
}

type apiError struct {
	status int
	Code   string
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("firestore: %d %s %s", e.status, e.Code, e.Msg) }

// call makes one request and decodes a JSON answer into out (nil to drop it).
func (f *Firestore) call(method, path string, query url.Values, body interface{}, out interface{}) error {
	tok, err := f.accessToken()
	if err != nil {
		return err
	}
	u := f.base + "/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		json.Unmarshal(data, &e)
		return &apiError{status: resp.StatusCode, Code: e.Error.Status, Msg: e.Error.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (f *Firestore) docs() string {
	return "projects/" + url.PathEscape(f.project) + "/databases/(default)/documents"
}

func isConflict(err error) bool {
	e, ok := err.(*apiError)
	if !ok {
		return false
	}
	switch e.Code {
	case "ALREADY_EXISTS", "FAILED_PRECONDITION", "ABORTED":
		return true
	}
	return e.status == 409 || e.status == 412
}

func isNotFound(err error) bool {
	e, ok := err.(*apiError)
	return ok && (e.status == 404 || e.Code == "NOT_FOUND")
}

// Get reads a document. It returns ErrFSNotFound when there is none.
func (f *Firestore) Get(coll, id string) (*Doc, error) {
	var d Doc
	err := f.call("GET", f.docs()+"/"+url.PathEscape(coll)+"/"+url.PathEscape(id), nil, nil, &d)
	if isNotFound(err) {
		return nil, ErrFSNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// Precondition limits a Put: Exists says the document must (true) or must not
// (false) exist; UpdateTime says it must not have changed since then.
type Precondition struct {
	Exists     *bool
	UpdateTime string
}

// Put writes a document, replacing all its fields, and returns it (its name and
// updateTime; the fields are the ones written). A failed precondition is
// ErrFSConflict. It is a one-write commit, which takes the precondition in its
// body (the emulator does not read a timestamp from the PATCH query).
func (f *Firestore) Put(coll, id string, fields map[string]interface{}, pre Precondition) (*Doc, error) {
	name := f.docs() + "/" + coll + "/" + id // a resource name is not URL-escaped
	write := map[string]interface{}{"update": map[string]interface{}{"name": name, "fields": fields}}
	cur := map[string]interface{}{}
	if pre.Exists != nil {
		cur["exists"] = *pre.Exists
	}
	if pre.UpdateTime != "" {
		cur["updateTime"] = pre.UpdateTime
	}
	if len(cur) > 0 {
		write["currentDocument"] = cur
	}
	var res struct {
		WriteResults []struct {
			UpdateTime string `json:"updateTime"`
		} `json:"writeResults"`
		CommitTime string `json:"commitTime"`
	}
	err := f.call("POST", f.docs()+":commit", nil, map[string]interface{}{"writes": []interface{}{write}}, &res)
	if isConflict(err) {
		return nil, ErrFSConflict
	}
	if err != nil {
		return nil, err
	}
	d := &Doc{Name: name}
	if len(res.WriteResults) > 0 {
		d.UpdateTime = res.WriteResults[0].UpdateTime
	}
	if d.UpdateTime == "" {
		d.UpdateTime = res.CommitTime
	}
	return d, nil
}

// Delete removes a document; ErrFSNotFound when there was none.
func (f *Firestore) Delete(coll, id string) error {
	q := url.Values{"currentDocument.exists": {"true"}}
	err := f.call("DELETE", f.docs()+"/"+url.PathEscape(coll)+"/"+url.PathEscape(id), q, nil, nil)
	if isNotFound(err) || isConflict(err) {
		return ErrFSNotFound
	}
	return err
}

// List calls fn for every document of a collection, in order of their ids and
// pageSize at a time, until fn returns false. It pages with a query that starts
// after the last id seen, which works on Firestore and on its emulator (the
// list call's page tokens do not on the emulator).
func (f *Firestore) List(coll string, pageSize int, fn func(*Doc) bool) error {
	last := ""
	for {
		q := map[string]interface{}{
			"from":    []interface{}{map[string]interface{}{"collectionId": coll}},
			"orderBy": []interface{}{map[string]interface{}{"field": map[string]string{"fieldPath": "__name__"}, "direction": "ASCENDING"}},
			"limit":   pageSize,
		}
		if last != "" {
			q["startAt"] = map[string]interface{}{
				"values": []interface{}{map[string]string{"referenceValue": f.docs() + "/" + coll + "/" + last}},
				"before": false, // start after it
			}
		}
		var rows []struct {
			Document *Doc `json:"document"`
		}
		if err := f.call("POST", f.docs()+":runQuery", nil, map[string]interface{}{"structuredQuery": q}, &rows); err != nil {
			return err
		}
		n := 0
		for _, r := range rows {
			if r.Document == nil {
				continue
			}
			n++
			last = r.Document.ID()
			if !fn(r.Document) {
				return nil
			}
		}
		if n < pageSize {
			return nil
		}
	}
}

// CommitAll writes many documents at once (up to 500 a request), replacing
// whatever is there.
func (f *Firestore) CommitAll(coll string, docs map[string]map[string]interface{}) error {
	writes := []interface{}{}
	flush := func() error {
		if len(writes) == 0 {
			return nil
		}
		err := f.call("POST", f.docs()+":commit", nil, map[string]interface{}{"writes": writes}, nil)
		writes = writes[:0]
		return err
	}
	for id, fields := range docs {
		writes = append(writes, map[string]interface{}{"update": map[string]interface{}{
			"name":   f.docs() + "/" + coll + "/" + id,
			"fields": fields,
		}})
		if len(writes) >= 400 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// ---- the Store ---------------------------------------------------------------------

// docID turns a key into a document id. Firestore ids may not contain "/", be
// "." or "..", look like "__x__", be empty, or be longer than 1500 bytes; the
// keys the site uses (a uid, "worker-pin:<uid>", a blog title) mostly pass, and
// any other is stored as "~" and the key in base64.
func docID(key []byte) string {
	s := string(key)
	plain := len(key) > 0 && len(key) <= 700 && utf8.Valid(key) &&
		!strings.ContainsAny(s, "/\x00") && s != "." && s != ".." &&
		!(strings.HasPrefix(s, "__") && strings.HasSuffix(s, "__")) && !strings.HasPrefix(s, "~")
	if plain {
		return s
	}
	return "~" + base64.RawURLEncoding.EncodeToString(key)
}

func keyOfDocID(id string) []byte {
	if strings.HasPrefix(id, "~") {
		if b, err := base64.RawURLEncoding.DecodeString(id[1:]); err == nil {
			return b
		}
	}
	return []byte(id)
}

type firestoreStore struct {
	f    *Firestore
	coll string
}

func (s *firestoreStore) Backend() string { return "firestore" }
func (s *firestoreStore) Commit() error   { return nil }
func (s *firestoreStore) Rollback() error { return nil }
func (s *firestoreStore) Close() error    { return nil }

func (s *firestoreStore) fields(value []byte) map[string]interface{} {
	return map[string]interface{}{"v": BytesValue(value), "t": TimeValue(time.Now())}
}

func (s *firestoreStore) Store(key, value []byte) error {
	_, err := s.f.Put(s.coll, docID(key), s.fields(value), Precondition{})
	return err
}

func (s *firestoreStore) Fetch(key []byte) ([]byte, error) {
	d, err := s.f.Get(s.coll, docID(key))
	if err == ErrFSNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return d.Bytes("v"), nil
}

func (s *firestoreStore) Delete(key []byte) error {
	err := s.f.Delete(s.coll, docID(key))
	if err == ErrFSNotFound {
		return ErrNotFound
	}
	return err
}

func (s *firestoreStore) Each(fn func(key, value []byte) bool) error {
	return s.f.List(s.coll, 300, func(d *Doc) bool {
		return fn(keyOfDocID(d.ID()), d.Bytes("v"))
	})
}

// copyOnce fills an empty collection from the unqlite file at path, as the
// MongoDB store does.
func (s *firestoreStore) copyOnce(path string) error {
	empty := true
	if err := s.f.List(s.coll, 1, func(*Doc) bool { empty = false; return false }); err != nil {
		return err
	}
	if !empty {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	local, err := openUnqlite(path)
	if err != nil {
		return err
	}
	defer local.Close()
	docs := map[string]map[string]interface{}{}
	if err := local.Each(func(k, v []byte) bool {
		docs[docID(k)] = s.fields(append([]byte(nil), v...))
		return true
	}); err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	if err := s.f.CommitAll(s.coll, docs); err != nil {
		return err
	}
	logf("persist: copied %d records from %s into Firestore (done once; Firestore now holds them)", len(docs), path)
	return nil
}
