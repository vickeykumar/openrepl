// Package persist is the key-value store under the site's databases (user
// sessions, feedback, blog, snippets, practice). A gateway or a standalone
// server (not a worker, which always keeps files) picks, in this order:
//
//  1. MongoDB, when OPENREPL_MONGODB_URI is set: every database is a collection
//     of that MongoDB database;
//  2. Firestore, when a service account key is given in
//     OPENREPL_FIRESTORE_CREDENTIALS (or FIRESTORE_EMULATOR_HOST is set) and the
//     project's Firestore answers a first read: every database is a collection
//     of its default database. If Firestore is not enabled in the project, or
//     refuses the key, the server says so in the log and goes on with files;
//  3. unqlite, a file on this server, as always.
//
// The cache in package cachedb sits on top of any of them, as before.
package persist

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nobonobo/unqlitego"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"utils"
)

// ErrNotFound is what Fetch and Delete answer for a key that is not stored.
// (unqlite answers with an error of its own for the same case; callers only
// look for err != nil.)
var ErrNotFound = errors.New("persist: key not found")

// Store is one database: keys and values are bytes.
type Store interface {
	Store(key, value []byte) error
	Fetch(key []byte) ([]byte, error)
	Delete(key []byte) error
	// Commit makes the writes since the last Commit permanent. MongoDB writes
	// are permanent at once, so there it does nothing.
	Commit() error
	Rollback() error
	// Each calls fn with every key and value until fn returns false.
	Each(fn func(key, value []byte) bool) error
	Close() error
	// Backend says where the data lives: "unqlite" or "mongodb".
	Backend() string
}

var (
	mu         sync.Mutex
	allowMongo bool // may this process keep its data remotely (MongoDB, Firestore)?
	client     *mongo.Client
)

// callTimeout bounds every call to MongoDB.
var callTimeout = 8 * time.Second

// How the choice of a database is tested at start-up: each backend is tried up
// to initTries times, each try gets probeTimeout, and there are retryDelay
// between tries.
var (
	initTries    = 3
	probeTimeout = 5 * time.Second
	retryDelay   = time.Second
)

// Configure says whether this process may keep its data remotely (in MongoDB or
// Firestore, when one is configured). The gateway and a standalone server may;
// a worker may not. It is called before Init, and forgets an earlier choice.
func Configure(useRemote bool) {
	mu.Lock()
	allowMongo = useRemote
	mu.Unlock()
	forgetChoice()
}

const (
	backendMongo     = "mongodb"
	backendFirestore = "firestore"
	backendFiles     = "unqlite"
)

var (
	choiceMu sync.Mutex
	decided  bool
	chosen   string
	fsClient *Firestore
	// why is what was found out on the way, for the log and the health page
	why string
)

func forgetChoice() {
	choiceMu.Lock()
	decided, chosen, fsClient, why = false, "", nil, ""
	choiceMu.Unlock()
}

// Init chooses where the databases are kept, once, and logs it. Nothing else
// is tested later: whatever it picks stays for the life of the process.
//
//  1. MongoDB, if OPENREPL_MONGODB_URI is set and answers (initTries tries);
//  2. else Firestore, if it is configured and answers (initTries tries);
//  3. else the files of this server.
//
// A process that may not go remote (a worker) keeps files without testing
// anything. Init returns the name of the choice: "mongodb", "firestore" or
// "unqlite".
func Init() string {
	choiceMu.Lock()
	defer choiceMu.Unlock()
	if decided {
		return chosen
	}
	decided = true
	mu.Lock()
	allowed := allowMongo
	mu.Unlock()
	var notes []string
	switch {
	case !allowed:
		chosen = backendFiles
		notes = append(notes, "this process keeps its databases in files")
	default:
		chosen = backendFiles
		if uri := utils.MongoURI(); uri != "" {
			err := tryTimes("MongoDB", func() error { return connectMongo(uri) }, uri)
			if err == nil {
				chosen = backendMongo
				break
			}
			notes = append(notes, fmt.Sprintf("MongoDB did not answer after %d tries (%v)", initTries, err))
		}
		f, err := newFirestore()
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("Firestore is configured but its credentials are not usable (%v)", err))
		case f == nil:
			if utils.MongoURI() != "" {
				notes = append(notes, "Firestore is not configured")
			}
		default:
			f.http.Timeout = probeTimeout
			err := tryTimes("Firestore", func() error {
				return f.List("kv__probe", 1, func(*Doc) bool { return false })
			}, "")
			f.http.Timeout = 15 * time.Second
			if err == nil {
				chosen, fsClient = backendFirestore, f
				break
			}
			notes = append(notes, fmt.Sprintf("Firestore of the project %s did not answer after %d tries (%v). Is Firestore enabled for the project, and may the service account use it?", f.Project(), initTries, err))
		}
	}
	why = strings.Join(notes, "; ")
	switch chosen {
	case backendMongo:
		logf("persist: the databases are stored in MongoDB, database %s", utils.MongoDBName())
	case backendFirestore:
		logf("persist: the databases are stored in Firestore, project %s", fsClient.Project())
	default:
		logf("persist: the databases are stored in files under %s (unqlite)", utils.GOTTY_PATH)
	}
	if why != "" {
		logf("persist: %s", why)
	}
	return chosen
}

// tryTimes runs probe up to initTries times, waiting retryDelay between tries,
// and returns the last error (without secret as the URI).
func tryTimes(what string, probe func() error, secret string) error {
	var err error
	for i := 1; i <= initTries; i++ {
		if err = probe(); err == nil {
			return nil
		}
		if secret != "" {
			err = redact(err, secret)
		}
		logf("persist: %s try %d of %d failed: %v", what, i, initTries, err)
		if i < initTries {
			time.Sleep(retryDelay)
		}
	}
	return err
}

// Chosen is what Init picked ("mongodb", "firestore" or "unqlite") and what it
// found out on the way; it calls Init if that has not happened.
func Chosen() (backend, notes string) {
	Init()
	choiceMu.Lock()
	defer choiceMu.Unlock()
	return chosen, why
}

// UsingMongo reports whether the databases are in MongoDB.
func UsingMongo() bool { return Init() == backendMongo }

// Remote says where the databases are kept besides the files of this server:
// "mongodb", "firestore", or "" (files).
func Remote() string {
	if c := Init(); c != backendFiles {
		return c
	}
	return ""
}

// FirestoreClient returns the Firestore connection when that is where the
// databases are, else nil.
func FirestoreClient() *Firestore {
	if Init() != backendFirestore {
		return nil
	}
	choiceMu.Lock()
	defer choiceMu.Unlock()
	return fsClient
}

func logf(format string, args ...interface{}) { log.Printf(format, args...) }

// ResetRemote forgets the choice and the MongoDB connection, so that the next
// Init looks again. For tests.
func ResetRemote() {
	forgetChoice()
	mu.Lock()
	if client != nil {
		client.Disconnect(context.Background())
	}
	client = nil
	mu.Unlock()
}

// Open opens the database that lives in the file path when unqlite is used.
// With MongoDB its collection is named after the file ("user_sessions.db"
// becomes "kv_user_sessions"), in the database OPENREPL_MONGODB_DB. The first
// time a collection is empty and the file exists, the file's records are
// copied into it once; after that MongoDB wins and the file is not read again.
func Open(path string) (Store, error) {
	switch Init() {
	case backendFirestore:
		s := &firestoreStore{f: FirestoreClient(), coll: "kv_" + strings.TrimSuffix(filepath.Base(path), ".db")}
		if err := s.copyOnce(path); err != nil {
			return nil, err
		}
		return s, nil
	case backendMongo:
	default:
		return openUnqlite(path)
	}
	coll, err := collection(path)
	if err != nil {
		return nil, err
	}
	m := &mongoStore{coll: coll}
	if err := m.copyOnce(path); err != nil {
		return nil, err
	}
	return m, nil
}

// ---- unqlite -------------------------------------------------------------------

type unqliteStore struct{ db *unqlitego.Database }

func openUnqlite(path string) (Store, error) {
	db, err := unqlitego.NewDatabase(path)
	if err != nil {
		return nil, err
	}
	return &unqliteStore{db}, nil
}

func (s *unqliteStore) Store(k, v []byte) error        { return s.db.Store(k, v) }
func (s *unqliteStore) Fetch(k []byte) ([]byte, error) { return s.db.Fetch(k) }
func (s *unqliteStore) Delete(k []byte) error          { return s.db.Delete(k) }
func (s *unqliteStore) Commit() error                  { return s.db.Commit() }
func (s *unqliteStore) Rollback() error                { return s.db.Rollback() }
func (s *unqliteStore) Close() error                   { return s.db.Close() }
func (s *unqliteStore) Backend() string                { return "unqlite" }

func (s *unqliteStore) Each(fn func(key, value []byte) bool) error {
	cursor, err := s.db.NewCursor()
	if err != nil {
		return err
	}
	defer cursor.Close()
	if err := cursor.First(); err != nil {
		return nil // an empty database has nothing to visit
	}
	for cursor.IsValid() {
		key, err := cursor.Key()
		if err != nil {
			return err
		}
		value, err := cursor.Value()
		if err != nil {
			return err
		}
		if !fn(key, value) {
			return nil
		}
		if err := cursor.Next(); err != nil {
			return nil
		}
	}
	return nil
}

// ---- MongoDB -------------------------------------------------------------------

// connectMongo connects, once, and checks that the server answers. Init calls
// it up to initTries times.
func connectMongo(uri string) error {
	mu.Lock()
	defer mu.Unlock()
	if client != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	c, err := mongo.Connect(ctx, options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(probeTimeout).
		SetConnectTimeout(probeTimeout).
		SetAppName("openrepl"))
	if err != nil {
		return err
	}
	if err := c.Ping(ctx, readpref.Primary()); err != nil {
		c.Disconnect(context.Background())
		return err
	}
	client = c
	return nil
}

// collection returns the collection for path in the database Init connected to.
func collection(path string) (*mongo.Collection, error) {
	mu.Lock()
	defer mu.Unlock()
	if client == nil {
		return nil, errors.New("persist: MongoDB is not connected")
	}
	name := "kv_" + strings.TrimSuffix(filepath.Base(path), ".db")
	return client.Database(utils.MongoDBName()).Collection(name), nil
}

func redact(err error, uri string) error {
	return errors.New(strings.ReplaceAll(err.Error(), uri, "<uri>"))
}

// a record: {_id: key, v: value, t: time}
type record struct {
	ID    interface{} `bson:"_id"`
	Value []byte      `bson:"v"`
	Time  time.Time   `bson:"t"`
}

// id is the key as a string when it is text (every key the site uses is) and as
// binary otherwise, so the records can be read in Atlas.
func id(key []byte) interface{} {
	if utf8.Valid(key) {
		return string(key)
	}
	return primitive.Binary{Data: key}
}

func keyOf(v interface{}) []byte {
	switch k := v.(type) {
	case string:
		return []byte(k)
	case primitive.Binary:
		return k.Data
	}
	return nil
}

type mongoStore struct{ coll *mongo.Collection }

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), callTimeout)
}

func (m *mongoStore) Backend() string { return "mongodb" }
func (m *mongoStore) Commit() error   { return nil }
func (m *mongoStore) Rollback() error { return nil }
func (m *mongoStore) Close() error    { return nil } // the connection is shared and ends with the process

func (m *mongoStore) Store(key, value []byte) error {
	c, cancel := ctx()
	defer cancel()
	_, err := m.coll.ReplaceOne(c, bson.M{"_id": id(key)},
		record{ID: id(key), Value: value, Time: time.Now().UTC()},
		options.Replace().SetUpsert(true))
	return err
}

func (m *mongoStore) Fetch(key []byte) ([]byte, error) {
	c, cancel := ctx()
	defer cancel()
	var r record
	err := m.coll.FindOne(c, bson.M{"_id": id(key)}).Decode(&r)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return r.Value, nil
}

func (m *mongoStore) Delete(key []byte) error {
	c, cancel := ctx()
	defer cancel()
	res, err := m.coll.DeleteOne(c, bson.M{"_id": id(key)})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *mongoStore) Each(fn func(key, value []byte) bool) error {
	c, cancel := context.WithTimeout(context.Background(), 5*callTimeout)
	defer cancel()
	cur, err := m.coll.Find(c, bson.M{})
	if err != nil {
		return err
	}
	defer cur.Close(c)
	for cur.Next(c) {
		var r record
		if err := cur.Decode(&r); err != nil {
			return err
		}
		if !fn(keyOf(r.ID), r.Value) {
			return nil
		}
	}
	return cur.Err()
}

// copyOnce fills an empty collection from the unqlite file at path, if there
// is one. The file is left as it is.
func (m *mongoStore) copyOnce(path string) error {
	c, cancel := ctx()
	defer cancel()
	n, err := m.coll.CountDocuments(c, bson.M{}, options.Count().SetLimit(1))
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil // nothing to bring along
	}
	local, err := openUnqlite(path)
	if err != nil {
		return err
	}
	defer local.Close()
	var docs []interface{}
	now := time.Now().UTC()
	if err := local.Each(func(k, v []byte) bool {
		docs = append(docs, record{ID: id(k), Value: append([]byte(nil), v...), Time: now})
		return true
	}); err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	c2, cancel2 := context.WithTimeout(context.Background(), 5*callTimeout)
	defer cancel2()
	// unordered, and a key that another instance copied first is not an error
	if _, err := m.coll.InsertMany(c2, docs, options.InsertMany().SetOrdered(false)); err != nil && !allDuplicates(err) {
		return err
	}
	log.Printf("persist: copied %d records from %s into MongoDB (done once; MongoDB now holds them)", len(docs), path)
	return nil
}

func allDuplicates(err error) bool {
	we, ok := err.(mongo.BulkWriteException)
	if !ok {
		return false
	}
	for _, e := range we.WriteErrors {
		if e.Code != 11000 {
			return false
		}
	}
	return we.WriteConcernError == nil
}
