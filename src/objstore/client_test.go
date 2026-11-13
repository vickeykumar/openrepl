package objstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T) (*Client, *Fake) {
	t.Helper()
	f := NewFake()
	t.Cleanup(f.Close)
	c, err := New(f.Config())
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(time.Duration) {}
	return c, f
}

var ctx = context.Background()

func TestPutGetHeadDeleteWithAPrefixAndAnAwkwardKey(t *testing.T) {
	c, f := newTestClient(t)
	key := "homes/anna 01/2026-11-01T14:30:00Z+x.tar.gz"
	if err := c.Put(ctx, key, []byte("hello"), "application/gzip", map[string]string{"SHA256": "abc", "generation": "7"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Stored(); len(got) != 1 || got[0] != "pfx/"+key {
		t.Fatalf("stored: %v", got)
	}
	data, obj, err := c.Get(ctx, key)
	if err != nil || string(data) != "hello" || obj.Size != 5 || obj.Meta["sha256"] != "abc" || obj.Meta["generation"] != "7" || obj.Key != key {
		t.Fatalf("get: %q %+v %v", data, obj, err)
	}
	head, err := c.Head(ctx, key)
	if err != nil || head.Size != 5 || head.Meta["sha256"] != "abc" || head.LastModified.IsZero() {
		t.Fatalf("head: %+v %v", head, err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
	if _, err := c.Head(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Errorf("head after delete: %v", err)
	}
	if err := c.Delete(ctx, key); err != nil {
		t.Errorf("deleting a key that is not there: %v", err)
	}
}

func TestAnEmptyObjectCanBeStored(t *testing.T) {
	c, _ := newTestClient(t)
	if err := c.Put(ctx, "empty", nil, "", nil); err != nil {
		t.Fatal(err)
	}
	data, obj, err := c.Get(ctx, "empty")
	if err != nil || len(data) != 0 || obj.Size != 0 {
		t.Fatalf("%q %+v %v", data, obj, err)
	}
}

func TestListFollowsThePagesAndStripsThePrefix(t *testing.T) {
	c, f := newTestClient(t)
	f.SetPageSize(2)
	for i := 0; i < 5; i++ {
		if err := c.Put(ctx, fmt.Sprintf("backups/a/%02d", i), []byte{byte(i)}, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	c.Put(ctx, "backups/b/00", []byte("other"), "", nil)
	got, err := c.List(ctx, "backups/a/")
	if err != nil || len(got) != 5 {
		t.Fatalf("%d objects, %v", len(got), err)
	}
	for i, o := range got {
		if o.Key != fmt.Sprintf("backups/a/%02d", i) || o.Size != 1 || o.LastModified.IsZero() {
			t.Errorf("object %d: %+v", i, o)
		}
	}
	paged := 0
	for _, r := range f.Requests() {
		if strings.Contains(r, "continuation-token") {
			paged++
		}
	}
	if paged != 2 {
		t.Errorf("pages followed: %d (%v)", paged, f.Requests())
	}
	if none, err := c.List(ctx, "nothing/"); err != nil || len(none) != 0 {
		t.Errorf("an empty listing: %v %v", none, err)
	}
}

func TestAServerThatFailsAFewTimesIsTriedAgain(t *testing.T) {
	c, f := newTestClient(t)
	f.FailNext(2)
	if err := c.Put(ctx, "k", []byte("v"), "", nil); err != nil {
		t.Fatalf("two 503s then success: %v", err)
	}
	f.FailNext(10)
	err := c.Put(ctx, "k2", []byte("v"), "", nil)
	var e *Error
	if !errors.As(err, &e) || e.Status != 503 || e.Code != "SlowDown" {
		t.Fatalf("a server that stays down: %v", err)
	}
	if len(f.Stored()) != 1 {
		t.Errorf("stored: %v", f.Stored())
	}
}

func TestAPutThatDiesLeavesNothingBehind(t *testing.T) {
	c, f := newTestClient(t)
	f.CutPuts(true)
	if err := c.Put(ctx, "snap", []byte("half"), "", nil); err == nil {
		t.Fatal("a cut connection was taken for success")
	}
	if len(f.Stored()) != 0 {
		t.Errorf("stored: %v", f.Stored())
	}
}

func TestWrongKeysAreRefusedWithTheServersReason(t *testing.T) {
	f := NewFake()
	defer f.Close()
	cfg := f.Config()
	cfg.Secret = "not-the-secret"
	c, _ := New(cfg)
	c.sleep = func(time.Duration) {}
	err := c.Check(ctx)
	var e *Error
	if !errors.As(err, &e) || e.Status != 403 || e.Code != "SignatureDoesNotMatch" {
		t.Fatalf("%v", err)
	}
	good, _ := New(f.Config())
	if err := good.Check(ctx); err != nil {
		t.Fatalf("right keys: %v", err)
	}
}

func TestNewChecksItsSettings(t *testing.T) {
	for name, cfg := range map[string]Config{
		"no endpoint": {Bucket: "b", AccessKey: "a", Secret: "s"},
		"no bucket":   {Endpoint: "https://x", AccessKey: "a", Secret: "s"},
		"no keys":     {Endpoint: "https://x", Bucket: "b"},
		"no scheme":   {Endpoint: "x.example.com", Bucket: "b", AccessKey: "a", Secret: "s"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	c, err := New(Config{Endpoint: "https://x.example.com/", Bucket: "b", AccessKey: "a", Secret: "s", Prefix: "/p/q/"})
	if err != nil || c.cfg.Region != "auto" || c.cfg.Prefix != "p/q" {
		t.Errorf("%+v %v", c, err)
	}
}

func TestVersionsOfAnOverwrittenKeyAreListedAndReadOne_ByOne(t *testing.T) {
	f := NewFake()
	defer f.Close()
	c, err := New(f.Config())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// a store that keeps no versions says so
	if _, err := c.ListVersions(ctx, "h/"); err != ErrNoVersions {
		t.Fatalf("an unversioned store: %v", err)
	}
	f.SetVersioned(true)
	for _, body := range []string{"one", "two", "three"} {
		if err := c.Put(ctx, "h/day.tar.gz", []byte(body), "application/gzip", map[string]string{"sha256": body}); err != nil {
			t.Fatal(err)
		}
	}
	c.Put(ctx, "other/x", []byte("x"), "", nil)
	vs, err := c.ListVersions(ctx, "h/")
	if err != nil || len(vs) != 3 {
		t.Fatalf("%+v %v", vs, err)
	}
	if !vs[0].IsLatest || vs[1].IsLatest || vs[0].Key != "h/day.tar.gz" || vs[0].Size != 5 {
		t.Errorf("the newest first: %+v", vs)
	}
	got := map[string]string{}
	for _, v := range vs {
		data, obj, err := c.GetVersion(ctx, v.Key, v.VersionID)
		if err != nil {
			t.Fatal(err)
		}
		got[string(data)] = obj.Meta["sha256"]
	}
	if len(got) != 3 || got["one"] != "one" || got["three"] != "three" {
		t.Errorf("each version has its own body and notes: %v", got)
	}
	if _, _, err := c.GetVersion(ctx, "h/day.tar.gz", "nope"); err != ErrNotFound {
		t.Errorf("a version that is not there: %v", err)
	}
	// a delete hides the key and keeps its versions
	c.Delete(ctx, "h/day.tar.gz")
	if _, _, err := c.Get(ctx, "h/day.tar.gz"); err != ErrNotFound {
		t.Errorf("a deleted key: %v", err)
	}
	vs, _ = c.ListVersions(ctx, "h/")
	marks := 0
	for _, v := range vs {
		if v.DeleteMarker {
			marks++
		}
	}
	if len(vs) != 4 || marks != 1 {
		t.Errorf("versions after a delete: %+v", vs)
	}
}
