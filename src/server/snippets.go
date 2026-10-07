package server

import (
	"crypto/rand"
	"encoding/json"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"persist"
	"utils"
)

// Share-code links (T13). A snapshot of the editor is saved under a short
// random id: POST /snippet stores one, GET /snippet?id= returns it, and
// /s/<id> opens the workspace (on the language's page) with that code loaded.
// Live-session sharing (#<session> links) is separate and unchanged.

const SNIPPET_DB = utils.GOTTY_PATH + "/snippets.db"

const (
	snippetMaxBytes  = 64 * 1024        // largest editor content we store
	snippetIDLen     = 8                // characters in an id, from snippetAlphabet
	snippetRateLimit = 30               // snippets one visitor can create ...
	snippetRateSpan  = 10 * time.Minute // ... in this window
)

const snippetAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/l/I

var (
	snippet_db_handle persist.Store
	snippetDBMu       sync.Mutex
	snippetIDPattern  = regexp.MustCompile(`^[A-Za-z0-9]{` + "8" + `}$`)
	snippetLangRegexp = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,19}$`)
)

type snippet struct {
	Lang    string `json:"lang"`    // value of the language in #optionlist, e.g. "python"
	Code    string `json:"code"`    // editor content
	Created int64  `json:"created"` // unix seconds
}

func InitSnippetDBHandle() {
	var err error
	snippet_db_handle, err = persist.Open(SNIPPET_DB)
	if err != nil {
		log.Println("ERROR: Error while creating snippet DB handle : ", err.Error())
		snippet_db_handle = nil // share-code links are off, the rest of the site works
		return
	}
	log.Println("Successfully initialized snippet handle, stored in", snippet_db_handle.Backend())
}

func CloseSnippetDBHandle() {
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	if snippet_db_handle == nil {
		return
	}
	if err := snippet_db_handle.Close(); err != nil {
		log.Println("ERROR: Error while closing snippet DB handle : ", err.Error())
	}
	snippet_db_handle = nil
}

// ---- rate limit per visitor -------------------------------------------------

type rateWindow struct {
	start time.Time
	count int
}

var (
	snippetRate   = map[string]*rateWindow{}
	snippetRateMu sync.Mutex
)

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func allowSnippet(ip string) bool {
	snippetRateMu.Lock()
	defer snippetRateMu.Unlock()
	now := time.Now()
	if len(snippetRate) > 10000 { // forget old windows now and then
		for k, w := range snippetRate {
			if now.Sub(w.start) > snippetRateSpan {
				delete(snippetRate, k)
			}
		}
	}
	w, ok := snippetRate[ip]
	if !ok || now.Sub(w.start) > snippetRateSpan {
		snippetRate[ip] = &rateWindow{start: now, count: 1}
		return true
	}
	if w.count >= snippetRateLimit {
		return false
	}
	w.count++
	return true
}

// ---- storage ------------------------------------------------------------------

func newSnippetID() (string, error) {
	b := make([]byte, snippetIDLen)
	max := big.NewInt(int64(len(snippetAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = snippetAlphabet[n.Int64()]
	}
	return string(b), nil
}

func fetchSnippet(id string) (snippet, bool) {
	var s snippet
	if !snippetIDPattern.MatchString(id) {
		return s, false
	}
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	if snippet_db_handle == nil {
		return s, false
	}
	data, err := snippet_db_handle.Fetch([]byte(id))
	if err != nil || len(data) == 0 {
		return s, false
	}
	if json.Unmarshal(data, &s) != nil {
		return s, false
	}
	return s, true
}

func storeSnippet(s snippet) (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	snippetDBMu.Lock()
	defer snippetDBMu.Unlock()
	if snippet_db_handle == nil {
		return "", errSnippetsOff
	}
	for tries := 0; tries < 5; tries++ {
		id, err := newSnippetID()
		if err != nil {
			return "", err
		}
		if existing, _ := snippet_db_handle.Fetch([]byte(id)); len(existing) > 0 {
			continue // taken, pick another
		}
		if err := snippet_db_handle.Store([]byte(id), data); err != nil {
			return "", err
		}
		return id, snippet_db_handle.Commit()
	}
	return "", errSnippetIDs
}

type snippetError string

func (e snippetError) Error() string { return string(e) }

const (
	errSnippetsOff = snippetError("code links are not available on this server")
	errSnippetIDs  = snippetError("could not find a free id")
)

// ---- handlers -----------------------------------------------------------------

func writeSnippetJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handleSnippet serves /snippet: GET ?id=<id> returns {lang, code, created};
// POST {lang, code} stores a snapshot and returns {id, url}.
func handleSnippet(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s, ok := fetchSnippet(r.URL.Query().Get("id"))
		if !ok {
			writeSnippetJSON(w, http.StatusNotFound, map[string]string{"error": "This code link doesn't exist."})
			return
		}
		writeSnippetJSON(w, http.StatusOK, s)
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, snippetMaxBytes*2+1024) // JSON escaping can double the size
		var in snippet
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeSnippetJSON(w, http.StatusBadRequest, map[string]string{"error": "The code is too large or not valid."})
			return
		}
		if !snippetLangRegexp.MatchString(in.Lang) {
			writeSnippetJSON(w, http.StatusBadRequest, map[string]string{"error": "Unknown language."})
			return
		}
		if strings.TrimSpace(in.Code) == "" {
			writeSnippetJSON(w, http.StatusBadRequest, map[string]string{"error": "There's no code to share."})
			return
		}
		if len(in.Code) > snippetMaxBytes {
			writeSnippetJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Code links hold up to 64 KB."})
			return
		}
		if !allowSnippet(clientIP(r)) {
			writeSnippetJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many code links. Try again in a few minutes."})
			return
		}
		id, err := storeSnippet(snippet{Lang: in.Lang, Code: in.Code, Created: time.Now().Unix()})
		if err != nil {
			log.Println("ERROR: storing snippet: ", err.Error())
			writeSnippetJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Couldn't save the code link. Try again later."})
			return
		}
		writeSnippetJSON(w, http.StatusOK, map[string]string{"id": id, "url": siteURL(r) + "/s/" + id})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeSnippetJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Use GET or POST."})
	}
}

// handleSnippetLink serves /s/<id>: it opens the language's page (or the home
// page) with ?s=<id>, and the page loads the code into the editor.
func handleSnippetLink(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/s/"), "/")
	s, ok := fetchSnippet(id)
	if !ok {
		errorHandler(w, r, "This code link doesn't exist. It may have been mistyped.", http.StatusNotFound)
		return
	}
	target := "/?repl=" + url.QueryEscape(s.Lang) + "&s=" + id
	for _, p := range langPages {
		if p.Repl == s.Lang {
			target = "/" + p.Slug + "?s=" + id
			break
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}
