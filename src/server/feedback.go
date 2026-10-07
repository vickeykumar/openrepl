package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// POST /feedback takes a message from the contact form on the home page (or any
// client): a name (optional), an email (optional, checked when given) and a
// message. It is public, so it limits what it takes: the size of the request
// and of each field, and how often one visitor and the whole site may send. The
// messages are read in the admin dashboard (LLD 13), not here.
//
// A client that asks for JSON (Accept: application/json, or a JSON body) gets
// JSON and proper status codes; anything else gets the plain text the old form
// expected.

const (
	feedbackMaxBody    = 16 * 1024
	feedbackNameMax    = 100
	feedbackEmailMax   = 254
	feedbackMessageMax = 4000
)

// How often feedback may be sent: per visitor address, and by everybody.
var feedbackRates = struct {
	perVisitor     int
	perVisitorSpan time.Duration
	global         int
	globalSpan     time.Duration
}{3, 10 * time.Minute, 60, time.Hour}

// feedbackLimiter is a sliding window of the times of the messages taken.
type feedbackLimiter struct {
	mu      sync.Mutex
	visitor map[string][]time.Time
	all     []time.Time
}

var feedbackLimit = &feedbackLimiter{visitor: map[string][]time.Time{}}

func recent(times []time.Time, now time.Time, span time.Duration) []time.Time {
	i := 0
	for i < len(times) && now.Sub(times[i]) >= span {
		i++
	}
	return times[i:]
}

// take records a message of the visitor, or says how long to wait.
func (l *feedbackLimiter) take(ip string, now time.Time) (ok bool, wait time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	mine := recent(l.visitor[ip], now, feedbackRates.perVisitorSpan)
	if len(mine) >= feedbackRates.perVisitor {
		l.visitor[ip] = mine
		return false, feedbackRates.perVisitorSpan - now.Sub(mine[0])
	}
	l.all = recent(l.all, now, feedbackRates.globalSpan)
	if len(l.all) >= feedbackRates.global {
		return false, feedbackRates.globalSpan - now.Sub(l.all[0])
	}
	l.visitor[ip] = append(mine, now)
	l.all = append(l.all, now)
	if len(l.visitor) > 10000 { // forget the visitors whose window has passed
		for k, v := range l.visitor {
			if len(recent(v, now, feedbackRates.perVisitorSpan)) == 0 {
				delete(l.visitor, k)
			}
		}
	}
	return true, 0
}

// feedbackProblem is why a message was refused.
type feedbackProblem struct {
	status  int
	message string
}

// cleanLine trims a one-line field and drops control characters.
func cleanLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

// cleanText trims a message and drops control characters other than line
// breaks and tabs.
func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s))
}

// checkFeedback cleans and checks the fields.
func checkFeedback(name, email, message string) (feedback, *feedbackProblem) {
	fb := feedback{Name: cleanLine(name), Email: cleanLine(email), Message: cleanText(message)}
	switch {
	case fb.Message == "":
		return fb, &feedbackProblem{http.StatusBadRequest, "Write a message first."}
	case utf8.RuneCountInString(fb.Message) > feedbackMessageMax:
		return fb, &feedbackProblem{http.StatusBadRequest, fmt.Sprintf("That message is too long. Keep it under %d characters.", feedbackMessageMax)}
	case utf8.RuneCountInString(fb.Name) > feedbackNameMax:
		return fb, &feedbackProblem{http.StatusBadRequest, fmt.Sprintf("That name is too long. Keep it under %d characters.", feedbackNameMax)}
	}
	if fb.Email != "" {
		addr, err := mail.ParseAddress(fb.Email)
		if err != nil || addr.Address != fb.Email || len(fb.Email) > feedbackEmailMax || !strings.Contains(fb.Email[strings.LastIndex(fb.Email, "@"):], ".") {
			return fb, &feedbackProblem{http.StatusBadRequest, "That email address doesn't look right."}
		}
	}
	return fb, nil
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json") ||
		strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

func feedbackReply(w http.ResponseWriter, r *http.Request, status int, message string) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": status < 300, "message": message})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, message)
}

func handleFeedback(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			// the list used to be here; the messages are read in the dashboard
			http.Redirect(rw, req, "/admin#feedback", http.StatusFound)
			return
		}
		rw.Header().Set("Allow", "POST")
		feedbackReply(rw, req, http.StatusMethodNotAllowed, "Use POST to send feedback.")
		return
	}

	req.Body = http.MaxBytesReader(rw, req.Body, feedbackMaxBody)
	var name, email, message, trap string
	if strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") {
		var in struct {
			Name    string `json:"name"`
			Email   string `json:"email"`
			Message string `json:"message"`
			Website string `json:"website"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			status, msg := http.StatusBadRequest, "The message was not valid JSON."
			if strings.Contains(err.Error(), "too large") {
				status, msg = http.StatusRequestEntityTooLarge, "That message is too big."
			}
			feedbackReply(rw, req, status, msg)
			return
		}
		name, email, message, trap = in.Name, in.Email, in.Message, in.Website
	} else {
		if err := req.ParseForm(); err != nil {
			status, msg := http.StatusBadRequest, "The form could not be read."
			if strings.Contains(err.Error(), "too large") {
				status, msg = http.StatusRequestEntityTooLarge, "That message is too big."
			}
			feedbackReply(rw, req, status, msg)
			return
		}
		name, email, message, trap = req.PostForm.Get("name"), req.PostForm.Get("email"), req.PostForm.Get("message"), req.PostForm.Get("website")
	}

	// "q=delete" was the old admin action; deleting is done in the dashboard now
	if req.URL.Query().Get("q") == "delete" || req.PostForm.Get("q") == "delete" {
		feedbackReply(rw, req, http.StatusGone, "Feedback is managed in the admin dashboard now.")
		return
	}

	// A hidden field that people never fill in: a bot did. It is told it worked
	// and nothing is kept.
	if strings.TrimSpace(trap) != "" {
		feedbackReply(rw, req, http.StatusOK, "Thanks, your message was sent.")
		return
	}

	fb, problem := checkFeedback(name, email, message)
	if problem != nil {
		feedbackReply(rw, req, problem.status, problem.message)
		return
	}
	if feedback_db_handle == nil {
		feedbackReply(rw, req, http.StatusServiceUnavailable, "Feedback can't be saved right now. Please try again later.")
		return
	}
	if ok, wait := feedbackLimit.take(clientIP(req), time.Now()); !ok {
		secs := int(wait.Seconds()) + 1
		rw.Header().Set("Retry-After", strconv.Itoa(secs))
		feedbackReply(rw, req, http.StatusTooManyRequests, "You've sent a few messages already. Try again in a few minutes.")
		return
	}
	if err := StoreFeedbackData(&fb); err != nil {
		feedbackReply(rw, req, http.StatusInternalServerError, "Feedback can't be saved right now. Please try again later.")
		return
	}
	log.Printf("feedback received (%d characters)", utf8.RuneCountInString(fb.Message)) // what it says is private
	status, msg := http.StatusOK, "Thanks for your Feedback !!"
	if wantsJSON(req) {
		status, msg = http.StatusCreated, "Thanks, your message was sent."
	}
	feedbackReply(rw, req, status, msg)
}
