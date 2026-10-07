package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"persist"
	"utils"
)

// Keeping the settings in MongoDB. The file store (settings_store.go) needs no
// loop; this file is only used when OPENREPL_MONGODB_URI is set on a gateway
// or a standalone server.
//
// The server holds a copy in memory (siteSettings). A loop reads the database
// every settingsPollInterval and, when the stored version moved, replaces the
// copy, so a change made through one instance reaches the others within
// seconds. A save is a compare-and-set on the version, so two admins cannot
// overwrite each other unseen.
//
// If the database cannot be reached at start-up the server runs on the
// built-in defaults and keeps trying; the health page says so. Saves are
// refused until a read has worked, so nothing is written over settings that
// were never seen.

var errSettingsUnavailable = errors.New("the settings store is not reachable right now; nothing was saved")

var settingsPollInterval = 10 * time.Second

var (
	// guarded by settingsMu (settings.go)
	settingsBackend  settingsStore // nil: the file
	settingsVersion  int64         // the stored version the copy in memory is from
	settingsSynced   bool          // a read of the store has worked
	settingsLastOK   time.Time
	settingsLastErr  string
	settingsMigrated string // what the first start did, for the health page and the log
)

var syncOnce sync.Mutex // one sync at a time

// StartSettingsSync switches the settings to MongoDB when OPENREPL_MONGODB_URI
// is set, and keeps them in step until ctx ends. A worker never does: it takes
// the gateway's orders. Without the URI it does nothing, and the file is used.
func StartSettingsSync(ctx context.Context, mode string) {
	kind := persist.Remote() // "mongodb", "firestore" or "": the same choice as the databases
	if kind == "" || mode == ModeWorker {
		GetSiteSettings() // read the file now, so that keys saved in the dashboard are in use from the start
		return
	}
	// A bad URI or a cluster that is down at start-up does not stop the server:
	// it runs on the defaults and the loop keeps trying.
	var store settingsStore
	open := func() (settingsStore, error) {
		if kind == "firestore" {
			s, err := newFirestoreSettingsStore()
			if err != nil {
				return nil, err
			}
			return s, nil
		}
		s, err := newMongoSettingsStore(utils.MongoURI(), utils.MongoDBName())
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	step := func() {
		if store == nil {
			s, err := open()
			if err != nil {
				noteSettingsError(fmt.Errorf("%s client: %v", kind, redactErr(err)))
			} else {
				store = s
				settingsMu.Lock()
				settingsBackend = store
				settingsMu.Unlock()
				log.Println("settings: using", kind)
			}
		}
		if store != nil {
			syncSettingsOnce()
		}
	}
	// The first read is made before the server starts (it takes at most a few
	// seconds), so that the keys saved in the dashboard are in use from the
	// first request instead of the environment's.
	step()
	go func() {
		tick := time.NewTicker(settingsPollInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				step()
			}
		}
	}()
}

func noteSettingsError(err error) {
	settingsMu.Lock()
	settingsLastErr = err.Error()
	settingsMu.Unlock()
	log.Println("settings: ", err)
}

// applySettings makes s the current settings. fromStore says that version is a
// version of the MongoDB document.
func applySettings(s SiteSettings, version int64, fromStore bool) {
	settingsMu.Lock()
	siteSettings = s
	settingsLoaded = true
	if fromStore {
		settingsVersion = version
		settingsSynced = true
		settingsLastOK = time.Now()
		settingsLastErr = ""
	}
	settingsMu.Unlock()
	utils.SetGenieRates(s.Genie.GuestPerMinute, s.Genie.UserPerMinute)
	applyKeyOverrides(s.Secrets)
	utils.SetExtraAdmins(s.Admins)
}

// syncSettingsOnce reads the store and brings the copy in memory up to date.
// The first time it finds nothing stored it uploads the settings.json of this
// server, if there is one (once: afterwards MongoDB wins).
func syncSettingsOnce() {
	syncOnce.Lock()
	defer syncOnce.Unlock()
	settingsMu.Lock()
	backend, known, synced := settingsBackend, settingsVersion, settingsSynced
	settingsMu.Unlock()
	if backend == nil {
		return
	}
	data, version, found, err := backend.Load()
	if err != nil {
		noteSettingsError(fmt.Errorf("reading the settings from %s: %v", backend.Name(), redactErr(err)))
		return
	}
	if !found {
		migrateFileToStore(backend)
		return
	}
	if synced && version == known {
		settingsMu.Lock()
		settingsLastOK, settingsLastErr = time.Now(), ""
		settingsMu.Unlock()
		return
	}
	var s SiteSettings
	if err := json.Unmarshal(data, &s); err == nil {
		s, err = s.normalize()
		if err == nil {
			applySettings(s, version, true)
			return
		}
	}
	// A stored document that does not pass the checks is not applied. The
	// version is still taken, so an admin can save over it.
	noteSettingsError(errors.New("the stored settings are invalid and were not applied"))
	settingsMu.Lock()
	settingsVersion, settingsSynced, settingsLastOK = version, true, time.Now()
	settingsMu.Unlock()
}

// migrateFileToStore runs when the store holds nothing: the server's own
// settings.json, if it has one, becomes the stored document. Otherwise the
// defaults stand and the first save creates the document.
func migrateFileToStore(backend settingsStore) {
	var s SiteSettings
	data, _, found, err := fileSettingsStore{}.Load()
	if err == nil && found {
		if err = json.Unmarshal(data, &s); err == nil {
			s, err = s.normalize()
		}
	}
	if err != nil || !found {
		if err != nil {
			log.Println("settings: not uploading ", SETTINGS_FILE, ": ", err)
		}
		applySettings(SiteSettings{}, 0, true)
		settingsMu.Lock()
		settingsMigrated = "nothing stored yet; running on the defaults"
		settingsMu.Unlock()
		return
	}
	out, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		var version int64
		if version, err = backend.Save(out, 0); err == nil {
			applySettings(s, version, true)
			settingsMu.Lock()
			settingsMigrated = "uploaded " + SETTINGS_FILE + " once"
			settingsMu.Unlock()
			log.Println("settings: uploaded", SETTINGS_FILE, "to", backend.Name(), "(done once; the store now holds the settings)")
			return
		}
	}
	if err == errSettingsConflict {
		return // another instance stored first; the next read picks it up
	}
	noteSettingsError(fmt.Errorf("uploading %s: %v", SETTINGS_FILE, redactErr(err)))
}

func redactErr(err error) error {
	if uri := utils.MongoURI(); uri != "" {
		return redactURI(err, uri)
	}
	return err
}

// settingsStoreStatus describes the store for the health page: its name, how
// the first start went, and whether it is healthy.
type settingsStoreStatus struct {
	Name     string // "file" or "mongodb"
	Healthy  bool
	Degraded bool // was reachable, is not now
	Detail   string
}

func currentSettingsStoreStatus() settingsStoreStatus {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if settingsBackend == nil && persist.Remote() == "" {
		return settingsStoreStatus{Name: "file", Healthy: true, Detail: "the file " + SETTINGS_FILE}
	}
	name := persist.Remote()
	if settingsBackend != nil {
		name = settingsBackend.Name()
	}
	st := settingsStoreStatus{Name: name}
	switch {
	case !settingsSynced:
		st.Detail = "not reachable yet: running on the built-in defaults and retrying"
		if settingsLastErr != "" {
			st.Detail += " (" + settingsLastErr + ")"
		}
	case settingsLastErr != "":
		st.Degraded = true
		st.Detail = fmt.Sprintf("version %d in memory, but the last read failed (%s); changes cannot be saved", settingsVersion, settingsLastErr)
	default:
		st.Healthy = true
		st.Detail = fmt.Sprintf("%s, version %d, read %ds ago", storeTitle(name), settingsVersion, int(time.Since(settingsLastOK).Seconds()))
		if settingsMigrated != "" {
			st.Detail += "; " + settingsMigrated
		}
	}
	return st
}

// storeTitle names a store for a person to read.
func storeTitle(name string) string {
	switch name {
	case "mongodb":
		return "MongoDB, database " + utils.MongoDBName()
	case "firestore":
		if f := persist.FirestoreClient(); f != nil {
			return "Firestore, project " + f.Project()
		}
		return "Firestore"
	}
	return name
}
