package user

import (
	"path/filepath"
	"testing"

	"utils"
)

func open(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users.db")
	if err := OpenSessionDB(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func signIn(t *testing.T, uid, email string, valid bool) {
	t.Helper()
	u := &User{Uid: uid, Name: "Name " + uid, Email: email}
	u.StsTokenManager.AccessToken = "tok-" + uid
	u.StsTokenManager.ExpirationTime = utils.GetUnixMilli() + 3600*1000
	if !valid {
		u.StsTokenManager.ExpirationTime = utils.GetUnixMilli() - 1000
	}
	ss := &UserSession{User: u}
	ss.Update(u)
	// an expired session is stored as it arrives; the purge on the next sign-in removes it
	if err := UpdateAndStoreSessionData(uid, ss.SessionID, ss, false); err != nil {
		t.Fatal(err)
	}
}

func TestListAccountsSkipsEverythingThatIsNotAProfile(t *testing.T) {
	open(t)
	signIn(t, "uid-b", "bob@example.com", true)
	signIn(t, "uid-a", "ann@example.com", true)
	db := GetUserDBHandle()
	db.Store([]byte(SESSION_KEY), []byte{0x01, 0x02, 0x03, 0x04}) // the cookie secret
	db.Store([]byte("not-a-profile"), []byte(`{"uid":"someone-else"}`))
	db.Commit()
	if err := SetWorkerPin("uid-a", "worker-1"); err != nil {
		t.Fatal(err)
	}
	if err := SetBlocked("uid-b", true); err != nil {
		t.Fatal(err)
	}

	accounts, err := ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %+v", accounts)
	}
	if accounts[0].Email != "ann@example.com" || accounts[0].Sessions != 1 || accounts[0].Blocked {
		t.Errorf("ann = %+v", accounts[0])
	}
	if accounts[1].Email != "bob@example.com" || accounts[1].Sessions != 0 || !accounts[1].Blocked {
		t.Errorf("bob = %+v (blocking signs the account out)", accounts[1])
	}
}

func TestABlockSurvivesARestart(t *testing.T) {
	path := open(t)
	signIn(t, "uid-a", "ann@example.com", true)
	if IsBlocked("uid-a") {
		t.Fatal("blocked before anybody blocked it")
	}
	if err := SetBlocked("uid-a", true); err != nil {
		t.Fatal(err)
	}
	if !IsBlocked("uid-a") || !IsSessionExpired("uid-a", "tok-uid-a") {
		t.Error("the block is not in force")
	}
	if err := UpdateAndStoreSessionData("uid-a", "x", &UserSession{}, false); err != ErrBlocked {
		t.Errorf("a blocked account stored a session: %v", err)
	}

	// the same file, opened again by a new process
	session_db_handle.Close()
	session_db_handle = nil
	if err := OpenSessionDB(path); err != nil {
		t.Fatal(err)
	}
	if !IsBlocked("uid-a") {
		t.Error("the block was forgotten")
	}
	if err := SetBlocked("uid-a", false); err != nil {
		t.Fatal(err)
	}
	if IsBlocked("uid-a") {
		t.Error("still blocked after unblocking")
	}
	signIn(t, "uid-a", "ann@example.com", true)
	if err := SetBlocked("", true); err == nil {
		t.Error("blocked an empty uid")
	}
}

func TestSignOutEverywhereEndsEverySession(t *testing.T) {
	open(t)
	signIn(t, "uid-a", "ann@example.com", true)
	if IsSessionExpired("uid-a", "tok-uid-a") {
		t.Fatal("the session should be live")
	}
	if err := SignOutEverywhere("uid-a"); err != nil {
		t.Fatal(err)
	}
	if !IsSessionExpired("uid-a", "tok-uid-a") {
		t.Error("the session survived")
	}
	if up, err := FetchUserProfileData("uid-a"); err != nil || up.Email != "ann@example.com" || len(up.SessionMap) != 0 {
		t.Errorf("the profile must stay, without sessions: %+v %v", up, err)
	}
	if err := SignOutEverywhere("nobody"); err == nil {
		t.Error("signed out an account that does not exist")
	}
}

func TestTheCookieKeyIsDerivedFromTheEnvironmentOrGeneratedOnceAndSaved(t *testing.T) {
	open(t)
	db := GetUserDBHandle()
	defer utils.SetGeneratedSecret("")
	key := []byte(SESSION_KEY)

	// nothing set, nothing stored: one is made and saved, and it stays
	t.Setenv("OPENREPL_SECRET", "")
	first := ensureServerSecret(db)
	if len(first) == 0 {
		t.Fatal("no key was made")
	}
	if stored, err := db.Fetch(key); err != nil || string(stored) != string(first) {
		t.Fatalf("saved %q (%v), made %q", stored, err, first)
	}
	if utils.Secret() == "" || string(CookieKey()) != string(first) {
		t.Fatal("the generated secret and the cookie key are not in use")
	}
	if again := ensureServerSecret(db); string(again) != string(first) {
		t.Fatal("a second start made another key")
	}

	// the environment sets one: the cookie key comes from it, and nothing is saved
	t.Setenv("OPENREPL_SECRET", "from-the-environment-0123456789")
	fromEnv := ensureServerSecret(db)
	if len(fromEnv) != 32 || string(fromEnv) == string(first) || string(fromEnv) == "from-the-environment-0123456789" {
		t.Fatalf("the derived key: %x", fromEnv)
	}
	if utils.Secret() != "from-the-environment-0123456789" || string(CookieKey()) != string(fromEnv) {
		t.Fatalf("in use: secret %q key %x", utils.Secret(), CookieKey())
	}
	if stored, _ := db.Fetch(key); string(stored) != string(first) {
		t.Fatalf("the database was changed by the environment's secret: %q", stored)
	}
	// the same secret gives the same key after a restart, another secret another key
	if again := ensureServerSecret(db); string(again) != string(fromEnv) {
		t.Fatal("the same secret gave another key")
	}
	t.Setenv("OPENREPL_SECRET", "another-one-0123456789")
	if other := ensureServerSecret(db); string(other) == string(fromEnv) {
		t.Fatal("another secret gave the same key")
	}

	// without it again, the saved one is back in use
	t.Setenv("OPENREPL_SECRET", "")
	if back := ensureServerSecret(db); string(back) != string(first) {
		t.Fatalf("the saved key was not used again: %x", back)
	}
}

func TestACopyOfTheSecretSavedByAnEarlierVersionIsRemoved(t *testing.T) {
	open(t)
	db := GetUserDBHandle()
	defer utils.SetGeneratedSecret("")
	t.Setenv("OPENREPL_SECRET", "from-the-environment-0123456789")
	db.Store([]byte(SESSION_KEY), []byte("from-the-environment-0123456789"))
	ensureServerSecret(db)
	if v, err := db.Fetch([]byte(SESSION_KEY)); err == nil && len(v) > 0 {
		t.Fatalf("the secret is still in the database: %q", v)
	}
}
