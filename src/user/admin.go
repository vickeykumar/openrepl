package user

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"cachedb"
	"utils"
)

// Account administration for the admin dashboard.

// blockedPrefix keeps the block flags apart from the profiles in the user
// database.
const blockedPrefix = "blocked:"

// ErrBlocked is returned when a blocked account tries to sign in.
var ErrBlocked = errors.New("this account is blocked")

var (
	blockedMu     sync.RWMutex
	blockedSet    = map[string]bool{}
	blockedLoaded bool
)

// loadBlocked reads the block flags once; later changes go through SetBlocked.
func loadBlocked() {
	blockedMu.RLock()
	done := blockedLoaded
	blockedMu.RUnlock()
	if done {
		return
	}
	blockedMu.Lock()
	defer blockedMu.Unlock()
	if blockedLoaded {
		return
	}
	GetUserDBHandle().Each(func(key, value []byte) bool {
		if k := string(key); strings.HasPrefix(k, blockedPrefix) && string(value) == "1" {
			blockedSet[strings.TrimPrefix(k, blockedPrefix)] = true
		}
		return true
	})
	blockedLoaded = true
}

// IsBlocked reports whether an admin blocked the account.
func IsBlocked(uid string) bool {
	if uid == "" {
		return false
	}
	loadBlocked()
	blockedMu.RLock()
	defer blockedMu.RUnlock()
	return blockedSet[uid]
}

// SetBlocked blocks or unblocks an account. A blocked account is signed out
// everywhere and cannot sign in again until it is unblocked.
func SetBlocked(uid string, blocked bool) error {
	if uid == "" {
		return errors.New("invalid user")
	}
	loadBlocked()
	flag := "0"
	if blocked {
		flag = "1"
	}
	db := GetUserDBHandle()
	if err := db.Store([]byte(blockedPrefix+uid), []byte(flag)); err != nil {
		return err
	}
	if err := db.Commit(); err != nil {
		return err
	}
	invalidateAccountsNow()
	blockedMu.Lock()
	if blocked {
		blockedSet[uid] = true
	} else {
		delete(blockedSet, uid)
	}
	blockedMu.Unlock()
	if blocked {
		return SignOutEverywhere(uid)
	}
	return nil
}

// SignOutEverywhere ends every session of the account. The browsers still
// hold their cookies, but the sessions are no longer known, so they are
// treated as signed out.
func SignOutEverywhere(uid string) error {
	up, err := FetchUserProfileData(uid)
	if err != nil {
		return err
	}
	up.SessionMap = make(map[string]UserSession)
	data, err := json.Marshal(up)
	if err != nil {
		return err
	}
	db := GetUserDBHandle()
	if err := db.Store([]byte(uid), data); err != nil {
		return err
	}
	err = db.Commit()
	invalidateAccountsNow()
	return err
}

// Account is one row of the admin's list of users.
type Account struct {
	Uid      string `json:"uid"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Sessions int    `json:"sessions"` // signed-in sessions that have not expired
	Blocked  bool   `json:"blocked"`
	// When the account signed in, in milliseconds; 0 if that was not recorded
	// (the account last signed in before it was).
	LastSignIn  int64 `json:"lastSignIn"`
	FirstSignIn int64 `json:"firstSignIn"`
	SignIns     int   `json:"signIns"`
}

// The list of accounts is read by the dashboard every few seconds (the Users view, the live map,
// Site usage), and each read of it is a scan of the whole user database, which on Firestore costs a
// read for every account. So the list is kept for five minutes (ScanCache); a sign-in, a block or a
// sign-out drops it, and a rescan then waits a minute at most.
var accountsCache = &utils.ScanCache[[]Account]{TTL: 5 * time.Minute, MinInterval: time.Minute}

// InvalidateAccounts says that an account changed (a sign-in, say), so the next list is read again, within
// a minute.
func InvalidateAccounts() { accountsCache.Invalidate() }

// invalidateAccountsNow is for what an admin just did to an account (a block, a sign-out): the next list
// is read at once, since the admin is looking at it.
func invalidateAccountsNow() { accountsCache.InvalidateNow() }

func copyAccounts(in []Account, err error) ([]Account, error) {
	out := make([]Account, len(in))
	copy(out, in)
	return out, err
}

// ListAccounts returns every account, by email address, as it was at the last scan (at most five
// minutes old, less when an account changed).
func ListAccounts() ([]Account, error) {
	return copyAccounts(accountsCache.Get(false, listAccountsScan))
}

// ListAccountsFresh is ListAccounts for an admin who asked for the latest: a new scan, unless one
// was made in the last minute.
func ListAccountsFresh() ([]Account, error) {
	return copyAccounts(accountsCache.Get(true, listAccountsScan))
}

// listAccountsScan reads every account from the database.
func listAccountsScan() ([]Account, error) {
	db := GetUserDBHandle()
	now := utils.GetUnixMilli()
	out := []Account{}
	err := db.Each(func(key, value []byte) bool {
		k := string(key)
		if strings.Contains(k, ":") { // worker pins and other records
			return true
		}
		var up UserProfile
		if json.Unmarshal(value, &up) != nil || up.Uid != k {
			return true
		}
		a := Account{Uid: up.Uid, Name: up.Name, Email: up.Email, LastSignIn: up.LastSignIn, FirstSignIn: up.FirstSignIn, SignIns: up.SignIns}
		for _, s := range up.SessionMap {
			if s.ExpirationTime > now {
				a.Sessions++
			}
		}
		out = append(out, a)
		return true
	})
	for i := range out {
		out[i].Blocked = IsBlocked(out[i].Uid)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Email != out[j].Email {
			return out[i].Email < out[j].Email
		}
		return out[i].Uid < out[j].Uid
	})
	return out, err
}

// OpenSessionDB makes the user database at path the one in use, instead of
// the default one. Tests use it to stay away from the real data.
func OpenSessionDB(path string) error {
	db, err := cachedb.NewDatabase(path)
	if err != nil {
		return err
	}
	session_db_handle = db
	accountsCache.Reset()
	accountsCache.MinInterval = 0 // the tests change accounts and look at once
	blockedMu.Lock()
	blockedSet, blockedLoaded = map[string]bool{}, false
	blockedMu.Unlock()
	return nil
}
