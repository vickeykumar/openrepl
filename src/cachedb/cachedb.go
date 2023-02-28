package cachedb

import (
	"github.com/nobonobo/unqlitego"
	"github.com/coocood/freecache"
	"log"
	"runtime/debug"
	"errors"
)

const GOGC = 50 
const EXPIRE_SEC = 0 			// expireSeconds <= 0 means no expire
const CACHE_SIZE = 15*1024*1024	// default size of 15MB, this should accomodate 5 sessions as of now
var IGNORE_CACHE_ERRORS bool = true	// ignore cache read/write errors, we can still get the data from db

// Database ...
// write in db first before caching
// Read from cache before cache lookup
type Database struct {
	handle 		*unqlitego.Database
	cachehandle *freecache.Cache
	expireSec	int
}

func init() {
	debug.SetGCPercent(GOGC)
}

// NewDatabase ...
func NewDatabase(filename string) (db *Database, err error) {
	db = &Database{}
	db.handle, err = unqlitego.NewDatabase(filename)
	if err != nil {
		return
	}
	db.cachehandle = freecache.NewCache(CACHE_SIZE)
	db.expireSec = EXPIRE_SEC
	return
}

// NewDatabase with custom size...
func NewDatabaseSize(filename string, cacheSize int) (db *Database, err error) {
	db = &Database{}
	db.handle, err = unqlitego.NewDatabase(filename)
	if err != nil {
		return
	}
	db.cachehandle = freecache.NewCache(cacheSize)
	db.expireSec = EXPIRE_SEC
	return
}

// update the current expire time in seconds
func (db *Database) SetExpireSec(expireSec int) {
	db.expireSec = expireSec
}

// Close ...
func (db *Database) Close() (err error) {
	db.cachehandle.Clear()
	err = db.handle.Close()
	return
}

// Store ...
func (db *Database) Store(key, value []byte) (err error) {
	// first store in db then cache
	log.Println("value size: ",len(value))
	err = db.handle.Store(key, value)
	if err != nil {
		return err 
	}
	err = db.cachehandle.Set(key, value, db.expireSec)
	if IGNORE_CACHE_ERRORS && err != nil {
		log.Println("ignoring cache write error: ",err.Error())
		err = nil
	}
	return
}

// Store With Expire ...
/*
 * sets a key, value and expiration for a cache entry and stores it in the cache. 
 * If the key is larger than 65535 or value is larger than 1/1024 of the cache size, 
 * the entry will not be written to the cache. expireSeconds <= 0 means no expire, 
 * but it can be evicted when cache is full.
 */
func (db *Database) StoreWithExpire(key, value []byte, expireSec int) (err error) {
	// first store in db then cache
	log.Println("value size: ",len(value))
	err = db.handle.Store(key, value)
	if err != nil {
		return err 
	}
	err = db.cachehandle.Set(key, value, expireSec)
	if IGNORE_CACHE_ERRORS && err != nil {
		log.Println("ignoring cache write error: ",err.Error())
		err = nil
	}
	db.expireSec = expireSec 	// store last expiresec
	return
}

// Fetch ...
func (db *Database) Fetch(key []byte) (value []byte, err error) {
	// check in cache if not found, check in db
	value, err = db.cachehandle.Get(key)
	if err != nil {
		log.Println("cache Miss, get it from db: ", string(key))
		// lookup in db
		value, err = db.handle.Fetch(key)
		//if found in db, save to cache for future use.
		if err == nil {
			db.StoreWithExpire(key, value, db.expireSec)
			// don't project this error
		}
	} else {
		// cache hit
		log.Println("cache hit: ", string(key))
	}
	return
}

// Delete ...
func (db *Database) Delete(key []byte) (err error) {
	// delete from from cache, then db ??
	affected := db.cachehandle.Del(key)
	if !affected {
		return errors.New("Failed delete cache Key Entry.")
	}
	err = db.handle.Delete(key)
	return
}

// Cache Clear
func (db *Database) Clear() {
	db.cachehandle.Clear()
}

// Commit ...
func (db *Database) Commit() (err error) {
	err = db.handle.Commit()
	return
}

// Rollback ...
func (db *Database) Rollback() (err error) {
	err = db.handle.Rollback()
	// something rolledback, clear the db to rebuild the cache
	// as of now no rollback in cache exists
	db.Clear()
	return
}