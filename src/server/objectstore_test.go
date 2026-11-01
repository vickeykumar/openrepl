package server

import (
	"strings"
	"testing"

	"objstore"
	"utils"
)

// The object store (S3, R2) in the dashboard: its two keys are kept encrypted, checked with the bucket, and the
// Health page says whether the bucket answers.

func s3Env(t *testing.T, f *objstore.Fake) {
	t.Helper()
	t.Setenv("OPENREPL_S3_ENDPOINT", f.URL)
	t.Setenv("OPENREPL_S3_BUCKET", objstore.FakeBucket)
	t.Setenv("OPENREPL_S3_ACCESS_KEY_ID", "")
	t.Setenv("OPENREPL_S3_SECRET_ACCESS_KEY", "")
	t.Cleanup(func() { utils.SetS3Overrides("", "") })
}

func TestTheObjectStoreKeysAreKeptCheckedAndNeverComeBack(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	f := objstore.NewFake()
	defer f.Close()
	s3Env(t, f)

	if utils.S3Enabled() {
		t.Fatal("enabled without keys")
	}
	// the first key cannot be checked alone
	w := postKey(h, "s3-access", objstore.FakeAccess)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "could not be checked") {
		t.Fatalf("the key id: %d %s", w.Code, w.Body.String())
	}
	// a secret that the bucket refuses is not kept
	if w := postKey(h, "s3-secret", "a-wrong-secret-0123456789"); w.Code != 400 {
		t.Fatalf("a wrong secret: %d %s", w.Code, w.Body.String())
	}
	if utils.S3SecretKey() != "" {
		t.Error("a refused secret is in use")
	}
	if w := postKey(h, "s3-secret", objstore.FakeSecret); w.Code != 200 {
		t.Fatalf("the secret: %d %s", w.Code, w.Body.String())
	}
	if !utils.S3Enabled() || utils.KeySource("s3-secret") != "dashboard" || utils.KeySource("s3-access") != "dashboard" {
		t.Fatalf("not in use: %v", utils.S3Enabled())
	}
	for _, path := range []string{"/admin/keys", "/admin/settings", "/admin/health", "/admin/audit"} {
		body := get(h, path).Body.String()
		if strings.Contains(body, objstore.FakeSecret) || strings.Contains(body, GetSiteSettings().Secrets.S3Secret) {
			t.Errorf("%s shows the secret or its ciphertext", path)
		}
	}
	var health struct {
		Checks []healthCheck `json:"checks"`
	}
	decode(t, get(h, "/admin/health"), &health)
	var found bool
	for _, c := range health.Checks {
		if c.Name == "Object storage" {
			found = true
			if c.Status != healthOK || !strings.Contains(c.Detail, "answers") {
				t.Errorf("object storage: %+v", c)
			}
		}
	}
	if !found {
		t.Error("no Object storage row in Health")
	}
	if w := postKey(h, "s3-secret", ""); w.Code != 200 || utils.S3Enabled() {
		t.Errorf("removing: %d, enabled %v", w.Code, utils.S3Enabled())
	}
}

func TestHealthSaysWhenTheObjectStoreIsSetUpInPartOrNotAtAll(t *testing.T) {
	f := objstore.NewFake()
	defer f.Close()
	s3Env(t, f)
	t.Setenv("OPENREPL_S3_ENDPOINT", "")
	t.Setenv("OPENREPL_S3_BUCKET", "")
	objectStorageCache = objectStorageState{}
	if st := objectStorageHealth(); st.status != healthOK || !strings.Contains(st.detail, "not set") {
		t.Errorf("none: %+v", st)
	}
	t.Setenv("OPENREPL_S3_ENDPOINT", f.URL)
	t.Setenv("OPENREPL_S3_BUCKET", objstore.FakeBucket)
	t.Setenv("OPENREPL_S3_ACCESS_KEY_ID", "only-the-id")
	objectStorageCache = objectStorageState{}
	if st := objectStorageHealth(); st.status != healthWarn || !strings.Contains(st.detail, "all needed") {
		t.Errorf("in part: %+v", st)
	}
	t.Setenv("OPENREPL_S3_ACCESS_KEY_ID", objstore.FakeAccess)
	t.Setenv("OPENREPL_S3_SECRET_ACCESS_KEY", "the-wrong-secret-0123456789")
	objectStorageCache = objectStorageState{}
	if st := objectStorageHealth(); st.status != healthFail || !strings.Contains(st.detail, "does not answer") {
		t.Errorf("wrong keys: %+v", st)
	}
	t.Setenv("OPENREPL_S3_SECRET_ACCESS_KEY", objstore.FakeSecret)
	objectStorageCache = objectStorageState{}
	if st := objectStorageHealth(); st.status != healthOK {
		t.Errorf("right keys: %+v", st)
	}
	objectStorageCache = objectStorageState{}
}
