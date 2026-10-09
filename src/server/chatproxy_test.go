package server

import (
	"cookie"
	"encoder"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// proxyCall sends one chat request through handleChatProxy as a visitor with a
// valid token, and returns what the proxy answered.
func proxyCall(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := encoder.Encrypt([]byte(openAIToken()), cookie.SECRET_KEY)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(body))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Referer", "http://localhost/")
	req.Header.Set("Authorization", "Bearer "+string(token))
	w := httptest.NewRecorder()
	handleChatProxy(w, req)
	return w
}

// fakeUpstream stands in for OpenAI and OpenRouter: it records the last request
// and answers with status and body.
func fakeUpstream(t *testing.T, status int, body string) (url string, last *http.Request, lastBody *string) {
	t.Helper()
	var got http.Request
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, gotBody = *r, string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &got, &gotBody
}

const gemmaRequest = `{"model":"google/gemma-4-31b-it","messages":[{"role":"user","content":"hi"}]}`

func unavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("not an error body: %q", w.Body.String())
	}
	if w.Code != http.StatusServiceUnavailable || e.Error.Type != "model_unavailable" || e.Error.Message != "Gemma 4 31B isn't available right now" {
		t.Fatalf("got %d %+v", w.Code, e.Error)
	}
	if strings.Contains(w.Body.String(), "providers") {
		t.Errorf("the reason from OpenRouter reached the visitor: %s", w.Body.String())
	}
}

func TestGemmaWithoutAnOpenRouterKeyIsUnavailable(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "")
	unavailable(t, proxyCall(t, gemmaRequest))
}

func TestGemmaGoesToOpenRouterWithItsOwnKeyAndHostRule(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "sk-or-test")
	url, last, lastBody := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	old := openrouterEndpoint
	openrouterEndpoint = url
	t.Cleanup(func() { openrouterEndpoint = old })

	w := proxyCall(t, gemmaRequest)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if last.Header.Get("Authorization") != "Bearer sk-or-test" {
		t.Errorf("OpenRouter was sent the wrong key: %q", last.Header.Get("Authorization"))
	}
	if strings.Contains(*lastBody, "sk-test-openai") || !strings.Contains(*lastBody, `"allow_fallbacks":false`) || !strings.Contains(*lastBody, `"modelrun/fp4"`) {
		t.Errorf("body sent on: %s", *lastBody)
	}
}

func TestOpenRouterRefusalsBecomeUnavailable(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "sk-or-test")
	for _, status := range []int{402, 404, 429, 500, 502} {
		url, _, _ := fakeUpstream(t, status, `{"error":{"message":"No allowed providers are available for the selected model","code":`+"404"+`}}`)
		old := openrouterEndpoint
		openrouterEndpoint = url
		w := proxyCall(t, gemmaRequest)
		openrouterEndpoint = old
		unavailable(t, w)
	}
}

func TestOpenRouterTimeoutBecomesUnavailable(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "sk-or-test")
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	oldURL, oldTimeout := openrouterEndpoint, openRouterTimeout
	openrouterEndpoint, openRouterTimeout = srv.URL, 100*1000*1000 // 100 ms
	defer func() { openrouterEndpoint, openRouterTimeout = oldURL, oldTimeout }()
	unavailable(t, proxyCall(t, gemmaRequest))
}

func TestOpenAIRequestsStillGoToOpenAIWithTheStatusKept(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "sk-or-test")
	url, last, lastBody := fakeUpstream(t, 400, `{"error":{"message":"bad"}}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })

	w := proxyCall(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"bad"`) {
		t.Errorf("an OpenAI error was not passed on as it is: %d %s", w.Code, w.Body.String())
	}
	if last.Header.Get("Authorization") != "Bearer sk-test-openai" || last.Header.Get("X-Title") != "" || strings.Contains(*lastBody, "provider") {
		t.Errorf("OpenAI request: %v %s", last.Header, *lastBody)
	}
}

// OpenRouter can answer 200 with no answer in it. The visitor is told so (and
// why, as far as it is ours to say), is offered another model as for one that
// is not available, and is not charged; a real answer passes untouched.
func TestAnOpenRouterReplyWithNoAnswerInItIsNotPassedOffAsOne(t *testing.T) {
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "sk-or-test")
	call := func(body string) *httptest.ResponseRecorder {
		url, _, _ := fakeUpstream(t, 200, body)
		old := openrouterEndpoint
		openrouterEndpoint = url
		defer func() { openrouterEndpoint = old }()
		return proxyCall(t, gemmaRequest)
	}
	for body, want := range map[string]string{
		`{"error":{"message":"Provider returned error: account 123 has no credit","code":429}}`:                   "its host reported an error",
		`{"choices":[{"finish_reason":"error","error":{"message":"upstream blew up"},"message":{"content":""}}]}`: "its host reported an error",
		`{"choices":[]}`: "the reply had no message in it",
		`{"choices":[{"finish_reason":"length","message":{"content":null,"reasoning":"thinking, thinking, thinking"}}]}`: "used its whole answer budget",
		`{"choices":[{"finish_reason":"stop","message":{"content":"   \n"}}]}`:                                           "it wrote nothing",
		`{"choices":[{"finish_reason":"content_filter","message":{"content":""}}]}`:                                      "stopped without writing anything (content_filter)",
	} {
		w := call(body)
		var e ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
			t.Fatalf("%s: not an error body: %d %q", body, w.Code, w.Body.String())
		}
		if w.Code != http.StatusBadGateway || e.Error.Type != "model_unavailable" || e.Error.Code != "model_empty" {
			t.Errorf("%s: %d %+v", body, w.Code, e.Error)
		}
		if !strings.Contains(e.Error.Message, "Gemma 4 31B sent back an empty answer") || !strings.Contains(e.Error.Message, want) {
			t.Errorf("%s: message %q, want %q in it", body, e.Error.Message, want)
		}
		// what the host wrote does not reach the visitor
		if strings.Contains(w.Body.String(), "account 123") || strings.Contains(w.Body.String(), "blew up") || strings.Contains(w.Body.String(), "thinking, thinking") {
			t.Errorf("%s: the host's words reached the visitor: %s", body, w.Body.String())
		}
		// and nothing was charged: no request balance was written
		for _, c := range w.Result().Cookies() {
			if c.Name == "user-session" && w.Code != http.StatusBadGateway {
				t.Errorf("%s: a cookie was written", body)
			}
		}
	}
	// answers pass as they came, byte for byte
	for _, body := range []string{
		`{"id":"x","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"here you are"}}],"usage":{"total_tokens":9}}`,
		`{"choices":[{"message":{"content":null,"tool_calls":[{"id":"1","type":"function"}]}}]}`,
		`{"choices":[{"message":{"content":[{"type":"text","text":"in parts"}]}}]}`,
		`not json at all`,
	} {
		w := call(body)
		if w.Code != 200 || w.Body.String() != body {
			t.Errorf("an answer was changed: %d %q, sent %q", w.Code, w.Body.String(), body)
		}
	}
}

func TestEmptyAnswerReadsOnlyWhatItCan(t *testing.T) {
	for body, empty := range map[string]bool{
		`{"choices":[{"message":{"content":"x"}}]}`:  false,
		`{"choices":[{"message":{"content":""}}]}`:   true,
		`{"choices":[{"message":{}}]}`:               true,
		`{"choices":[{"message":{"content":null}}]}`: true,
		``:                   false, // not ours to judge
		`[1,2,3]`:            false,
		`{"object":"other"}`: true, // a completion without choices has no answer
	} {
		if got := emptyAnswer([]byte(body)) != ""; got != empty {
			t.Errorf("%q: empty=%v, want %v (%q)", body, got, empty, emptyAnswer([]byte(body)))
		}
	}
}
