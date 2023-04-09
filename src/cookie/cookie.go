package cookie

import (
	"github.com/gorilla/sessions"
	"log"
	"net/http"
	"strconv"
	"utils"
	"user"
	"errors"
	"encoder"
)

const MAX_CONN_PER_BROWSER = 1

// handling for cookies and incrementing session cookie counter
func IncrementCounterCookies(rw http.ResponseWriter, req *http.Request) {
	cookie, err := req.Cookie("Session-Counter")
	if err == http.ErrNoCookie {
		cookie = &http.Cookie{
			Name:  "Session-Counter",
			Value: "0",
		}
	} else if err != nil {
		log.Println("unknown error while getting cookie: ", err.Error())
		return
	}
	sessionCount, _ := strconv.Atoi(cookie.Value)
	sessionCount++
	cookie.Value = strconv.Itoa(sessionCount)
	http.SetCookie(rw, cookie)
	log.Println("cookie set: ", cookie)
}

func DecrementCounterCookies(rw http.ResponseWriter, req *http.Request) {
	// decrementing cookie
	cookie, err := req.Cookie("Session-Counter")
	if err == http.ErrNoCookie {
		cookie = &http.Cookie{
			Name:  "Session-Counter",
			Value: "0",
		}
	} else {
		log.Println("unknown error while getting cookie: ", err.Error())
		return
	}
	sessionCount, _ := strconv.Atoi(cookie.Value)
	if sessionCount > 0 {
		sessionCount--
		cookie.Value = strconv.Itoa(sessionCount)
		http.SetCookie(rw, cookie)
		log.Println("cookie dec: ", cookie)
	}
}

func GetCounterCookieValue(req *http.Request) int {
	cookie, err := req.Cookie("Session-Counter")
	if err != nil {
		log.Println("Error while getting cookie: ", err.Error())
		return 0
	}
	sessionCount, _ := strconv.Atoi(cookie.Value)
	return sessionCount
}


var session_store *sessions.CookieStore

// initilize the cookiestore with secret stored in session DB
func Init_SessionStore(secret string) {
	session_store = sessions.NewCookieStore([]byte(secret))
}

func Get_SessionStore() *sessions.CookieStore {
	return session_store
}

func init() {
    session_db_handle := user.GetUserDBHandle()
    if session_db_handle == nil {
    	panic(errors.New("uninitialized session_db_handle!!"))
    }
    secret , err := session_db_handle.Fetch([]byte(user.SESSION_KEY))
    log.Println("secret: ", secret, err)
    if err != nil {
	// failed to fetch secret, generate a temporary secret for this instance
    	secret := encoder.GenerateLargePrime().Bytes()
    	log.Println("Failed to fetch secret for session_cookie, generated temporary secret: ", secret, err)
    }
    // init one time session store using SESSION_KEY
    Init_SessionStore(string(secret))

}


func Set_SessionCookie(rw http.ResponseWriter, req *http.Request, session user.UserSession) (err error) {
		session_cookie, err := session_store.Get(req, "user-session")
		if err != nil {
			// log and move on, u can still save
			log.Println("Error: while getting cookie err: ", err.Error())
		}
		// Set some session values.
		session_cookie.Values["uid"] = session.Uid
		session_cookie.Values["sessionID"] = session.SessionID
		session_cookie.Values ["loggedIn"] = session.LoggedIn
		session_cookie.Values ["expirationTime"] = session.ExpirationTime
		session_cookie.Values [utils.HOME_DIR_KEY] = user.GetHomeDir(session.Uid)

		// set maxage of the session
		session_cookie.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   int(session.ExpirationTime-utils.GetUnixMilli())/1000,
		}

		log.Println("session cookie save: ", int(session.ExpirationTime-utils.GetUnixMilli())/1000, session_cookie)
		return session_store.Save(req, rw, session_cookie)
}


func Delete_SessionCookie(rw http.ResponseWriter, req *http.Request, session user.UserSession) (err error) {
		session_cookie, err := session_store.Get(req, "user-session")
		if err != nil {
			log.Println("Error: while getting cookie err: ", err.Error())
		}
		// Set some session values.
		session_cookie.Values["uid"] = session.Uid
		session_cookie.Values["sessionID"] = session.SessionID
		session_cookie.Values ["loggedIn"] = false

		// set maxage of the session
		session_cookie.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   -1,
		}

		return session_store.Save(req, rw, session_cookie)
}


func Is_UserLoggedIn(req *http.Request) (loggedin bool) {
	session_cookie, err := session_store.Get(req, "user-session")
	val := session_cookie.Values["loggedIn"]
	loggedin, ok := val.(bool);
	log.Println ("loggedIn: ", loggedin, err)
	if !ok {
		return false
	}
	return loggedin
}

func Get_Uid(req *http.Request) (uid string) {
	session_cookie, _ := session_store.Get(req, "user-session")
	val := session_cookie.Values["uid"]
	uid, ok := val.(string);
	if !ok {
		return ""
	}
	return uid
}

func Get_SessionID(req *http.Request) (sessionid string) {
	session_cookie, _ := session_store.Get(req, "user-session")
	val := session_cookie.Values["sessionID"]
	sessionid, ok := val.(string);
	if !ok {
		return ""
	}
	return sessionid
}

func Get_ExpirationTime(req *http.Request) (e int64) {
	session_cookie, _ := session_store.Get(req, "user-session")
	val := session_cookie.Values["expirationTime"]
	e, ok := val.(int64);
	if !ok {
		return utils.GetUnixMilli()
	}
	return e
}

func IsSessionExpired(req *http.Request) bool {
	var age int = int(Get_ExpirationTime(req)-utils.GetUnixMilli())/1000
	if age < 0 {
		return true
	}
	return false
}

func Get_SessionCookie(req *http.Request) (session user.UserSession) {
		session.Uid = Get_Uid(req)
		session.SessionID = Get_SessionID(req)
		session.LoggedIn = Is_UserLoggedIn(req)
		session.ExpirationTime = Get_ExpirationTime(req)
		return session
}

func UpdateGuestSessionCookieAge(rw http.ResponseWriter, req *http.Request, newage int) (err error) {
	session_cookie, err := session_store.Get(req, "user-session")
	if err != nil {
		// log and move on, u can still save
		log.Println("Error: while getting cookie err: ", err.Error())
	}
	var maxage int = int(Get_ExpirationTime(req)-utils.GetUnixMilli())/1000

	if !Is_UserLoggedIn(req) || IsSessionExpired(req) {
		// only update age if user not logged in
		maxage = newage
	}
	// update maxage (sec) of the session 
	session_cookie.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   maxage,
	}
	err = session_store.Save(req, rw, session_cookie)
	log.Println("session cookie save: ", maxage, session_cookie, "Error: ", err)
	return err
}

func GetOrUpdateHomeDir(rw http.ResponseWriter, req *http.Request, Uid string) (homedir string) {
		session_cookie, err := session_store.Get(req, "user-session")
		if err != nil {
			// log and move on, u can still save
			log.Println("Error: while getting cookie err: ", err.Error())
		}
		if !Is_UserLoggedIn(req) || IsSessionExpired(req) {
			// this is as good as empty Uid
			Uid = ""
		}
		var ok bool

		homedir, ok = session_cookie.Values[utils.HOME_DIR_KEY].(string);
		log.Println("previous homedir: ", homedir, ok, session_cookie)
		// check if same uid amd valid home dir, if not generate a new homedir
		if Uid!=Get_Uid(req) || !ok || homedir=="" {
			homedir = user.GetHomeDir(Uid)
		}
		session_cookie.Values[utils.HOME_DIR_KEY] = homedir

		err = UpdateGuestSessionCookieAge(rw, req, utils.DEADLINE_MINUTES*60) 
		if err != nil {
			// log and move on
			log.Println("Error: while updating cookie err: ", err.Error())
		}
		return homedir
}
