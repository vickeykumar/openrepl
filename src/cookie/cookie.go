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
	"containers"
)

const MAX_CONN_PER_BROWSER = 1
// this key will be used for encryption and decdryption of tokens 
var SECRET_KEY []byte = encoder.GenerateLargePrime().Bytes()

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
func Init_SessionStore(secret []byte) {
	session_store = sessions.NewCookieStore(secret)
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
    log.Println("secret fetched: ", err)
    if err != nil {
	// failed to fetch secret, generate a temporary secret for this instance
    	secret = encoder.GenerateLargePrime().Bytes()
    	log.Println("Failed to fetch secret for session_cookie, generated temporary secret. ", err)
    }
    SECRET_KEY = secret
    // init one time session store using SESSION_KEY
    Init_SessionStore(secret)

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

		// number of api request remaining in seconds
		var req_count_rem float64 = float64(session.ExpirationTime-utils.GetUnixMilli())/1000
		// set maxage of the session
		session_cookie.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   int(req_count_rem),
		}

		if session.LoggedIn {
			session_cookie.Values[utils.OPENAI_REQUEST_COUNT_KEY] = (req_count_rem/60)*utils.USER_FACTOR
		} else {
			session_cookie.Values[utils.OPENAI_REQUEST_COUNT_KEY] = (req_count_rem/60)*utils.GUEST_FACTOR
		}

		log.Println("session cookie save: ", int(session.ExpirationTime-utils.GetUnixMilli())/1000)
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
		delete(session_cookie.Values, utils.OPENAI_REQUEST_COUNT_KEY)
		delete(session_cookie.Values, utils.OPENAI_REQUEST_LAST_ACCESS)

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

func GetOpenApiRequestCount(req *http.Request) (count float64) {
	session_cookie, _ := session_store.Get(req, "user-session")
	val := session_cookie.Values[utils.OPENAI_REQUEST_COUNT_KEY]
	count, ok := val.(float64);
	if !ok {
		return float64(0)
	}
	return count
}

func SetOpenApiRequestCount(rw http.ResponseWriter, req *http.Request, count float64) (err error) {
	session_cookie, _ := session_store.Get(req, "user-session")
	session_cookie.Values[utils.OPENAI_REQUEST_COUNT_KEY] = count
	return session_store.Save(req, rw, session_cookie)
}

func GetOpenApiLastAccessTime(req *http.Request) (lastaccesstime int64) {
	session_cookie, _ := session_store.Get(req, "user-session")
	val := session_cookie.Values[utils.OPENAI_REQUEST_LAST_ACCESS]
	lastaccesstime, ok := val.(int64);
	if !ok {
		return utils.GetUnixMilli() - utils.DEADLINE_MINUTES*60*1000
	}
	return lastaccesstime
}

/* 
   to be called whenever openAIapi request is made, to recharge the request count
   based on current time lapsed, it should eb able to give n number of requests per minute,
   as given by user or GUEST_FACTOR
*/
func UpdateOpenApiRequestCountBalance(rw http.ResponseWriter, req *http.Request) (err error) {
	session_cookie, _ := session_store.Get(req, "user-session")
	// recharge req_count balance in sec
	current_time_mili := utils.GetUnixMilli()
	var req_count_balance_sec float64 = float64(current_time_mili-GetOpenApiLastAccessTime(req))/1000

	var req_count_balance float64 = 0
	var max_cap float64 = utils.GUEST_FACTOR*utils.DEADLINE_MINUTES // max num of request per minute a user can make
	if Is_UserLoggedIn(req) {
		req_count_balance = GetOpenApiRequestCount(req)+(req_count_balance_sec/60)*utils.USER_FACTOR
		max_cap = utils.USER_FACTOR*utils.DEADLINE_MINUTES
	} else {
		req_count_balance = GetOpenApiRequestCount(req)+(req_count_balance_sec/60)*utils.GUEST_FACTOR
		max_cap = utils.GUEST_FACTOR*utils.DEADLINE_MINUTES
	}
	if max_cap >= req_count_balance {
		session_cookie.Values[utils.OPENAI_REQUEST_COUNT_KEY] = req_count_balance
	} else {
		session_cookie.Values[utils.OPENAI_REQUEST_COUNT_KEY] = max_cap
	}
	// not exceeding request balance more that maxcap req per user
	session_cookie.Values[utils.OPENAI_REQUEST_LAST_ACCESS] = current_time_mili
	return session_store.Save(req, rw, session_cookie)
}

func GetOpenApiAccessToken(req *http.Request) (acc_token, secret []byte) {
	session_cookie, _ := session_store.Get(req, "user-session")
	val := session_cookie.Values[utils.ACCESS_TOKEN_KEY]
	secretval := session_cookie.Values[utils.ACCESS_SECRET_KEY]
	acc_token, _ = val.([]byte);
	secret, _ = secretval.([]byte);
	return acc_token, secret
}

func SetOpenApiAccessToken(rw http.ResponseWriter, req *http.Request, acc_token, secret []byte) (err error) {
	session_cookie, _ := session_store.Get(req, "user-session")
	session_cookie.Values[utils.ACCESS_TOKEN_KEY] = acc_token
	session_cookie.Values[utils.ACCESS_SECRET_KEY] = secret
	return session_store.Save(req, rw, session_cookie)
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
	log.Println("session cookie save: ", maxage, "Error: ", err)
	return err
}

func GetOrUpdateHomeDir(rw http.ResponseWriter, req *http.Request, Uid string) (homedir string) {
		// try to get homedir from jid, priority 1
		jid := req.Form.Get("jid")
		ppid := encoder.DecodeToPID(jid)	// try getting parent processid
		if ppid != -1 {		// called as a fork of another parent process
			return containers.GetWorkingDir(ppid)
		}

		// try getting homedir from request query itself, priority 2
		if req.Form.Has(utils.HOME_DIR_KEY) {
			homedir = req.Form.Get(utils.HOME_DIR_KEY)
			return homedir
		}

		// try getting homedir from session cookie
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
		log.Println("previous homedir: ", homedir, ok)
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
