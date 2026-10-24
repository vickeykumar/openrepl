package persist

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"meters"
	"utils"
)

// Stats is how much a remote database holds and how much of its allowance has been used,
// for the dashboard's live map. It reads what the database can tell: MongoDB's sizes (and
// its disk, when it says), and for Firestore, which cannot be asked its size by the
// application, the documents read, written and deleted today by this server.
type Stats struct {
	Backend string // "mongodb", "firestore", or "" for files
	// MongoDB, in bytes; a size the server did not report is 0
	DataBytes    int64
	IndexBytes   int64
	StorageBytes int64
	DiskUsed     int64
	DiskTotal    int64
	// Firestore, counted by this server since midnight Pacific time
	Reads, Writes, Deletes int64
	Err                    error
}

// Firestore's free daily allowance, per project.
const (
	FirestoreFreeReads   = 50000
	FirestoreFreeWrites  = 20000
	FirestoreFreeDeletes = 20000
)

var errMongoNotConnected = errors.New("persist: MongoDB is not connected")

// number reads a count from a MongoDB reply, where it may be an int32, an int64 or a double.
func number(v interface{}) int64 {
	switch n := v.(type) {
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

// statsFromMongo is the sizes in a dbStats reply.
func statsFromMongo(reply bson.M) Stats {
	return Stats{
		Backend:      backendMongo,
		DataBytes:    number(reply["dataSize"]),
		IndexBytes:   number(reply["indexSize"]),
		StorageBytes: number(reply["storageSize"]),
		DiskUsed:     number(reply["fsUsedSize"]),
		DiskTotal:    number(reply["fsTotalSize"]),
	}
}

// CurrentStats reads the sizes now. It asks MongoDB once (a few milliseconds), so a
// caller that polls keeps its own cache.
func CurrentStats() Stats {
	switch Init() {
	case backendMongo:
		mu.Lock()
		c := client
		mu.Unlock()
		if c == nil {
			return Stats{Backend: backendMongo, Err: errMongoNotConnected}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var reply bson.M
		if err := c.Database(utils.MongoDBName()).RunCommand(ctx, bson.D{{Key: "dbStats", Value: 1}}).Decode(&reply); err != nil {
			return Stats{Backend: backendMongo, Err: redact(err, utils.MongoURI())}
		}
		return statsFromMongo(reply)
	case backendFirestore:
		return Stats{Backend: backendFirestore, Reads: meters.DayValue(meters.FSReads), Writes: meters.DayValue(meters.FSWrites), Deletes: meters.DayValue(meters.FSDeletes)}
	}
	return Stats{}
}
