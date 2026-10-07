package server

import (
	"encoding/json"
	"persist"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
	"io/ioutil"
	"utils"
	"html/template"
	"bytes"
	"user"
	"cookie"
)

const FEEDBACK_DB = utils.GOTTY_PATH + "/feedback.db"

var feedback_db_handle persist.Store

type feedback struct {
	Name    string
	Email   string
	Message string
	Read    bool // set by an admin at /admin
}

func InitFeedbackDBHandle() {
	var err error
	feedback_db_handle, err = persist.Open(FEEDBACK_DB)
	if err != nil {
		log.Println("ERROR: Error while creating feedback DB handle : ", err.Error())
		os.Exit(3)
	}
	log.Println("Successfully initialized fb handle, stored in", feedback_db_handle.Backend())
}

func CloseFeedbackDBHandle() {
	if feedback_db_handle == nil {
		return
	}
	err := feedback_db_handle.Close()
	if err != nil {
		log.Println("ERROR: Error while closing feedback DB handle : ", err.Error())
	}
	feedback_db_handle = nil
}

func StoreFeedbackData(fb *feedback) error {
	data, err := json.Marshal(*fb)
	if err != nil {
		log.Println("ERROR: Error while marshalling feedback data. Error: ", err.Error())
		return err
	}
	timestamp := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	err = feedback_db_handle.Store([]byte(timestamp), data)
	if err != nil {
		log.Println("ERROR: Failed to store the feedbackdata, error: ", err.Error())
		return err
	}
	err = feedback_db_handle.Commit()
	if err != nil {
		log.Println("ERROR: Failed to commit the feedbackdata to disk, error: ", err.Error())
	}
	return err
}

func deleteFeedbackData(key string) error {
	err := feedback_db_handle.Delete([]byte(key))
	if err!= nil {
		return err
	}
	err = feedback_db_handle.Commit()
	return err
}

func FetchFeedbackDataMap() (fblistmap map[int64]feedback) {
	fblistmap = make(map[int64]feedback)
	err := feedback_db_handle.Each(func(key, value []byte) bool {
		timestamp, err := strconv.ParseInt(string(key), 10, 64)
		if err != nil {
			log.Println("Failed parsing for key: ", string(key), err.Error())
			return true
		}
		fb := feedback{} // a field missing from the record must not keep the last one's value
		if err := json.Unmarshal(value, &fb); err != nil {
			log.Println("ERROR: while unMarshalling for key: ", timestamp, value, " Error: ", err)
			return true
		}
		fblistmap[timestamp] = fb
		return true
	})
	if err != nil {
		log.Println("Error reading the feedback records: ", err.Error())
	}
	return
}

func handleLoginSession(rw http.ResponseWriter, req *http.Request) {
	log.Println("method:", req.Method)
	homedirjob := func () {
		uid := cookie.Get_Uid(req)
		// we need this here as first API to be hit to generate homedir and save it to cookie
		homedir := cookie.GetOrUpdateHomeDir(rw, req, uid)
		defer func () {
			if uid == "" {
					// reset the job to delete the guests working dir after a certain deadline
					jobname := utils.REMOVE_JOB_KEY+homedir
					utils.GottyJobs.ResetJob(jobname, utils.DEADLINE_MINUTES*time.Minute, func() {
						utils.RemoveDir(homedir)
					})
			}
		}()

	}
	if req.Method == "POST" {
		defer homedirjob();
		req.ParseForm()
		//var session UserSession
		for key, val := range req.Form {
			log.Println("%s: %s", key, val);
		}
		body, err := ioutil.ReadAll(req.Body)
	    if err != nil {
	        panic(err)
	    }
    	//log.Println("body: ", string(body))
		var session user.UserSession
	    err = json.Unmarshal(body, &session)
	    if err != nil {
	        panic(err)
	    }

	    log.Println("before user: ", *session.User)
	    // fill other fields from lower hierarchy
	    session.Update(session.User)
	    session.LogIn()

	    if user.IsBlocked(session.Uid) {
	        http.Error(rw, "This account is blocked.", http.StatusForbidden)
	        return
	    }

	    log.Println("session: ", session)

	    err = user.UpdateAndStoreSessionData(session.Uid, session.SessionID, &session, false)
		if err == nil {
			log.Println("Session data Successfully written to the SESSION_DB.")
			// now can write/update session cookie here.
		} else {
			log.Println("ERROR: Failed to store the session data in SESSION_DB, error: ", err.Error())
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		err = cookie.Set_SessionCookie(rw, req, session)
		if err != nil {
			log.Println("Error: Set_SessionCookie Failed: ", err.Error())
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			return
		}

	} else if req.Method == "GET" {
		homedirjob();	// first thing to login and create the directory so that other following apis can reuse the homedir
		log.Println("handleLoginSession: GET");
		req.ParseForm()
		//var session UserSession
		for key, val := range req.Form {
			log.Println("%s: %s", key, val);
		}

		var session user.UserSession
		session = cookie.Get_SessionCookie(req)
		// verify the session in local db
		if (user.IsSessionExpired(session.Uid, session.SessionID)==true) {
			session.LogOut()
		}
		log.Println("session returned: ",session)
		rw.Write(utils.JsonMarshal(session))
	}
}


func handleLogoutSession(rw http.ResponseWriter, req *http.Request) {
	log.Println("method:", req.Method)
	if req.Method == "POST" {
		req.ParseForm()
		//var session UserSession
		for key, val := range req.Form {
			log.Println("%s: %s", key, val);
		}
		body, err := ioutil.ReadAll(req.Body)
		    if err != nil {
		        panic(err)
		    }
	    	log.Println("body: ", string(body))
		var session user.UserSession
		session = cookie.Get_SessionCookie(req)
		log.Println("deleting user: "+session.Uid+ " with sessionID: "+session.SessionID+" from SESSION_COOKIE STORE")
		err = cookie.Delete_SessionCookie(rw, req, session)
		if err != nil {
			log.Println("ERROR: deleting user: "+session.Uid+" from SESSION_COOKIE STORE: "+err.Error())
		}
		err = user.UpdateAndStoreSessionData(session.Uid, session.SessionID, &session, true)	// store after deleting this session id(true)
		if err == nil {
			log.Println("Session data Successfully written to the SESSION_DB.")
			// now can write/update session cookie here.
		} else {
			log.Println("ERROR: Failed to store the session data in SESSION_DB, error: ", err.Error())
			http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
			return
		}

	} else if req.Method == "GET" {
		log.Println("Error: invalid request type: GET")
		http.Error(rw, "Internal Server Error: invalid request type", http.StatusInternalServerError)
		return
	}
}

// userProfileReply is the profile as the page receives it. IsAdmin tells the
// account menu to offer a link to /admin; the dashboard checks it again.
type userProfileReply struct {
	user.UserProfile
	IsAdmin bool `json:"isAdmin"`
}

// profilePage is what profile.html is filled from.
type profilePage struct {
	user.UserProfile
	IsAdmin bool
}

func handleUserProfileJson(rw http.ResponseWriter, req *http.Request, status int, up user.UserProfile) {
	rw.WriteHeader(status)
	// nullify the session map before sending probably we will not need it.
	up.SessionMap = nil
	rw.Write(utils.JsonMarshal(userProfileReply{UserProfile: up, IsAdmin: status == http.StatusOK && utils.IsAdminEmail(up.Email)}))
}

func handleUserProfile(rw http.ResponseWriter, req *http.Request) {
	log.Println("handleUserProfile: method:", req.Method)
	if req.Method == "GET" {
		req.ParseForm()
		for key, val := range req.Form {
			log.Println("%s: %s", key, val);
		}

		var json bool

		query, ok := req.Form["q"]
		if !ok || len(query) == 0 {
			json=false
		} else {
			if query[0] == "json" {
				// json data is asked
				json = true
			}
		}
		

		var up user.UserProfile
		var session user.UserSession
		session = cookie.Get_SessionCookie(req)
		// verify the session in local db
		if (user.IsSessionExpired(session.Uid, session.SessionID)==true) {
			session.LogOut()
			//return
			if json == true {
				handleUserProfileJson(rw, req, http.StatusUnauthorized, up)
				return
			}
			errorHandler(rw, req, "Session Expired!! Please Sign in again.", http.StatusUnauthorized)
			return
		}
		log.Println("session returned: ",session)


		// now get the user profile data for rendering
		up, err := user.FetchUserProfileData(session.Uid)
		if err != nil {
			log.Println("ERROR: Fetching UserProfile for user: "+session.Uid+" Error: "+ err.Error())
			if json == true {
				handleUserProfileJson(rw, req, http.StatusNotFound, up)
				return
			}
			errorHandler(rw, req, "User Not Found.", http.StatusNotFound )
			return
		}

		if json == true {
			handleUserProfileJson(rw, req, http.StatusOK, up)
			return
		}

		profileData, err := Asset("static/profile.html")
		if err != nil {
			panic("profile not found") // must be in bindata
		}
		profileTemplate, err := template.New("profile").Parse(string(profileData))
		if err != nil {
			log.Println("profile template parse failed") // must be valid
			errorHandler(rw, req, "404 Page Not Found", http.StatusNotFound)
			return
		}

		profileBuf := new(bytes.Buffer)
		err = profileTemplate.Execute(profileBuf, profilePage{UserProfile: up, IsAdmin: utils.IsAdminEmail(up.Email)})
		if err != nil {
			errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		rw.Write(profileBuf.Bytes())

	} else if req.Method == "POST" {
		log.Println("Error: invalid request type: POST")
		errorHandler(rw, req, "Invalid Request", http.StatusBadRequest)
		return
	}
}


func handlePracticeQuestions(rw http.ResponseWriter, req *http.Request) {
	log.Println("handlePracticeQuestions: method:", req.Method)
	if req.Method == "GET" {
		req.ParseForm()
		for key, val := range req.Form {
			log.Println("%s: %s", key, val);
		}

		practiceData, err := Asset("static/practice.html")
		if err != nil {
			log.Println("Error : practice template not found") // must be in bindata
			errorHandler(rw, req, "404 Page Not Found", http.StatusNotFound)
			return
		}
		practiceTemplate, err := template.New("practice").Parse(string(practiceData))
		if err != nil {
			log.Println("practice template parse failed") // must be valid
			errorHandler(rw, req, "404 Page Not Found", http.StatusNotFound)
			return
		}

		// Render the template (pass data if needed, or nil for no data)
		if err := practiceTemplate.Execute(rw, nil); err != nil {
		    log.Println("Error executing practice template:", err)
			errorHandler(rw, req, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	} else if req.Method == "POST" {
		log.Println("Error: invalid request type: POST")
		errorHandler(rw, req, "Invalid Request Method", http.StatusBadRequest)
		return
	}
}

