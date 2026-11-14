package objstore

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fake is a bucket in memory (for the tests of this package and of the ones that use an object store) that speaks as much of the S3 API as the client uses, checks the signature of
// every request (with the signer of the package, which is itself checked against the example of the AWS
// documentation in sigv4_test.go), and can be told to fail.
type Fake struct {
	*httptest.Server
	mu       sync.Mutex
	objects  map[string]fakeObject
	pageSize int
	fail     int  // the next requests that get a 503
	cutPuts  bool // a PUT stores nothing and drops the connection
	requests []string
	// Now is the clock of the bucket (the time an object is stored); nil: the real one
	Now func() time.Time
	// versioned makes the bucket keep what an overwrite replaces and hide, not remove, what is deleted (as B2 does)
	versioned bool
	versions  map[string][]fakeVersion // key -> versions, oldest first
	nextID    int
}

type fakeVersion struct {
	id     string
	object fakeObject
	marker bool
	at     time.Time
}

type fakeObject struct {
	data []byte
	meta map[string]string
	at   time.Time
	typ  string
}

// the keys and the bucket of a Fake
const (
	FakeAccess = "AKIATESTKEY"
	FakeSecret = "test-secret-key-0123456789"
	FakeBucket = "bucket"
)

func NewFake() *Fake {
	f := &Fake{objects: map[string]fakeObject{}, pageSize: 1000}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// SetVersioned makes the bucket keep every version of a key and hide a deleted one behind a delete marker, as a versioned bucket
// does; the listing of versions (ListVersions) and the read of one version (GetVersion) work only then.
func (f *Fake) SetVersioned(on bool) {
	f.mu.Lock()
	f.versioned = on
	if on && f.versions == nil {
		f.versions = map[string][]fakeVersion{}
	}
	f.mu.Unlock()
}

// Age moves the time of every stored version of a key back by d (for tests of what is old).
func (f *Fake) Age(key string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key = f.cfgPrefix() + key
	if o, ok := f.objects[key]; ok {
		o.at = o.at.Add(-d)
		f.objects[key] = o
	}
	vs := f.versions[key]
	for i := range vs {
		vs[i].at = vs[i].at.Add(-d)
		vs[i].object.at = vs[i].at
	}
}

func (f *Fake) cfgPrefix() string { return f.Config().Prefix + "/" }

func (f *Fake) now() time.Time {
	if f.Now != nil {
		return f.Now().UTC()
	}
	return time.Now().UTC()
}

func (f *Fake) Config() Config {
	return Config{Endpoint: f.URL, Region: "auto", Bucket: FakeBucket, AccessKey: FakeAccess, Secret: FakeSecret, Prefix: "pfx"}
}

func (f *Fake) authorized(r *http.Request, body []byte) bool {
	got := r.Header.Get("Authorization")
	want := r.Clone(r.Context())
	want.Header = r.Header.Clone()
	want.Header.Del("Authorization")
	want.URL.Host = r.Host
	t, err := time.Parse(timeFormat, r.Header.Get("x-amz-date"))
	if err != nil {
		return false
	}
	payload := emptySHA256
	if len(body) > 0 {
		payload = hashHex(body)
	}
	if r.Header.Get("x-amz-content-sha256") != payload {
		return false
	}
	sign(want, FakeAccess, FakeSecret, "auto", payload, t)
	return got == want.Header.Get("Authorization")
}

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := ioutil.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	if f.fail > 0 {
		f.fail--
		http.Error(w, "<Error><Code>SlowDown</Code><Message>slow down</Message></Error>", http.StatusServiceUnavailable)
		return
	}
	if !f.authorized(r, body) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<Error><Code>SignatureDoesNotMatch</Code><Message>the signature is wrong</Message></Error>")
		return
	}
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/"+FakeBucket)
	path = strings.TrimPrefix(path, "/")
	key, _ := unescape(path)
	switch r.Method {
	case http.MethodPut:
		if f.cutPuts {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		meta := map[string]string{}
		for k, v := range r.Header {
			if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-amz-meta-") {
				meta[strings.TrimPrefix(lk, "x-amz-meta-")] = v[0]
			}
		}
		obj := fakeObject{data: body, meta: meta, at: f.now(), typ: r.Header.Get("Content-Type")}
		f.objects[key] = obj
		if f.versioned {
			f.nextID++
			id := fmt.Sprintf("v%d", f.nextID)
			f.versions[key] = append(f.versions[key], fakeVersion{id: id, object: obj, at: obj.at})
			w.Header().Set("x-amz-version-id", id)
		}
		sum := md5.Sum(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	case http.MethodGet, http.MethodHead:
		if key == "" {
			if _, ok := r.URL.Query()["versions"]; ok {
				f.listVersions(w, r)
				return
			}
			f.list(w, r)
			return
		}
		o, ok := f.objects[key]
		if id := r.URL.Query().Get("versionId"); id != "" {
			ok = false
			for _, v := range f.versions[key] {
				if v.id == id && !v.marker {
					o, ok = v.object, true
				}
			}
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				fmt.Fprint(w, "<Error><Code>NoSuchKey</Code><Message>no such key</Message></Error>")
			}
			return
		}
		sum := md5.Sum(o.data)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		w.Header().Set("Last-Modified", o.at.Format(http.TimeFormat))
		w.Header().Set("Content-Length", fmt.Sprint(len(o.data)))
		for k, v := range o.meta {
			w.Header().Set("x-amz-meta-"+k, v)
		}
		if r.Method == http.MethodGet {
			w.Write(o.data)
		}
	case http.MethodDelete:
		if id := r.URL.Query().Get("versionId"); id != "" {
			// one version goes for good (the current one is never asked for by the callers; here it just goes from the list)
			vs := f.versions[key]
			for i := range vs {
				if vs[i].id == id {
					f.versions[key] = append(vs[:i:i], vs[i+1:]...)
					break
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		delete(f.objects, key)
		if f.versioned {
			f.nextID++
			f.versions[key] = append(f.versions[key], fakeVersion{id: fmt.Sprintf("v%d", f.nextID), marker: true, at: f.now()})
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func unescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			var c byte
			fmt.Sscanf(s[i+1:i+3], "%02X", &c)
			b.WriteByte(c)
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

func (f *Fake) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	start := 0
	if tok := q.Get("continuation-token"); tok != "" {
		fmt.Sscan(tok, &start)
	}
	size := f.pageSize
	if mk := q.Get("max-keys"); mk != "" {
		fmt.Sscan(mk, &size)
	}
	end := start + size
	truncated := end < len(keys)
	if end > len(keys) {
		end = len(keys)
	}
	type content struct {
		Key          string
		Size         int
		ETag         string
		LastModified string
	}
	res := struct {
		XMLName   xml.Name `xml:"ListBucketResult"`
		Truncated bool     `xml:"IsTruncated"`
		Next      string   `xml:"NextContinuationToken,omitempty"`
		Contents  []content
	}{Truncated: truncated}
	if truncated {
		res.Next = fmt.Sprint(end)
	}
	for _, k := range keys[start:end] {
		o := f.objects[k]
		res.Contents = append(res.Contents, content{Key: k, Size: len(o.data), ETag: `"x"`, LastModified: o.at.Format(time.RFC3339)})
	}
	xml.NewEncoder(w).Encode(res)
}

// Stored lists the keys that are in the bucket, as the server sees them (with the prefix).
func (f *Fake) Stored() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for k := range f.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SetPageSize makes a listing give at most n keys a page.
func (f *Fake) SetPageSize(n int) { f.mu.Lock(); f.pageSize = n; f.mu.Unlock() }

// FailNext makes the next n requests get a 503.
func (f *Fake) FailNext(n int) { f.mu.Lock(); f.fail = n; f.mu.Unlock() }

// CutPuts makes every PUT drop the connection and store nothing.
func (f *Fake) CutPuts(on bool) { f.mu.Lock(); f.cutPuts = on; f.mu.Unlock() }

// Requests lists what was asked of it, "METHOD /path?query".
func (f *Fake) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// Object returns what is stored under a key of the bucket, with the prefix.
func (f *Fake) Object(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.objects[key]
	return o.data, ok
}

// Set stores an object directly, with a time, as if it had been put then (for tests of retention).
func (f *Fake) Set(key string, data []byte, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = fakeObject{data: data, meta: map[string]string{}, at: at.UTC()}
}

// listVersions answers ?versions: every version of every key under the prefix (newest first for a key), with delete markers.
func (f *Fake) listVersions(w http.ResponseWriter, r *http.Request) {
	if !f.versioned {
		w.WriteHeader(http.StatusNotImplemented)
		fmt.Fprint(w, "<Error><Code>NotImplemented</Code><Message>no versions here</Message></Error>")
		return
	}
	prefix := r.URL.Query().Get("prefix")
	var keys []string
	for k := range f.versions {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	type item struct {
		Key          string
		VersionId    string
		IsLatest     bool
		LastModified string
		Size         int
	}
	res := struct {
		XMLName      xml.Name `xml:"ListVersionsResult"`
		IsTruncated  bool
		Version      []item
		DeleteMarker []item
	}{}
	for _, k := range keys {
		vs := f.versions[k]
		for i := len(vs) - 1; i >= 0; i-- {
			v := vs[i]
			it := item{Key: k, VersionId: v.id, IsLatest: i == len(vs)-1, LastModified: v.at.Format(time.RFC3339), Size: len(v.object.data)}
			if v.marker {
				res.DeleteMarker = append(res.DeleteMarker, it)
			} else {
				res.Version = append(res.Version, it)
			}
		}
	}
	xml.NewEncoder(w).Encode(res)
}
