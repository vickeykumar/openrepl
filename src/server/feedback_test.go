package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// feedbackRig gives a test an empty feedback database and a fresh limiter.
func feedbackRig(t *testing.T) {
	t.Helper()
	withFeedbackDB(t, nil)
	old := feedbackLimit
	feedbackLimit = &feedbackLimiter{visitor: map[string][]time.Time{}}
	t.Cleanup(func() { feedbackLimit = old })
}

func stored() []feedback {
	var out []feedback
	for _, fb := range FetchFeedbackDataMap() {
		out = append(out, fb)
	}
	return out
}

func postForm(fields url.Values, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/feedback", strings.NewReader(fields.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "203.0.113.5:4444"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	handleFeedback(w, r)
	return w
}

func TestAFormMessageIsStoredAndThanked(t *testing.T) {
	feedbackRig(t)
	w := postForm(url.Values{"name": {"  Ann  "}, "email": {"ann@example.com"}, "message": {"Add Kotlin, please.\r\nThanks"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Thanks for your Feedback") {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	got := stored()
	if len(got) != 1 || got[0].Name != "Ann" || got[0].Email != "ann@example.com" || got[0].Message != "Add Kotlin, please.\nThanks" {
		t.Fatalf("stored: %+v", got)
	}
}

func TestAJSONClientGetsJSONAndStatusCodes(t *testing.T) {
	feedbackRig(t)
	body, _ := json.Marshal(map[string]string{"name": "Bo", "message": "Dark mode on the blog?"})
	r := httptest.NewRequest("POST", "/feedback", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "203.0.113.6:1"
	w := httptest.NewRecorder()
	handleFeedback(w, r)
	var out struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 201 || !out.OK || w.Header().Get("Content-Type") != "application/json" || len(stored()) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// and a refusal is JSON too
	r = httptest.NewRequest("POST", "/feedback", strings.NewReader(`{"message":"  "}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handleFeedback(w, r)
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 400 || out.OK || out.Message == "" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestMessagesAreChecked(t *testing.T) {
	feedbackRig(t)
	long := strings.Repeat("x", feedbackMessageMax+1)
	for name, f := range map[string]url.Values{
		"no message":          {"message": {""}},
		"only spaces":         {"message": {" \n\t "}},
		"message too long":    {"message": {long}},
		"name too long":       {"name": {strings.Repeat("n", feedbackNameMax+1)}, "message": {"hi"}},
		"email without a dot": {"email": {"ann@localhost"}, "message": {"hi"}},
		"not an email":        {"email": {"not an email"}, "message": {"hi"}},
		"email with a name":   {"email": {"Ann <ann@example.com>"}, "message": {"hi"}},
		"two emails":          {"email": {"a@example.com, b@example.com"}, "message": {"hi"}},
	} {
		if w := postForm(f, nil); w.Code != 400 {
			t.Errorf("%s: %d %q", name, w.Code, w.Body.String())
		}
	}
	if len(stored()) != 0 {
		t.Fatalf("a refused message was stored: %+v", stored())
	}
	// no email, and no name, is fine; control characters are dropped
	if w := postForm(url.Values{"message": {"hello\x00 there\x07"}, "name": {"A\x00nn"}}, nil); w.Code != 200 {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if got := stored(); len(got) != 1 || got[0].Message != "hello there" || got[0].Name != "Ann" || got[0].Email != "" {
		t.Fatalf("stored: %+v", got)
	}
}

func TestAHugeRequestIsRefused(t *testing.T) {
	feedbackRig(t)
	w := postForm(url.Values{"message": {strings.Repeat("y", feedbackMaxBody*2)}}, nil)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if len(stored()) != 0 {
		t.Fatal("stored")
	}
}

func TestABotThatFillsTheHiddenFieldIsThankedAndIgnored(t *testing.T) {
	feedbackRig(t)
	w := postForm(url.Values{"message": {"buy cheap pills"}, "website": {"http://spam.example"}}, nil)
	if w.Code != 200 || len(stored()) != 0 {
		t.Fatalf("%d, stored %d", w.Code, len(stored()))
	}
}

func TestOneVisitorMayNotSendMoreThanThreeInTenMinutes(t *testing.T) {
	feedbackRig(t)
	send := func(ip string) *httptest.ResponseRecorder {
		return postForm(url.Values{"message": {"hi"}}, func(r *http.Request) { r.Header.Set("X-Forwarded-For", ip) })
	}
	for i := 0; i < 3; i++ {
		if w := send("198.51.100.1"); w.Code != 200 {
			t.Fatalf("message %d: %d", i+1, w.Code)
		}
	}
	w := send("198.51.100.1")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "Try again in a few minutes") {
		t.Fatalf("the fourth: %d %q retry-after %q", w.Code, w.Body.String(), w.Header().Get("Retry-After"))
	}
	if w := send("198.51.100.2"); w.Code != 200 {
		t.Fatalf("another visitor: %d", w.Code)
	}
	if len(stored()) != 4 {
		t.Fatalf("stored %d, want 4", len(stored()))
	}
	// a refused message (a typo) does not use up the allowance
	for i := 0; i < 5; i++ {
		postForm(url.Values{"message": {""}}, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "198.51.100.3") })
	}
	if w := send("198.51.100.3"); w.Code != 200 {
		t.Fatalf("after refused messages: %d", w.Code)
	}
}

func TestTheLimiterWindowsMoveOn(t *testing.T) {
	l := &feedbackLimiter{visitor: map[string][]time.Time{}}
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if ok, _ := l.take("a", t0.Add(time.Duration(i)*time.Minute)); !ok {
			t.Fatalf("take %d refused", i)
		}
	}
	ok, wait := l.take("a", t0.Add(5*time.Minute))
	if ok || wait != 5*time.Minute {
		t.Fatalf("a fourth within the window: %v %v", ok, wait)
	}
	if ok, _ := l.take("a", t0.Add(10*time.Minute)); !ok {
		t.Fatal("the first message has left the window, so one more is allowed")
	}
}

func TestTheWholeSiteHasACapToo(t *testing.T) {
	l := &feedbackLimiter{visitor: map[string][]time.Time{}}
	t0 := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for i := 0; i < feedbackRates.global; i++ {
		if ok, _ := l.take("visitor-"+string(rune('A'+i%26))+string(rune('a'+i/26)), t0.Add(time.Duration(i)*time.Second)); !ok {
			t.Fatalf("message %d of the hour refused", i)
		}
	}
	if ok, wait := l.take("someone-new", t0.Add(90*time.Second)); ok || wait <= 0 {
		t.Fatalf("over the site's cap: %v %v", ok, wait)
	}
	if ok, _ := l.take("someone-new", t0.Add(time.Hour+time.Second)); !ok {
		t.Fatal("an hour later it is allowed again")
	}
}

func TestTheOldAdminPageAndDeleteAreGone(t *testing.T) {
	feedbackRig(t)
	postForm(url.Values{"message": {"keep me"}}, nil)
	var key string
	for k := range FetchFeedbackDataMap() {
		key = strconv.FormatInt(k, 10)
	}
	// GET goes to the dashboard
	w := httptest.NewRecorder()
	handleFeedback(w, httptest.NewRequest("GET", "/feedback", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/admin#feedback" {
		t.Fatalf("GET: %d %q", w.Code, w.Header().Get("Location"))
	}
	// the old delete does nothing and says so
	w = postForm(url.Values{"q": {"delete"}, "key": {key}}, nil)
	if w.Code != http.StatusGone || len(stored()) != 1 {
		t.Fatalf("delete: %d, stored %d", w.Code, len(stored()))
	}
	w = httptest.NewRecorder()
	handleFeedback(w, httptest.NewRequest("PUT", "/feedback", nil))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
		t.Fatalf("PUT: %d %q", w.Code, w.Header().Get("Allow"))
	}
}

func TestWhatPeopleWriteIsNotInTheLog(t *testing.T) {
	feedbackRig(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	postForm(url.Values{"name": {"Secret Sam"}, "email": {"sam@private.example"}, "message": {"my private opinion"}}, nil)
	for _, leaked := range []string{"Secret Sam", "sam@private.example", "my private opinion"} {
		if strings.Contains(buf.String(), leaked) {
			t.Errorf("the log has %q:\n%s", leaked, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "feedback received") {
		t.Errorf("the log does not say a message came:\n%s", buf.String())
	}
}

func TestWithoutADatabaseFeedbackIsAnswered503(t *testing.T) {
	feedbackRig(t)
	old := feedback_db_handle
	feedback_db_handle = nil
	defer func() { feedback_db_handle = old }()
	if w := postForm(url.Values{"message": {"hi"}}, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", w.Code)
	}
}
