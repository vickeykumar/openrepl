package server

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cookie"
	"user"
	"utils"
)

// What visitors leave on the site, for the dashboard: feedback, shared code
// and accounts. Every handler here runs behind adminAPI.

var errNoSuchRecord = errors.New("no such record")

var (
	feedbackIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)
	uidPattern        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// ---- feedback ------------------------------------------------------------------

// feedbackRow is one message of the inbox.
type feedbackRow struct {
	ID      string `json:"id"`
	Time    string `json:"time"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
	Read    bool   `json:"read"`
}

func feedbackRows() []feedbackRow {
	if feedback_db_handle == nil {
		return []feedbackRow{}
	}
	m := FetchFeedbackDataMap()
	keys := make([]int64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] > keys[j] }) // newest first
	rows := make([]feedbackRow, 0, len(keys))
	for _, k := range keys {
		fb := m[k]
		rows = append(rows, feedbackRow{
			ID:      strconv.FormatInt(k, 10),
			Time:    time.Unix(0, k).UTC().Format(time.RFC3339),
			Name:    fb.Name,
			Email:   fb.Email,
			Message: fb.Message,
			Read:    fb.Read,
		})
	}
	return rows
}

// csvCell stops a spreadsheet from running a cell as a formula: a visitor
// controls the text of every field of the feedback form.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (server *Server) handleAdminFeedback(w http.ResponseWriter, r *http.Request) {
	parts := server.adminParts(r)
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		rows := feedbackRows()
		if r.URL.Query().Get("format") == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="openrepl-feedback.csv"`)
			cw := csv.NewWriter(w)
			cw.Write([]string{"time", "name", "email", "message", "read"})
			for _, f := range rows {
				cw.Write([]string{f.Time, csvCell(f.Name), csvCell(f.Email), csvCell(f.Message), strconv.FormatBool(f.Read)})
			}
			cw.Flush()
			return
		}
		unread := 0
		for _, f := range rows {
			if !f.Read {
				unread++
			}
		}
		adminJSON(w, http.StatusOK, map[string]interface{}{"feedback": rows, "unread": unread})
	case len(parts) == 3 && r.Method == http.MethodPost && feedbackIDPattern.MatchString(parts[1]):
		id := parts[1]
		switch parts[2] {
		case "read", "unread":
			if err := setFeedbackRead(id, parts[2] == "read"); err != nil {
				adminError(w, http.StatusNotFound, "No such message.")
				return
			}
			adminJSON(w, http.StatusOK, map[string]string{"id": id})
		case "delete":
			if feedback_db_handle == nil {
				adminError(w, http.StatusNotFound, "No such message.")
				return
			}
			if err := deleteFeedbackData(id); err != nil {
				adminError(w, http.StatusNotFound, "No such message.")
				return
			}
			server.audit(r, "feedback delete", "message "+id)
			adminJSON(w, http.StatusOK, map[string]string{"id": id})
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func setFeedbackRead(id string, read bool) error {
	if feedback_db_handle == nil {
		return errNoSuchRecord
	}
	data, err := feedback_db_handle.Fetch([]byte(id))
	if err != nil || len(data) == 0 {
		return errNoSuchRecord
	}
	var fb feedback
	if err := json.Unmarshal(data, &fb); err != nil {
		return err
	}
	fb.Read = read
	data, err = json.Marshal(fb)
	if err != nil {
		return err
	}
	if err := feedback_db_handle.Store([]byte(id), data); err != nil {
		return err
	}
	return feedback_db_handle.Commit()
}

// ---- shared code ---------------------------------------------------------------

type snippetRow struct {
	ID      string `json:"id"`
	Lang    string `json:"lang"`
	Created string `json:"created"`
	Bytes   int    `json:"bytes"`
	Preview string `json:"preview"`
	Code    string `json:"code,omitempty"`
}

const snippetListMax = 300

func preview(code string) string {
	code = strings.TrimSpace(code)
	if utf8.RuneCountInString(code) <= 160 {
		return code
	}
	return string([]rune(code)[:160]) + "…"
}

// listSnippets returns the newest shared snippets.
func listSnippets() ([]snippetRow, error) {
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	rows := []snippetRow{}
	if snippet_db_handle == nil {
		return rows, errSnippetsOff
	}
	err := snippet_db_handle.Each(func(key, value []byte) bool {
		if !snippetIDPattern.MatchString(string(key)) {
			return true
		}
		var s snippet
		if json.Unmarshal(value, &s) != nil {
			return true
		}
		rows = append(rows, snippetRow{
			ID:      string(key),
			Lang:    s.Lang,
			Created: time.Unix(s.Created, 0).UTC().Format(time.RFC3339),
			Bytes:   len(s.Code),
			Preview: preview(s.Code),
		})
		return true
	})
	if err != nil {
		return rows, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created > rows[j].Created })
	if len(rows) > snippetListMax {
		rows = rows[:snippetListMax]
	}
	return rows, nil
}

func deleteSnippet(id string) error {
	if !snippetIDPattern.MatchString(id) {
		return errNoSuchRecord
	}
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	if snippet_db_handle == nil {
		return errSnippetsOff
	}
	if data, err := snippet_db_handle.Fetch([]byte(id)); err != nil || len(data) == 0 {
		return errNoSuchRecord
	}
	if err := snippet_db_handle.Delete([]byte(id)); err != nil {
		return err
	}
	return snippet_db_handle.Commit()
}

func (server *Server) handleAdminSnippets(w http.ResponseWriter, r *http.Request) {
	parts := server.adminParts(r)
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		rows, err := listSnippets()
		if err == errSnippetsOff {
			adminJSON(w, http.StatusOK, map[string]interface{}{"snippets": rows, "off": true})
			return
		}
		if err != nil {
			adminError(w, http.StatusInternalServerError, "Could not read the shared snippets.")
			return
		}
		adminJSON(w, http.StatusOK, map[string]interface{}{"snippets": rows})
	case len(parts) == 2 && r.Method == http.MethodGet:
		s, ok := fetchSnippet(parts[1])
		if !ok {
			adminError(w, http.StatusNotFound, "No such snippet.")
			return
		}
		adminJSON(w, http.StatusOK, snippetRow{
			ID: parts[1], Lang: s.Lang, Created: time.Unix(s.Created, 0).UTC().Format(time.RFC3339),
			Bytes: len(s.Code), Code: s.Code,
		})
	case len(parts) == 3 && parts[2] == "delete" && r.Method == http.MethodPost:
		if err := deleteSnippet(parts[1]); err != nil {
			adminError(w, http.StatusNotFound, "No such snippet.")
			return
		}
		server.audit(r, "snippet delete", "snippet "+parts[1])
		adminJSON(w, http.StatusOK, map[string]string{"id": parts[1]})
	default:
		http.NotFound(w, r)
	}
}

// ---- accounts ------------------------------------------------------------------

func (server *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	parts := server.adminParts(r)
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		accounts, err := user.ListAccounts()
		if err != nil {
			adminError(w, http.StatusInternalServerError, "Could not read the accounts.")
			return
		}
		// An admin's address is not hidden from admins, but the list says which
		// accounts are admins so that nobody blocks one by mistake.
		type row struct {
			user.Account
			Admin bool `json:"admin"`
		}
		rows := make([]row, len(accounts))
		for i, a := range accounts {
			rows[i] = row{Account: a, Admin: utils.IsAdminEmail(a.Email)}
		}
		adminJSON(w, http.StatusOK, map[string]interface{}{"users": rows})
	case len(parts) == 3 && r.Method == http.MethodPost && uidPattern.MatchString(parts[1]):
		uid, action := parts[1], parts[2]
		up, err := user.FetchUserProfileData(uid)
		if err != nil {
			adminError(w, http.StatusNotFound, "No such account.")
			return
		}
		switch action {
		case "signout":
			if err := user.SignOutEverywhere(uid); err != nil {
				adminError(w, http.StatusInternalServerError, "Could not sign the account out.")
				return
			}
			server.audit(r, "user sign out", up.Email)
		case "block":
			if uid == cookie.Get_Uid(r) || utils.IsAdminEmail(up.Email) {
				adminError(w, http.StatusBadRequest, "An admin account cannot be blocked. Remove it from the admin list first.")
				return
			}
			if err := user.SetBlocked(uid, true); err != nil {
				adminError(w, http.StatusInternalServerError, "Could not block the account.")
				return
			}
			server.audit(r, "user block", up.Email)
		case "unblock":
			if err := user.SetBlocked(uid, false); err != nil {
				adminError(w, http.StatusInternalServerError, "Could not unblock the account.")
				return
			}
			server.audit(r, "user unblock", up.Email)
		default:
			http.NotFound(w, r)
			return
		}
		adminJSON(w, http.StatusOK, map[string]string{"uid": uid})
	default:
		http.NotFound(w, r)
	}
}
