package objstore

import (
	"os"
	"strings"
	"testing"
)

// A run against a real S3 API server (RustFS, MinIO, R2, S3), when OPENREPL_TEST_S3_ENDPOINT, _BUCKET,
// _ACCESS_KEY_ID and _SECRET_ACCESS_KEY are set: the bucket has to exist. It writes below a prefix of its own
// and removes what it wrote.
func TestAgainstARealServer(t *testing.T) {
	endpoint, bucket := os.Getenv("OPENREPL_TEST_S3_ENDPOINT"), os.Getenv("OPENREPL_TEST_S3_BUCKET")
	access, secret := os.Getenv("OPENREPL_TEST_S3_ACCESS_KEY_ID"), os.Getenv("OPENREPL_TEST_S3_SECRET_ACCESS_KEY")
	if endpoint == "" || bucket == "" || access == "" || secret == "" {
		t.Skip("no real S3 server given (OPENREPL_TEST_S3_*)")
	}
	c, err := New(Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: access, Secret: secret, Prefix: "go-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	keys := []string{"plain.txt", "dir/with space/é+&=.bin", "backups/prod/20261101T143000Z.tar.gz"}
	for i, k := range keys {
		data := []byte(strings.Repeat("x", 1000*(i+1)))
		if err := c.Put(ctx, k, data, "application/octet-stream", map[string]string{"sha256": "abc", "generation": "3"}); err != nil {
			t.Fatalf("put %q: %v", k, err)
		}
		got, obj, err := c.Get(ctx, k)
		if err != nil || string(got) != string(data) || obj.Meta["sha256"] != "abc" || obj.Meta["generation"] != "3" {
			t.Fatalf("get %q: %d bytes %+v %v", k, len(got), obj, err)
		}
		head, err := c.Head(ctx, k)
		if err != nil || head.Size != int64(len(data)) || head.LastModified.IsZero() {
			t.Fatalf("head %q: %+v %v", k, head, err)
		}
	}
	listed, err := c.List(ctx, "")
	if err != nil || len(listed) != len(keys) {
		t.Fatalf("list: %v %v", listed, err)
	}
	only, err := c.List(ctx, "backups/")
	if err != nil || len(only) != 1 || only[0].Key != keys[2] {
		t.Fatalf("list with a prefix: %v %v", only, err)
	}
	for _, k := range keys {
		if err := c.Delete(ctx, k); err != nil {
			t.Fatalf("delete %q: %v", k, err)
		}
		if _, err := c.Head(ctx, k); err != ErrNotFound {
			t.Errorf("after delete %q: %v", k, err)
		}
	}
	bad := *c
	bad.cfg.Secret = "wrong"
	if err := bad.Check(ctx); err == nil {
		t.Error("wrong keys were accepted")
	}
}
