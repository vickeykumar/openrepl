package server

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"persist"
)

// These run against a real MongoDB, and only when OPENREPL_TEST_MONGODB_URI
// names one (for example mongodb://127.0.0.1:27017). Each test uses a database
// of its own, which it drops.
func mongoTestStore(t *testing.T) *mongoSettingsStore {
	t.Helper()
	uri := os.Getenv("OPENREPL_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("OPENREPL_TEST_MONGODB_URI is not set")
	}
	db := fmt.Sprintf("openrepl_test_%d", time.Now().UnixNano())
	store, err := newMongoSettingsStore(uri, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), mongoCallTimeout)
		defer cancel()
		store.client.Database(db).Drop(ctx)
		store.client.Disconnect(ctx)
	})
	if err := store.ping(); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestMongoStoreVersions(t *testing.T) {
	s := mongoTestStore(t)
	if _, _, found, err := s.Load(); err != nil || found {
		t.Fatalf("empty database: found=%v err=%v", found, err)
	}
	if v, err := s.Save([]byte(`{"a":1}`), 0); err != nil || v != 1 {
		t.Fatalf("first save: %d %v", v, err)
	}
	if _, err := s.Save([]byte(`{"a":2}`), 0); err != errSettingsConflict {
		t.Fatalf("a second insert: %v", err)
	}
	if v, err := s.Save([]byte(`{"a":3}`), 1); err != nil || v != 2 {
		t.Fatalf("update: %d %v", v, err)
	}
	if _, err := s.Save([]byte(`{"a":4}`), 1); err != errSettingsConflict {
		t.Fatalf("a stale update: %v", err)
	}
	data, v, found, err := s.Load()
	if err != nil || !found || v != 2 || string(data) != `{"a":3}` {
		t.Fatalf("load: %q %d %v %v", data, v, found, err)
	}
}

// the whole path: upload of settings.json, then the store wins, then a change
// from another instance is picked up
func TestMongoSyncEndToEnd(t *testing.T) {
	s := mongoTestStore(t)
	withStore(t, &fakeStore{})
	settingsMu.Lock()
	settingsBackend = s
	settingsMu.Unlock()
	writeSettingsFile(t, SiteSettings{Announcement: Announcement{Text: "from the file", Level: "info"}})

	syncSettingsOnce()
	if GetSiteSettings().Announcement.Text != "from the file" {
		t.Fatalf("not uploaded: %+v", GetSiteSettings())
	}
	if _, v, found, _ := s.Load(); !found || v != 1 {
		t.Fatalf("the document: version %d found %v", v, found)
	}
	if err := SaveSiteSettings(SiteSettings{Maintenance: Maintenance{Enabled: true}}, settingsVersion); err != nil {
		t.Fatal(err)
	}
	// another instance saves
	if _, err := s.Save([]byte(`{"colorOfTheDay":true,"announcement":{"text":"","level":"info"}}`), settingsVersion); err != nil {
		t.Fatal(err)
	}
	syncSettingsOnce()
	got := GetSiteSettings()
	if !got.ColorOfTheDay || got.Maintenance.Enabled {
		t.Fatalf("the other instance's change was not taken: %+v", got)
	}
}

func TestMongoUnreachableDoesNotLeakThePassword(t *testing.T) {
	if os.Getenv("OPENREPL_TEST_MONGODB_URI") == "" {
		t.Skip("OPENREPL_TEST_MONGODB_URI is not set")
	}
	uri := "mongodb://boss:hunter2@127.0.0.1:1/?serverSelectionTimeoutMS=500"
	s, err := newMongoSettingsStore(uri, "x")
	if err != nil {
		t.Fatal(redactURI(err, uri))
	}
	_, _, _, err = s.Load()
	if err == nil {
		t.Fatal("a store on a closed port answered")
	}
	if strings.Contains(redactURI(err, uri).Error(), "hunter2") {
		t.Fatal(err)
	}
}

// ---- Firestore (the emulator) ------------------------------------------------------

func TestFirestoreSettingsStoreVersions(t *testing.T) {
	host := os.Getenv("OPENREPL_TEST_FIRESTORE_EMULATOR")
	if host == "" {
		t.Skip("OPENREPL_TEST_FIRESTORE_EMULATOR is not set")
	}
	t.Setenv("FIRESTORE_EMULATOR_HOST", host)
	t.Setenv("OPENREPL_FIRESTORE_PROJECT", fmt.Sprintf("settings-%d", time.Now().UnixNano()))
	t.Setenv("OPENREPL_MONGODB_URI", "")
	persist.ResetRemote()
	persist.Configure(true)
	t.Cleanup(func() { persist.Configure(false); persist.ResetRemote() })

	s, err := newFirestoreSettingsStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := s.Load(); err != nil || found {
		t.Fatalf("empty: found=%v err=%v", found, err)
	}
	if v, err := s.Save([]byte(`{"a":1}`), 0); err != nil || v != 1 {
		t.Fatalf("first save: %d %v", v, err)
	}
	other, _ := newFirestoreSettingsStore() // another instance
	if _, err := other.Save([]byte(`{"a":2}`), 0); err != errSettingsConflict {
		t.Fatalf("a second insert: %v", err)
	}
	if v, err := s.Save([]byte(`{"a":3}`), 1); err != nil || v != 2 {
		t.Fatalf("update: %d %v", v, err)
	}
	// the other instance read version 2, and then this one saves again
	if _, _, _, err := other.Load(); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Save([]byte(`{"a":4}`), 2); err != nil || v != 3 {
		t.Fatalf("update 2: %d %v", v, err)
	}
	if _, err := other.Save([]byte(`{"a":5}`), 2); err != errSettingsConflict {
		t.Fatalf("a stale update: %v", err)
	}
	if _, err := (&firestoreSettingsStore{f: s.f}).Save([]byte(`{}`), 3); err != errSettingsConflict {
		t.Fatalf("a save with no read before it: %v", err)
	}
	data, v, found, err := s.Load()
	if err != nil || !found || v != 3 || string(data) != `{"a":4}` {
		t.Fatalf("load: %q %d %v %v", data, v, found, err)
	}

	// the whole path through the sync code, with the store as the backend
	withStore(t, &fakeStore{})
	settingsMu.Lock()
	settingsBackend = &firestoreSettingsStore{f: s.f}
	settingsMu.Unlock()
	syncSettingsOnce()
	if st := currentSettingsStoreStatus(); !st.Healthy || st.Name != "firestore" || !strings.Contains(st.Detail, "Firestore") {
		t.Fatalf("status: %+v", st)
	}
}
