package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"utils"
)

func TestWhatLooksLikeSpamIsNamedAndWhatDoesNotIsLeftAlone(t *testing.T) {
	spam := map[string]feedback{
		"link tags":                 {Message: "Привет! Подробности здесь: [url=https://dragon-app.org]скачать[/url]"},
		"many links":                {Message: "see http://a.example http://b.example and https://c.example"},
		"the template":              {Name: "Thank you very much for your inquiry We appreciate you 8895889 https://youtube.com faceapple !", Message: "Thank you very much for your inquiry"},
		"casino":                    {Message: "Best online casino bonus for you"},
		"an advertisement":          {Message: "Cheap backlinks for your site https://seo.example"},
		"a link in the name":        {Name: "http://spam.example", Message: "Please add Kotlin to the list of languages"},
		"nothing but a link":        {Message: "https://youtube.com/watch?v=x"},
		"link tags in the template": {Message: "[URL=http://x.example]x[/URL]"},
	}
	for name, fb := range spam {
		if spamReason(fb) == "" {
			t.Errorf("%s was not recognised as spam: %+v", name, fb)
		}
	}
	fine := []feedback{
		{Message: "Add Kotlin, please."},
		{Message: "hi"},
		{Message: "Привет! Можно добавить поддержку Kotlin?"},
		{Message: "Bug: the terminal hangs on Python 3. Details at https://github.com/vickeykumar/openrepl/issues/12 — happens every time I press Run."},
		{Name: "Ann", Email: "ann@example.com", Message: "Love the site. Could you add dark mode to the blog?"},
		{Message: "Hi, ego volo scire vestri pretium."}, // odd, but nothing a filter should refuse
	}
	for _, fb := range fine {
		if r := spamReason(fb); r != "" {
			t.Errorf("a real message was called spam (%s): %+v", r, fb)
		}
	}
}

func TestGmailDotsAndTagsAreOneAddress(t *testing.T) {
	cases := map[string]string{
		"A.n.Dr.Ej.V.i.K.a@Gmail.com":     "andrejvika@gmail.com",
		"andrejvika+promo@googlemail.com": "andrejvika@gmail.com",
		"Ann.Lee@Example.com":             "ann.lee@example.com", // only Gmail ignores dots
		"nobody":                          "nobody",
	}
	for in, want := range cases {
		if got := normalizeEmail(in); got != want {
			t.Errorf("normalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnAddressMaySendOnceADay(t *testing.T) {
	l := &emailLimiter{seen: map[string]time.Time{}}
	t0 := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	if ok, _ := l.take("a.n.d@gmail.com", t0); !ok {
		t.Fatal("the first is allowed")
	}
	if ok, wait := l.take("and+x@gmail.com", t0.Add(time.Hour)); ok || wait != 23*time.Hour {
		t.Fatalf("the same Gmail mailbox again: %v %v", ok, wait)
	}
	if ok, _ := l.take("other@example.com", t0.Add(time.Hour)); !ok {
		t.Fatal("another address is allowed")
	}
	if ok, _ := l.take("", t0); !ok {
		t.Fatal("no address is never limited here")
	}
	if ok, _ := l.take("a.n.d@gmail.com", t0.Add(24*time.Hour+time.Second)); !ok {
		t.Fatal("a day later it is allowed again")
	}
}

func TestOneVisitorHasADailyCapToo(t *testing.T) {
	l := &feedbackLimiter{visitor: map[string][]time.Time{}}
	t0 := time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)
	for i := 0; i < feedbackRates.perDay; i++ {
		if ok, _ := l.take("v", t0.Add(time.Duration(i)*time.Hour)); !ok {
			t.Fatalf("message %d refused", i+1)
		}
	}
	if ok, wait := l.take("v", t0.Add(time.Duration(feedbackRates.perDay)*time.Hour)); ok || wait <= 0 {
		t.Fatalf("over the daily cap: %v %v", ok, wait)
	}
	if ok, _ := l.take("v", t0.Add(25*time.Hour)); !ok {
		t.Fatal("a day later it is allowed again")
	}
}

// google stands in for Google's check: "good" passes, "down" is a server error, anything else fails.
func google(t *testing.T) (calls *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		r.ParseForm()
		if r.PostForm.Get("secret") != "s3cret" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error-codes": []string{"invalid-input-secret"}})
			return
		}
		switch r.PostForm.Get("response") {
		case "good":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		case "expired":
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "hostname": "openrepl.com", "error-codes": []string{"timeout-or-duplicate"}})
		case "down":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error-codes": []string{"invalid-input-response"}})
		}
	}))
	old := recaptchaVerifyURL
	recaptchaVerifyURL = srv.URL
	t.Cleanup(func() { recaptchaVerifyURL = old; srv.Close() })
	return &n
}

func TestWithTheSecretSetTheRecaptchaAnswerIsChecked(t *testing.T) {
	feedbackRig(t)
	calls := google(t)
	os.Setenv(utils.EnvRecaptchaSecret, "s3cret")
	t.Cleanup(func() { os.Unsetenv(utils.EnvRecaptchaSecret) })

	// a bot that posts straight to /feedback has no answer
	w := postForm(url.Values{"message": {"Please add Kotlin"}}, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "not a robot") || len(stored()) != 0 || *calls != 0 {
		t.Fatalf("no answer: %d %q stored %d calls %d", w.Code, w.Body.String(), len(stored()), *calls)
	}
	// a made-up answer
	w = postForm(url.Values{"message": {"Please add Kotlin"}, "g-recaptcha-response": {"forged"}}, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "did not pass") || len(stored()) != 0 {
		t.Fatalf("forged: %d %q stored %d", w.Code, w.Body.String(), len(stored()))
	}
	// an answer that is too old or was used already says so
	w = postForm(url.Values{"message": {"Please add Kotlin"}, "g-recaptcha-response": {"expired"}}, nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "ran out of time") || len(stored()) != 0 {
		t.Fatalf("expired: %d %q stored %d", w.Code, w.Body.String(), len(stored()))
	}
	// Google cannot be asked: the message is not kept, and the visitor is told to retry
	w = postForm(url.Values{"message": {"Please add Kotlin"}, "g-recaptcha-response": {"down"}}, nil)
	if w.Code != 503 || len(stored()) != 0 {
		t.Fatalf("google down: %d %q", w.Code, w.Body.String())
	}
	// a good answer
	w = postForm(url.Values{"message": {"Please add Kotlin"}, "g-recaptcha-response": {"good"}}, nil)
	if w.Code != 200 || len(stored()) != 1 {
		t.Fatalf("good: %d %q stored %d", w.Code, w.Body.String(), len(stored()))
	}
}

func TestAJSONClientSendsTheAnswerInItsBody(t *testing.T) {
	feedbackRig(t)
	google(t)
	os.Setenv(utils.EnvRecaptchaSecret, "s3cret")
	t.Cleanup(func() { os.Unsetenv(utils.EnvRecaptchaSecret) })
	send := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/feedback", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "203.0.113.9:1"
		w := httptest.NewRecorder()
		handleFeedback(w, r)
		return w
	}
	if w := send(`{"message":"Dark mode?"}`); w.Code != 400 {
		t.Fatalf("without an answer: %d", w.Code)
	}
	if w := send(`{"message":"Dark mode?","g-recaptcha-response":"good"}`); w.Code != http.StatusCreated {
		t.Fatalf("with one: %d %s", w.Code, w.Body.String())
	}
}

func TestSpamIsThankedAndNothingIsKept(t *testing.T) {
	feedbackRig(t)
	w := postForm(url.Values{"message": {"Best casino: [url=https://x.example]play[/url]"}, "email": {"bot@example.com"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Thanks") || len(stored()) != 0 {
		t.Fatalf("%d %q stored %d", w.Code, w.Body.String(), len(stored()))
	}
}

func TestTheSameAddressCannotSendTwiceInADay(t *testing.T) {
	feedbackRig(t)
	if w := postForm(url.Values{"message": {"Add Zig please"}, "email": {"a.n.d@gmail.com"}}, nil); w.Code != 200 {
		t.Fatalf("first: %d", w.Code)
	}
	w := postForm(url.Values{"message": {"And Nim too"}, "email": {"and+2@gmail.com"}}, nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || len(stored()) != 1 {
		t.Fatalf("second: %d %q stored %d", w.Code, w.Body.String(), len(stored()))
	}
}

func TestWithoutTheSecretTheFormStillWorks(t *testing.T) {
	feedbackRig(t)
	os.Unsetenv(utils.EnvRecaptchaSecret)
	if w := postForm(url.Values{"message": {"Add Zig please"}}, nil); w.Code != 200 || len(stored()) != 1 {
		t.Fatalf("%d stored %d", w.Code, len(stored()))
	}
}

func TestTheInboxMarksSpamAndDeletesItInOneGo(t *testing.T) {
	_, h := adminTestServer(t)
	withFeedbackDB(t, map[string]feedback{
		"1790000000000000001": {Name: "Ann", Email: "ann@example.com", Message: "Love it"},
		"1790000000000000002": {Name: "JamesEmity", Email: "a.j@gmail.com", Message: "Hello [url=https://dragon-app.org]x[/url]"},
		"1790000000000000003": {Name: "Robertdub", Email: "r@example.com", Message: "Hi, ego volo scire vestri pretium."},
		"1790000000000000004": {Name: "Robertdub", Email: "r@example.com", Message: "Hi, ego volo scire vestri pretium."},
	})
	var list struct{ Feedback []feedbackRow }
	decode(t, get(h, "/admin/feedback"), &list)
	spam := map[string]string{}
	for _, f := range list.Feedback {
		spam[f.ID] = f.Spam
	}
	if spam["1790000000000000001"] != "" || spam["1790000000000000002"] == "" || spam["1790000000000000003"] != "" || !strings.Contains(spam["1790000000000000004"], "again") {
		t.Fatalf("marks: %+v", spam)
	}
	w := post(h, "/admin/feedback/spam/delete", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":2`) {
		t.Fatalf("delete spam: %d %s", w.Code, w.Body.String())
	}
	decode(t, get(h, "/admin/feedback"), &list)
	if len(list.Feedback) != 2 {
		t.Fatalf("what is left: %+v", list.Feedback)
	}
}
