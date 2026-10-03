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
