package server

import (
	"context"
	"errors"
	"io/ioutil"
	"os"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"persist"
	"utils"
)

// Where the admin settings (settings.go) are kept. The default is the file
// SETTINGS_FILE. With OPENREPL_MONGODB_URI set, a gateway or a standalone
// server keeps them in MongoDB instead and does not write the file, so they
// outlive a host whose disk is wiped on every deploy. A worker never reads
// either: it takes its orders from the gateway.

// errSettingsConflict means the stored settings changed since this server read
// them (another admin, or another instance, saved first).
var errSettingsConflict = errors.New("the settings were changed by someone else; reload and try again")

// settingsStore holds one document of settings, as JSON.
type settingsStore interface {
	Name() string
	// Load returns the stored JSON and its version. found is false when
	// nothing is stored yet.
	Load() (data []byte, version int64, found bool, err error)
	// Save stores data if the stored version is still base (0 for nothing
	// stored yet), and returns the new version; otherwise errSettingsConflict.
	Save(data []byte, base int64) (version int64, err error)
}

// ---- the file ------------------------------------------------------------------

// fileSettingsStore is settings.json. It has no versions: the last save wins.
type fileSettingsStore struct{}

func (fileSettingsStore) Name() string { return "file" }

func (fileSettingsStore) Load() ([]byte, int64, bool, error) {
	data, err := ioutil.ReadFile(SETTINGS_FILE)
	if os.IsNotExist(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	return data, 0, true, nil
}

func (fileSettingsStore) Save(data []byte, _ int64) (int64, error) {
	if err := os.MkdirAll(utils.GOTTY_PATH, 0755); err != nil {
		return 0, err
	}
	tmp := SETTINGS_FILE + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return 0, err
	}
	return 0, os.Rename(tmp, SETTINGS_FILE)
}

// ---- MongoDB -------------------------------------------------------------------

const (
	mongoCollection  = "settings"
	mongoDocumentID  = "site"
	mongoCallTimeout = 6 * time.Second
)

// mongoSettingsStore keeps the document {_id: "site", v: <version>, data:
// <the settings as JSON>, updated: <time>} in the collection "settings".
type mongoSettingsStore struct {
	client *mongo.Client
	coll   *mongo.Collection
	db     string
}

type mongoSettingsDoc struct {
	ID      string    `bson:"_id"`
	Version int64     `bson:"v"`
	Data    string    `bson:"data"`
	Updated time.Time `bson:"updated"`
}

// newMongoSettingsStore prepares the client. It does not wait for the server:
// an Atlas cluster that is down at start-up shows up as errors from Load, which
// the sync loop keeps retrying.
func newMongoSettingsStore(uri, db string) (*mongoSettingsStore, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mongoCallTimeout)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(mongoCallTimeout).
		SetConnectTimeout(mongoCallTimeout).
		SetAppName("openrepl"))
	if err != nil {
		return nil, redactURI(err, uri)
	}
	return &mongoSettingsStore{client: client, coll: client.Database(db).Collection(mongoCollection), db: db}, nil
}

func (m *mongoSettingsStore) Name() string { return "mongodb" }

func (m *mongoSettingsStore) Load() ([]byte, int64, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mongoCallTimeout)
	defer cancel()
	var doc mongoSettingsDoc
	err := m.coll.FindOne(ctx, bson.M{"_id": mongoDocumentID}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		// the server answered: nothing is stored yet
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	return []byte(doc.Data), doc.Version, true, nil
}

func (m *mongoSettingsStore) Save(data []byte, base int64) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mongoCallTimeout)
	defer cancel()
	now := time.Now().UTC()
	if base == 0 {
		_, err := m.coll.InsertOne(ctx, mongoSettingsDoc{ID: mongoDocumentID, Version: 1, Data: string(data), Updated: now})
		if mongo.IsDuplicateKeyError(err) {
			return 0, errSettingsConflict
		}
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	res, err := m.coll.UpdateOne(ctx,
		bson.M{"_id": mongoDocumentID, "v": base},
		bson.M{"$set": bson.M{"v": base + 1, "data": string(data), "updated": now}})
	if err != nil {
		return 0, err
	}
	if res.MatchedCount == 0 {
		return 0, errSettingsConflict
	}
	return base + 1, nil
}

// ping checks that the server answers, for the health page and the sync loop.
func (m *mongoSettingsStore) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), mongoCallTimeout)
	defer cancel()
	return m.client.Ping(ctx, readpref.Primary())
}

// redactURI keeps the password out of an error: the driver quotes parts of the
// URI in some messages.
func redactURI(err error, uri string) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), uri, "<uri>"))
}

// ---- Firestore -------------------------------------------------------------------

// firestoreSettingsStore keeps the same document as the MongoDB store, in the
// collection "settings": {v: <version>, data: <the settings as JSON>, updated}.
// A save is a write that requires the document not to have changed since the
// last read (its updateTime), or not to exist yet.
type firestoreSettingsStore struct {
	f          *persist.Firestore
	mu         sync.Mutex
	updateTime string // of the document as last read or written
}

func newFirestoreSettingsStore() (*firestoreSettingsStore, error) {
	f := persist.FirestoreClient()
	if f == nil {
		return nil, errors.New("Firestore is not available")
	}
	return &firestoreSettingsStore{f: f}, nil
}

func (s *firestoreSettingsStore) Name() string { return "firestore" }

func (s *firestoreSettingsStore) Load() ([]byte, int64, bool, error) {
	d, err := s.f.Get(mongoCollection, mongoDocumentID)
	if err == persist.ErrFSNotFound {
		s.mu.Lock()
		s.updateTime = ""
		s.mu.Unlock()
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	s.mu.Lock()
	s.updateTime = d.UpdateTime
	s.mu.Unlock()
	return []byte(d.String("data")), d.Int("v"), true, nil
}

func (s *firestoreSettingsStore) Save(data []byte, base int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fields := map[string]interface{}{
		"v":       persist.IntValue(base + 1),
		"data":    persist.StringValue(string(data)),
		"updated": persist.TimeValue(time.Now()),
	}
	var pre persist.Precondition
	if base == 0 {
		no := false
		pre.Exists = &no
	} else {
		if s.updateTime == "" {
			return 0, errSettingsConflict // never read: do not write over what is there
		}
		pre.UpdateTime = s.updateTime
	}
	d, err := s.f.Put(mongoCollection, mongoDocumentID, fields, pre)
	if err == persist.ErrFSConflict {
		return 0, errSettingsConflict
	}
	if err != nil {
		return 0, err
	}
	s.updateTime = d.UpdateTime
	return base + 1, nil
}
