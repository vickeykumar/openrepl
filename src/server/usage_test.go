package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func usageCall(t *testing.T, c *http.Cookie) genieUsage {
	t.Helper()
	req := httptest.NewRequest("GET", "/chat/usage", nil)
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handleChatUsage(w, req)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("usage: %d %v", w.Code, w.Header())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Error("asking for the usage saved a cookie: it must not count as a request")
	}
	var u genieUsage
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return u
}

func TestAGuestIsToldWhatIsLeftWithoutBeingCharged(t *testing.T) {
	agentSetup(t, GenieSettings{})
	for i := 0; i < 3; i++ {
		u := usageCall(t, nil)
		// a guest starts full: 0.33 a minute for an hour
		if u.Cap < 19 || u.Cap > 20 || u.Left != u.Cap || u.PerMinute != 0.33 || u.SignedIn || u.Unlimited || u.Agent != nil {
			t.Fatalf("a guest: %+v", u)
		}
	}
	w := httptest.NewRecorder()
	handleChatUsage(w, httptest.NewRequest("POST", "/chat/usage", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestAnAnswerSaysWhatIsLeftAfterIt(t *testing.T) {
	c, _ := agentSetup(t, GenieSettings{AgentTasksPerHour: 5})
	before := usageCall(t, c)
	if !before.SignedIn || before.Cap != 60 || before.Left != 60 || before.PerMinute != 1 {
		t.Fatalf("signed in, before: %+v", before)
	}
	if before.Agent == nil || before.Agent.Used != 0 || before.Agent.PerHour != 5 || before.Agent.MaxSteps != defaultAgentMaxSteps {
		t.Fatalf("agent, before: %+v", before.Agent)
	}

	w := agentCall(t, agentStep1, c, "")
	if w.Code != 200 {
		t.Fatalf("the task: %d %s", w.Code, w.Body.String())
	}
	var after genieUsage
	if err := json.Unmarshal([]byte(w.Header().Get(usageHeader)), &after); err != nil {
		t.Fatalf("the usage header %q: %v", w.Header().Get(usageHeader), err)
	}
	// the first step of a task takes one request and one of the hour's tasks
	if after.Left < 58.9 || after.Left > 59.1 || after.Cap != 60 || !after.SignedIn {
		t.Errorf("after the task: %+v", after)
	}
	if after.Agent == nil || after.Agent.Used != 1 || after.Agent.NextFreeSeconds < 3500 || after.Agent.NextFreeSeconds > 3600 {
		t.Errorf("agent, after the task: %+v", after.Agent)
	}
	found := false
	for _, h := range w.Header().Values("Access-Control-Expose-Headers") {
		if strings.Contains(h, usageHeader) {
			found = true
		}
	}
	if !found {
		t.Error("the page cannot read the usage header: it is not exposed")
	}
	// and the count is the same when asked for
	if u := usageCall(t, c); u.Agent == nil || u.Agent.Used != 1 {
		t.Errorf("asked afterwards: %+v", u.Agent)
	}
}

func TestAgentUsageIsLeftOutWhereAgentModeIsOff(t *testing.T) {
	c, _ := agentSetup(t, GenieSettings{})
	if err := SaveSiteSettings(SiteSettings{Genie: GenieSettings{AgentDisabled: true}}); err != nil {
		t.Fatal(err)
	}
	if u := usageCall(t, c); u.Agent != nil || !u.SignedIn {
		t.Errorf("agent mode off: %+v", u)
	}
}
