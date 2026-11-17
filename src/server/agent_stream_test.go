package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cookie"
	"encoder"
)

// the token the page sends to the proxy (as agentCall does)
func agentToken(t *testing.T) (string, error) {
	t.Helper()
	b, err := encoder.Encrypt([]byte(openAIToken()), cookie.SECRET_KEY)
	return string(b), err
}

func TestTheStepToolIsTheJSONOfAStepAndForcesTheCall(t *testing.T) {
	out := withStepTool([]byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"x"}],"response_format":{"type":"json_object"}}`))
	var got struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string `json:"name"`
				Parameters struct {
					Required   []string `json:"required"`
					Properties map[string]struct {
						Items struct {
							Properties map[string]struct {
								Enum []string `json:"enum"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"properties"`
				} `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		Choice map[string]interface{} `json:"tool_choice"`
		Format interface{}            `json:"response_format"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "step" {
		t.Fatalf("the tool: %s", out)
	}
	if got.Format != nil {
		t.Error("the JSON mode stayed beside the tool")
	}
	if fn, _ := got.Choice["function"].(map[string]interface{}); got.Choice["type"] != "function" || fn["name"] != "step" {
		t.Errorf("the call is not forced: %v", got.Choice)
	}
	params := got.Tools[0].Function.Parameters
	if strings.Join(params.Required, ",") != "say,actions,done" {
		t.Errorf("required: %v", params.Required)
	}
	// the actions it may name are the ones the page carries out
	enum := params.Properties["actions"].Items.Properties["type"].Enum
	if strings.Join(enum, ",") != strings.Join(agentActions, ",") {
		t.Errorf("the actions of the tool: %v", enum)
	}
	if _, ok := params.Properties["plan"]; !ok {
		t.Error("the tool has no plan")
	}
	// a body that is not JSON is not changed
	if string(withStepTool([]byte("nope"))) != "nope" {
		t.Error("a bad body was changed")
	}
}

func TestOnlyOpenAIModelsGetTheToolAndOnlyWhenTheSwitchIsOn(t *testing.T) {
	openai, _ := modelByID("gpt-4o-mini")
	cf, _ := modelByID(modelCFGemma)
	if nativeToolFor(GenieSettings{}, openai) {
		t.Error("the tool was given with the switch off")
	}
	if !nativeToolFor(GenieSettings{AgentNativeTools: true}, openai) {
		t.Error("the tool was not given with the switch on")
	}
	if nativeToolFor(GenieSettings{AgentNativeTools: true}, cf) {
		t.Error("a Cloudflare model was given the tool")
	}
}

func TestTheProxySendsTheToolToTheModelOnlyForAnAgentStep(t *testing.T) {
	c, lastBody := agentSetup(t, GenieSettings{AgentNativeTools: true})
	if w := agentCall(t, agentStep1, c, ""); w.Code != 200 {
		t.Fatalf("a step: %d %s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"tool_choice"`, `"name":"step"`} {
		if !strings.Contains(*lastBody, want) {
			t.Errorf("the model was not sent %s", want)
		}
	}
	if strings.Contains(*lastBody, "response_format") {
		t.Error("the JSON mode was sent with the tool")
	}
	// a chat message is left alone
	chat := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`
	if w := agentCall(t, chat, c, ""); w.Code != 200 || strings.Contains(*lastBody, "tool_choice") {
		t.Errorf("chat: %d %s", w.Code, *lastBody)
	}
}

// a streamed answer, as OpenAI sends it, one event at a time
func streamingUpstream(t *testing.T, events []string) (url string, release chan struct{}) {
	t.Helper()
	release = make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for i, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			w.(http.Flusher).Flush()
			if i == 0 {
				select {
				case <-release:
				case <-time.After(5 * time.Second):
				}
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL, release
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	pieces chan string
}

func (f *flushRecorder) Write(b []byte) (int, error) {
	n, err := f.ResponseRecorder.Write(b)
	select {
	case f.pieces <- string(b):
	default:
	}
	return n, err
}

func TestAStreamedAnswerIsSentOnAsItIsWrittenAndItsTokensAreCounted(t *testing.T) {
	c := meterSetup(t, GenieSettings{StreamAnswers: true, ModelPrices: map[string]modelPrice{"gpt-4o-mini": {In: 1, Out: 1}}})
	events := []string{
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":2000,"completion_tokens":1000}}`,
	}
	url, release := streamingUpstream(t, events)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })
	req := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Referer", "http://localhost/")
	token, _ := agentToken(t)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(c)
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), pieces: make(chan string, 16)}
	done := make(chan struct{})
	go func() {
		handleChatProxy(rec, req)
		close(done)
	}()
	// the first event reaches the page before the model has finished
	select {
	case p := <-rec.pieces:
		if !strings.Contains(p, "Hel") {
			t.Errorf("the first piece: %q", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first piece was held back until the answer was whole")
	}
	close(release)
	<-done
	if !rec.Flushed {
		t.Error("the answer was not flushed")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"lo"`) || !strings.Contains(body, "[DONE]") {
		t.Errorf("the rest of the answer: %s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("the type: %s", ct)
	}
	// what the stream used is counted
	if r := meter.month("u:uid-ann"); r.Chats != 1 || r.Credits != 3 {
		t.Errorf("the record: %+v", r)
	}
}

func TestAnAnswerComesWholeWhenAnAdminHasNotAllowedStreaming(t *testing.T) {
	c := meterSetup(t, GenieSettings{})
	req := httptest.NewRequest("POST", "/chat/completions", strings.NewReader(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("Referer", "http://localhost/")
	token, _ := agentToken(t)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	handleChatProxy(w, req)
	if w.Code != 200 || strings.Contains(w.Header().Get("Content-Type"), "event-stream") || !strings.HasPrefix(strings.TrimSpace(w.Body.String()), "{") {
		t.Errorf("not whole: %d %s %.80s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
}

func TestTheDashboardSavesTheStreamAndToolSwitchesAndTheNewPageIsToldToStream(t *testing.T) {
	_, h := adminTestServer(t)
	body := `{"colorOfTheDay":false,"announcement":{"text":"","level":"info"},"maintenance":{"enabled":false,"message":""},"disabledLanguages":[],` +
		`"genie":{"disabled":false,"agentNativeTools":true,"streamAnswers":true}}`
	if w := post(h, "/admin/settings", body); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	got := GetSiteSettings().Genie
	if !got.AgentNativeTools || !got.StreamAnswers {
		t.Fatalf("in use: %+v", got)
	}
	pub := httptest.NewRecorder()
	handleSettingsJS(pub, httptest.NewRequest("GET", "/settings.js", nil))
	if !strings.Contains(pub.Body.String(), `"genieStream":true`) || strings.Contains(pub.Body.String(), "agentNativeTools") {
		t.Errorf("settings.js: %s", pub.Body.String())
	}
	var log struct{ Entries []struct{ Detail string } }
	decode(t, get(h, "/admin/audit"), &log)
	found := false
	for _, e := range log.Entries {
		found = found || strings.Contains(e.Detail, "native tool calling on")
	}
	if !found {
		t.Errorf("audit: %s", get(h, "/admin/audit").Body.String())
	}
}
