package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"

	"cookie"
	"user"
	"utils"
)

// Admin accounts. There are two kinds:
//
//   - owners: the accounts in OPENREPL_ADMIN_EMAILS (or the settings file's
//     user.email). They are fixed by whoever runs the server and cannot be
//     removed here;
//   - added admins: accounts an owner added in the dashboard. They are kept in
//     the settings (SiteSettings.Admins) and can do everything in the
//     dashboard except change this list, so an added admin cannot add or
//     remove anyone, itself included.
//
// An admin is whoever signs in with an account whose address is on either list
// (utils.IsAdminEmail).

// isOwner reports whether the request comes from an owner.
func (server *Server) isOwner(w http.ResponseWriter, r *http.Request) bool {
	if server.admin.owner != nil {
		return server.admin.owner(w, r)
	}
	session := cookie.Get_SessionCookie(r)
	up, err := user.FetchUserProfileData(session.Uid)
	if err != nil || user.IsSessionExpired(session.Uid, session.SessionID) {
		return false
	}
	return utils.IsOwnerEmail(up.Email)
}

type adminsReply struct {
	Owners []string `json:"owners"`
	Admins []string `json:"admins"`
	// CanManage is true when the person asking is an owner.
	CanManage bool `json:"canManage"`
	Max       int  `json:"max"`
}

func (server *Server) currentAdminsReply(w http.ResponseWriter, r *http.Request) adminsReply {
	owners := utils.AdminEmails()
	sort.Slice(owners, func(i, j int) bool { return strings.ToLower(owners[i]) < strings.ToLower(owners[j]) })
	admins := GetSiteSettings().Admins
	if admins == nil {
		admins = []string{}
	}
	if owners == nil {
		owners = []string{}
	}
	return adminsReply{Owners: owners, Admins: admins, CanManage: server.isOwner(w, r), Max: maxExtraAdmins}
}

// setAdmins replaces the list of added admins, leaving the rest of the
// settings as they are.
func setAdmins(admins []string) error {
	cur := GetSiteSettings()
	cur.Admins = admins
	return SaveSiteSettings(cur, -1)
}

// handleAdminAdmins lists the admins (GET) and, for owners, adds or removes one
// (POST: {"action": "add"|"remove", "email": "..."}).
func (server *Server) handleAdminAdmins(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		adminJSON(rw, http.StatusOK, server.currentAdminsReply(rw, req))
	case http.MethodPost:
		if !server.isOwner(rw, req) {
			adminError(rw, http.StatusForbidden, "Only the owners, the accounts in OPENREPL_ADMIN_EMAILS, can add or remove admins.")
			return
		}
		req.Body = http.MaxBytesReader(rw, req.Body, 4*1024)
		var in struct {
			Action string `json:"action"`
			Email  string `json:"email"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			adminError(rw, http.StatusBadRequest, "The request was not valid JSON.")
			return
		}
		email := strings.ToLower(strings.TrimSpace(in.Email))
		if !validEmail(email) {
			adminError(rw, http.StatusBadRequest, "That is not an email address.")
			return
		}
		admins := append([]string(nil), GetSiteSettings().Admins...)
		switch in.Action {
		case "add":
			if utils.IsOwnerEmail(email) {
				adminError(rw, http.StatusConflict, email+" is an owner already.")
				return
			}
			for _, a := range admins {
				if a == email {
					adminError(rw, http.StatusConflict, email+" is an admin already.")
					return
				}
			}
			if len(admins) >= maxExtraAdmins {
				adminError(rw, http.StatusBadRequest, "At most 50 admins can be added.")
				return
			}
			admins = append(admins, email)
		case "remove":
			if utils.IsOwnerEmail(email) {
				adminError(rw, http.StatusConflict, email+" is an owner: it comes from OPENREPL_ADMIN_EMAILS and cannot be removed here.")
				return
			}
			kept := admins[:0]
			found := false
			for _, a := range admins {
				if a == email {
					found = true
					continue
				}
				kept = append(kept, a)
			}
			if !found {
				adminError(rw, http.StatusNotFound, email+" is not an added admin.")
				return
			}
			admins = kept
		default:
			adminError(rw, http.StatusBadRequest, "The action is add or remove.")
			return
		}
		if err := setAdmins(admins); err != nil {
			switch err {
			case errSettingsConflict:
				adminError(rw, http.StatusConflict, "Someone else changed the settings just now. Reload and try again.")
			case errSettingsUnavailable:
				adminError(rw, http.StatusServiceUnavailable, "The settings store is not reachable right now, so nothing was saved.")
			default:
				log.Println("saving the admins failed: ", err)
				adminError(rw, http.StatusInternalServerError, "Could not save the admins. Please try again.")
			}
			return
		}
		if in.Action == "add" {
			server.audit(req, "admins", "admin added: "+email)
			// an account that is signed in keeps its sessions, so the new admin
			// can use the dashboard on its next request
		} else {
			server.audit(req, "admins", "admin removed: "+email)
		}
		adminJSON(rw, http.StatusOK, server.currentAdminsReply(rw, req))
	default:
		rw.Header().Set("Allow", "GET, POST")
		adminError(rw, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}
