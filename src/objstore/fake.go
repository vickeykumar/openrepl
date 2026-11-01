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
		f.objects[key] = fakeObject{data: body, meta: meta, at: time.Now().UTC(), typ: r.Header.Get("Content-Type")}
		sum := md5.Sum(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	case http.MethodGet, http.MethodHead:
		if key == "" {
			f.list(w, r)
			return
		}
		o, ok := f.objects[key]
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
		delete(f.objects, key)
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
