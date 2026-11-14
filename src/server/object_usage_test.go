package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"objstore"
)

func TestHumanSizeAndTheUsageLine(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 900: "900 B", 2048: "2.0 KB", 5 << 20: "5.0 MB", 390 << 20: "390.0 MB", 3 << 30: "3.0 GB"} {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
	if got := groupDigits(1234567); got != "1,234,567" {
		t.Errorf("groupDigits = %q", got)
	}
	u := objstore.Usage{Bytes: 390 << 20, Objects: 1234, OlderBytes: 270 << 20, OlderVersions: 80, VersionsKnown: true}
	text := objectUsageText(u)
	for _, want := range []string{"390.0 MB in 1,234 objects", "270.0 MB in 80 older versions"} {
		if !strings.Contains(text, want) {
			t.Errorf("usage text %q lacks %q", text, want)
		}
	}
	if got := objectUsageShort(u); got != "660.0 MB" {
		t.Errorf("short = %q", got)
	}
	u.Truncated = true
	if got := objectUsageShort(u); !strings.HasPrefix(got, "at least ") {
		t.Errorf("a cut count: %q", got)
	}
}

// The map's object store box counts the bucket in the background and then shows what is stored.
func TestTheObjectStoreBoxShowsWhatTheBucketHolds(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s3Env(t, f)
	t.Setenv("OPENREPL_S3_ACCESS_KEY_ID", objstore.FakeAccess)
	t.Setenv("OPENREPL_S3_SECRET_ACCESS_KEY", objstore.FakeSecret)
	objectStorageCache = objectStorageState{}
	objectUsage.mu.Lock()
	objectUsage.has, objectUsage.running, objectUsage.err, objectUsage.at, objectUsage.key = false, false, "", time.Time{}, ""
	objectUsage.mu.Unlock()
	t.Cleanup(func() { objectStorageCache = objectStorageState{} })

	c, err := objstore.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(context.Background(), "workspaces/a/1", make([]byte, 3<<20), "", nil); err != nil {
		t.Fatal(err)
	}
	// the first look starts the count and says so
	node := objectStorageNode()
	var stored string
	for _, r := range node.Rows {
		if r.Label == "Stored" {
			stored = r.Value
		}
	}
	if stored == "" {
		t.Fatalf("no Stored row: %+v", node.Rows)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stored, "3.0 MB") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		stored = ""
		node = objectStorageNode()
		for _, r := range node.Rows {
			if r.Label == "Stored" {
				stored = r.Value
			}
		}
	}
	if !strings.Contains(stored, "3.0 MB in 1 object") {
		t.Fatalf("Stored row: %q", stored)
	}
	if !strings.Contains(node.Text, "3.0 MB stored") {
		t.Errorf("the line under the box: %q", node.Text)
	}
}
