package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"utils"
)

// What keeps spam out of the feedback inbox, beyond the hidden field and the
// rate limit in feedback.go:
//
//   1. the reCAPTCHA answer is checked here, with Google (OPENREPL_RECAPTCHA_SECRET).
//      The box on the page only switches on the Send button; a bot posts straight
//      to /feedback, so the check has to be made by the server;
//   2. the text is looked at: link tags, many links, casino and pill spam, and the
//      "Thank you very much for your inquiry" template;
//   3. one message per email address a day (Gmail's dots and +tags do not make a
//      new address), and a daily cap per visitor.
//
// Spam that is recognised is answered as if it was sent, and nothing is kept, as
// with the hidden field: the sender learns nothing to get around.

var (
	// Google's check; a variable so a test can point it at a fake
	recaptchaVerifyURL = "https://www.google.com/recaptcha/api/siteverify"
	recaptchaClient    = &http.Client{Timeout: 6 * time.Second}

	errRecaptchaUnavailable = errors.New("the reCAPTCHA check could not be made")

	recaptchaWarned sync.Once
)

// recaptchaAnswer is what Google says about an answer.
type recaptchaAnswer struct {
	OK       bool
	Codes    []string // Google's error codes, e.g. "timeout-or-duplicate", "invalid-input-response"
	Hostname string   // the site the box was solved on
}

func (a recaptchaAnswer) has(code string) bool {
	for _, c := range a.Codes {
		if c == code {
			return true
		}
	}
	return false
}

// verifyRecaptcha asks Google whether the answer the visitor got from the box is
// good. It returns an error when Google could not be asked, or when the secret is
// not accepted. The visitor's address is not sent: it is optional, and behind a
// proxy it is the proxy's, which says nothing about the visitor.
func verifyRecaptcha(secret, token string) (recaptchaAnswer, error) {
	var answer recaptchaAnswer
	form := url.Values{"secret": {secret}, "response": {token}}
	resp, err := recaptchaClient.PostForm(recaptchaVerifyURL, form)
	if err != nil {
		return answer, errRecaptchaUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return answer, errRecaptchaUnavailable
	}
	var out struct {
		Success    bool     `json:"success"`
		Hostname   string   `json:"hostname"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64*1024)).Decode(&out); err != nil {
		return answer, errRecaptchaUnavailable
	}
	answer = recaptchaAnswer{OK: out.Success, Codes: out.ErrorCodes, Hostname: out.Hostname}
	// a wrong or missing secret is the server's problem, not the visitor's
	if answer.has("missing-input-secret") || answer.has("invalid-input-secret") {
		log.Println("ERROR: reCAPTCHA does not accept the secret", utils.EnvRecaptchaSecret, "(is it the secret of the site key on the page?)")
		return answer, errRecaptchaUnavailable
	}
	return answer, nil
}

// normalizeEmail is the address with what does not change who it is taken out:
// the case, and for Gmail the dots and the +tag of the name (a.n.d.r.e.j@gmail.com
// and andrej+x@gmail.com are one mailbox).
func normalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at < 1 {
		return email
	}
	local, domain := email[:at], email[at+1:]
	if domain == "googlemail.com" {
		domain = "gmail.com"
	}
	if domain == "gmail.com" {
		if plus := strings.Index(local, "+"); plus >= 0 {
			local = local[:plus]
		}
		local = strings.ReplaceAll(local, ".", "")
	}
	return local + "@" + domain
}

var (
	linkPattern = regexp.MustCompile(`(?i)(https?://|www\.)[^\s\]]+`)
	// forum-style link tags, which no person writes into a contact form
	bbcodePattern = regexp.MustCompile(`(?i)\[/?(url|link|img)(=[^\]]*)?\]|<a\s+href`)
	// words that, in a feedback form, only spam uses
	strongSpam = regexp.MustCompile(`(?i)\b(casino|casinos|viagra|cialis|porn|escort|dragon\s?money|dragonmoney|sports?\s?betting|online\s?betting)\b`)
	// words that spam uses along with a link
	weakSpam = regexp.MustCompile(`(?i)\b(forex|backlinks?|seo\s+(services?|packages?)|payday|loans?|crypto\s?(airdrop|giveaway)|bitcoin\s?(doubler|giveaway)|promo\s?code|bonus)\b`)
	// the template that arrives with a random number and a youtube link
	inquiryTemplate = regexp.MustCompile(`(?i)thank you very much for your inquiry`)
)

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// spamReason says why a message looks like spam, or "" when it does not. It
// looks at the name and the message and never refuses a message for being short
// or foreign: only for what a spammer needs to put in it.
func spamReason(fb feedback) string {
	text := fb.Name + "\n" + fb.Message
	links := linkPattern.FindAllString(text, -1)
	switch {
	case bbcodePattern.MatchString(text):
		return "link tags"
	case len(links) >= 3:
		return "many links"
	case inquiryTemplate.MatchString(text):
		return "the \"thank you for your inquiry\" template"
	case strongSpam.MatchString(text):
		return "casino or pill spam"
	case len(links) > 0 && weakSpam.MatchString(text):
		return "an advertisement with a link"
	case linkPattern.MatchString(fb.Name):
		return "a link in the name"
	case len(links) > 0 && countLetters(linkPattern.ReplaceAllString(fb.Message, "")) < 12:
		return "nothing but a link"
	}
	return ""
}

// emailLimiter lets an address send once a day.
type emailLimiter struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var emailLimit = &emailLimiter{seen: map[string]time.Time{}}

const feedbackEmailSpan = 24 * time.Hour

// take records a message of the address, or says how long until the next may be sent.
// An empty address is never limited here (the visitor limit still applies).
func (l *emailLimiter) take(email string, now time.Time) (ok bool, wait time.Duration) {
	if strings.TrimSpace(email) == "" {
		return true, 0
	}
	key := normalizeEmail(email)
	l.mu.Lock()
	defer l.mu.Unlock()
	if at, found := l.seen[key]; found && now.Sub(at) < feedbackEmailSpan {
		return false, feedbackEmailSpan - now.Sub(at)
	}
	l.seen[key] = now
	if len(l.seen) > 10000 {
		for k, at := range l.seen {
			if now.Sub(at) >= feedbackEmailSpan {
				delete(l.seen, k)
			}
		}
	}
	return true, 0
}

// spamReasons labels the stored messages for the dashboard: what spamReason says,
// and a message that an address has already sent in the same words.
// rows are ordered newest first; the map is by message id.
func spamReasons(order []int64, byID map[int64]feedback) map[int64]string {
	out := map[int64]string{}
	type sentKey struct{ email, message string }
	sent := map[sentKey]bool{}
	for i := len(order) - 1; i >= 0; i-- { // oldest first, so the later copy is the repeat
		id := order[i]
		fb := byID[id]
		if r := spamReason(fb); r != "" {
			out[id] = r
			continue
		}
		if strings.TrimSpace(fb.Email) != "" {
			k := sentKey{normalizeEmail(fb.Email), strings.ToLower(strings.TrimSpace(fb.Message))}
			if sent[k] {
				out[id] = "the same message sent again"
			}
			sent[k] = true
		}
	}
	return out
}
