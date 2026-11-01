// Package objstore is a small client of the S3 API (Cloudflare R2, AWS S3, MinIO, RustFS), with nothing but
// the standard library, like persist's Firestore client: the official SDK would bring a long list of modules
// into this GOPATH build. It does what OpenREPL needs of an object store: put, get, head, list and delete of
// whole objects, addressed path style (https://endpoint/bucket/key), which every one of them takes.
package objstore

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is where the objects are and how to sign in. Prefix, if set, is put in front of every key (it keeps
// several deployments in one bucket apart); it is not part of the keys the caller sees.
type Config struct {
	Endpoint  string // https://<account>.r2.cloudflarestorage.com, or http://127.0.0.1:9000
	Region    string // "auto" for R2
	Bucket    string
	AccessKey string
	Secret    string
	Prefix    string
	HTTP      *http.Client
}

// Client is a connection to one bucket.
type Client struct {
	cfg   Config
	base  *url.URL
	http  *http.Client
	now   func() time.Time
	sleep func(time.Duration)
}

// ErrNotFound is what Get, Head and Delete answer for a key that is not there.
var ErrNotFound = errors.New("objstore: no such key")

// Error is an answer that is not a success.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("objstore: %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("objstore: %d %s", e.Status, e.Message)
}

// Object is what is known of a stored object.
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	Meta         map[string]string // x-amz-meta-*, keys in lower case
}

// New checks the settings and makes a client. Nothing is sent yet.
func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.Secret == "" {
		return nil, errors.New("objstore: the endpoint, the bucket and both keys are needed")
	}
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("objstore: %q is not an endpoint (it looks like https://account.r2.cloudflarestorage.com)", cfg.Endpoint)
	}
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{cfg: cfg, base: u, http: hc, now: time.Now, sleep: time.Sleep}, nil
}

// Bucket is the name of the bucket.
func (c *Client) Bucket() string { return c.cfg.Bucket }

func (c *Client) full(key string) string {
	if c.cfg.Prefix == "" {
		return key
	}
	return c.cfg.Prefix + "/" + key
}

func (c *Client) strip(key string) string {
	if c.cfg.Prefix == "" {
		return key
	}
	return strings.TrimPrefix(key, c.cfg.Prefix+"/")
}

func (c *Client) url(key string, q url.Values) *url.URL {
	u := *c.base
	path := "/" + uriEncode(c.cfg.Bucket, false)
	if key != "" {
		path += "/" + uriEncode(c.full(key), true)
	}
	u.Path = ""
	u.RawPath = ""
	u.Opaque = ""
	// the path is built encoded, and parsed back so that EscapedPath gives it as it is
	parsed, _ := url.Parse(u.Scheme + "://" + u.Host + path)
	if len(q) > 0 {
		parsed.RawQuery = canonicalQuery(q)
	}
	return parsed
}

// do sends one request, signed, and tries again a few times when the answer says to (a 5xx, a 429, a lost
// connection). A body is read again from body.
func (c *Client) do(ctx context.Context, method string, u *url.URL, body []byte, hdr map[string]string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			c.sleep(time.Duration(attempt*attempt) * 300 * time.Millisecond)
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.ContentLength = int64(len(body))
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		payload := emptySHA256
		if body != nil {
			payload = hashHex(body)
		}
		sign(req, c.cfg.AccessKey, c.cfg.Secret, c.cfg.Region, payload, c.now())
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = readError(resp)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// readError turns an answer that is not a success into an Error and closes it.
func readError(resp *http.Response) error {
	defer resp.Body.Close()
	data, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 8192))
	e := &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	var x struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(data, &x) == nil && x.Code != "" {
		e.Code, e.Message = x.Code, x.Message
	}
	return e
}

func objectFrom(key string, h http.Header) *Object {
	o := &Object{Key: key, ETag: strings.Trim(h.Get("ETag"), `"`), Meta: map[string]string{}}
	fmt.Sscan(h.Get("Content-Length"), &o.Size)
	if t, err := http.ParseTime(h.Get("Last-Modified")); err == nil {
		o.LastModified = t
	}
	for k, v := range h {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-amz-meta-") && len(v) > 0 {
			o.Meta[strings.TrimPrefix(lk, "x-amz-meta-")] = v[0]
		}
	}
	return o
}

// Put stores data under key, replacing what was there. Meta is kept with the object (names in lower case).
func (c *Client) Put(ctx context.Context, key string, data []byte, contentType string, meta map[string]string) error {
	hdr := map[string]string{}
	if contentType != "" {
		hdr["Content-Type"] = contentType
	}
	for k, v := range meta {
		hdr["x-amz-meta-"+strings.ToLower(k)] = v
	}
	if data == nil {
		data = []byte{}
	}
	resp, err := c.do(ctx, http.MethodPut, c.url(key, nil), data, hdr)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return readError(resp)
	}
	resp.Body.Close()
	return nil
}

// Get reads the whole object.
func (c *Client) Get(ctx context.Context, key string) ([]byte, *Object, error) {
	resp, err := c.do(ctx, http.MethodGet, c.url(key, nil), nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, nil, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return nil, nil, readError(resp)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return data, objectFrom(key, resp.Header), nil
}

// Head tells about an object without reading it.
func (c *Client) Head(ctx context.Context, key string) (*Object, error) {
	resp, err := c.do(ctx, http.MethodHead, c.url(key, nil), nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode/100 != 2 {
		return nil, &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	return objectFrom(key, resp.Header), nil
}

// Delete removes an object. A key that is not there is not an error (S3 answers 204 either way).
func (c *Client) Delete(ctx context.Context, key string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.url(key, nil), nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return readError(resp)
	}
	resp.Body.Close()
	return nil
}

// List returns every object whose key starts with prefix, in key order. It follows the pages; more than
// maxObjects is an error (a listing that is cut short must never be taken for the whole).
func (c *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	const maxObjects = 20000
	var out []Object
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {c.full(prefix)}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := c.do(ctx, http.MethodGet, c.url("", q), nil, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 != 2 {
			return nil, readError(resp)
		}
		var page struct {
			Truncated bool   `xml:"IsTruncated"`
			Next      string `xml:"NextContinuationToken"`
			Contents  []struct {
				Key          string `xml:"Key"`
				Size         int64  `xml:"Size"`
				ETag         string `xml:"ETag"`
				LastModified string `xml:"LastModified"`
			} `xml:"Contents"`
		}
		err = xml.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("objstore: the listing could not be read: %v", err)
		}
		for _, o := range page.Contents {
			t, _ := time.Parse(time.RFC3339, o.LastModified)
			out = append(out, Object{Key: c.strip(o.Key), Size: o.Size, ETag: strings.Trim(o.ETag, `"`), LastModified: t})
		}
		if len(out) > maxObjects {
			return nil, errors.New("objstore: the listing is too long to be used")
		}
		if !page.Truncated || page.Next == "" {
			return out, nil
		}
		token = page.Next
	}
}

// Check asks whether the bucket answers and the keys are accepted (a listing of one key).
func (c *Client) Check(ctx context.Context) error {
	q := url.Values{"list-type": {"2"}, "max-keys": {"1"}}
	resp, err := c.do(ctx, http.MethodGet, c.url("", q), nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return readError(resp)
	}
	resp.Body.Close()
	return nil
}
