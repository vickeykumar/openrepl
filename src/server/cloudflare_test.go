package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"utils"
)

// The Cloudflare Workers AI models: the proxy calls them with the token and the account id, sends them only
// what they accept, and tells a visitor they are not available when Cloudflare cannot answer.

const cfRequest = `{"model":"@cf/google/gemma-4-26b-a4b-it","messages":[{"role":"user","content":"hi"}],"stream":true}`

func cfEnv(t *testing.T) {
	t.Helper()
	isolateSettings(t)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-test-openai")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENREPL_CLOUDFLARE_API_TOKEN", "cf-test-token")
	t.Setenv("OPENREPL_CLOUDFLARE_ACCOUNT_ID", "0123456789abcdef0123456789abcdef")
}

func cfUnavailable(t *testing.T, code int, body string) {
	t.Helper()
	var e ErrorResponse
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not an error body: %q", body)
	}
	if code != http.StatusServiceUnavailable || e.Error.Type != "model_unavailable" || e.Error.Message != "Gemma 4 26B (Cloudflare) isn't available right now" {
		t.Fatalf("got %d %+v", code, e.Error)
	}
}

func TestACloudflareModelNeedsTheTokenAndTheAccountId(t *testing.T) {
	for _, missing := range []string{"OPENREPL_CLOUDFLARE_API_TOKEN", "OPENREPL_CLOUDFLARE_ACCOUNT_ID"} {
		cfEnv(t)
		t.Setenv(missing, "")
		if utils.CloudflareEnabled() {
			t.Fatalf("enabled without %s", missing)
		}
		w := proxyCall(t, cfRequest)
		cfUnavailable(t, w.Code, w.Body.String())
	}
}

func TestACloudflareModelGoesToItsAccountWithItsOwnTokenAndNothingExtra(t *testing.T) {
	cfEnv(t)
	url, last, lastBody := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	old := cloudflareAPI
	cloudflareAPI = url
	t.Cleanup(func() { cloudflareAPI = old })

	w := proxyCall(t, strings.Replace(cfRequest, `,"stream":true`, "", 1))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if last.URL.Path != "/accounts/0123456789abcdef0123456789abcdef/ai/v1/chat/completions" {
		t.Errorf("called %s", last.URL.Path)
	}
	if last.Header.Get("Authorization") != "Bearer cf-test-token" {
		t.Errorf("Cloudflare was sent the wrong key: %q", last.Header.Get("Authorization"))
	}
	for _, leak := range []string{"sk-test-openai", "sk-or-test", `"provider"`, `"usage"`, "stream_options", "allow_fallbacks"} {
		if strings.Contains(*lastBody, leak) {
			t.Errorf("%s was sent to Cloudflare: %s", leak, *lastBody)
		}
	}
	// the name keeps its @cf/ prefix: without it Cloudflare bills the AI Gateway credit
	if !strings.Contains(*lastBody, `"model":"@cf/google/gemma-4-26b-a4b-it"`) {
		t.Errorf("model in the body: %s", *lastBody)
	}
}

func TestCloudflareRefusalsBecomeUnavailable(t *testing.T) {
	cfEnv(t)
	for _, status := range []int{401, 402, 429, 500, 502} {
		url, _, _ := fakeUpstream(t, status, `{"errors":[{"message":"Insufficient balance; add money to your gateway","code":2021}]}`)
		old := cloudflareAPI
		cloudflareAPI = url
		w := proxyCall(t, cfRequest)
		cloudflareAPI = old
		cfUnavailable(t, w.Code, w.Body.String())
		if strings.Contains(w.Body.String(), "balance") {
			t.Errorf("the reason from Cloudflare reached the visitor: %s", w.Body.String())
		}
	}
}

func TestACloudflareReplyWithNoAnswerIsNotPassedOffAsOne(t *testing.T) {
	cfEnv(t)
	url, _, _ := fakeUpstream(t, 200, `{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`)
	old := cloudflareAPI
	cloudflareAPI = url
	t.Cleanup(func() { cloudflareAPI = old })
	w := proxyCall(t, strings.Replace(cfRequest, `,"stream":true`, "", 1))
	if w.Code == 200 {
		t.Fatalf("an empty reply was passed on as an answer: %s", w.Body.String())
	}
}

func TestCloudflareModelsThatThinkGetRoomForIt(t *testing.T) {
	cfEnv(t)
	got := sanitizedAsIs(t, `{"model":"`+modelCFQwen+`","messages":[{"role":"user","content":"hi"}],"max_tokens":500}`)
	if got["model"] != modelCFQwen || got["max_tokens"] != float64(500+2000) {
		t.Errorf("qwen: %v", got)
	}
	got = sanitizedAsIs(t, `{"model":"`+modelCFGemma+`","messages":[{"role":"user","content":"hi"}],"max_tokens":500}`)
	if got["max_tokens"] != float64(500) {
		t.Errorf("gemma: %v", got)
	}
	for _, id := range []string{modelCFGemma, modelCFQwen, modelCFGLM} {
		if !strings.HasPrefix(id, "@cf/") {
			t.Errorf("%s has no @cf/ prefix", id)
		}
	}
}

func TestModelsOfEveryProviderCanBeAddedAndEachMustLookRight(t *testing.T) {
	isolateSettings(t)
	good := []CustomModel{
		{ID: "nvidia/nemotron-3-super-120b-a12b:free", Name: "Nemotron"},
		{Provider: "openrouter", ID: "meta-llama/llama-3.3-70b-instruct", Name: "Llama", Hosts: []string{"nvidia"}},
		{Provider: "openai", ID: "gpt-4.1-mini", Name: "GPT-4.1 mini"},
		{Provider: "openai", ID: "gpt-7-sol", Name: "Sol", Reasoning: true},
		{Provider: "cloudflare", ID: "@cf/zai-org/glm-5.3-flash", Name: "GLM 5.3 Flash", Reasoning: true},
	}
	out, err := normalizeCustomModels(good)
	if err != nil {
		t.Fatal(err)
	}
	if out[1].Provider != "" || out[1].provider() != providerOpenRouter {
		t.Errorf("openrouter is the same as no provider: %+v", out[1])
	}
	if m := out[3].model(); m.Provider != providerOpenAI || !m.Reasoning || !m.Custom {
		t.Errorf("an OpenAI model that thinks first: %+v", m)
	}
	if m := out[2].model(); m.Provider != providerOpenAI || m.Reasoning {
		t.Errorf("an OpenAI model that does not: %+v", m)
	}
	if m := out[4].model(); m.Provider != providerCloudflare || m.Reasoning {
		t.Errorf("only an OpenAI model takes an effort: %+v", m)
	}
	if m := out[1].model(); len(m.Hosts) != 1 {
		t.Errorf("hosts of an OpenRouter model: %+v", m)
	}

	for name, bad := range map[string]CustomModel{
		"a cloudflare id with no @cf/":   {Provider: "cloudflare", ID: "qwen/qwen3-30b-a3b-fp8"},
		"an openrouter id for openai":    {Provider: "openai", ID: "openai/gpt-4o"},
		"an @cf/ id for openrouter":      {ID: "@cf/qwen/qwen3-30b-a3b-fp8"},
		"hosts on a cloudflare model":    {Provider: "cloudflare", ID: "@cf/qwen/qwen3-30b-a3b-fp8", Hosts: []string{"nvidia"}},
		"a provider that does not exist": {Provider: "acme", ID: "acme/model"},
		"a built-in model":               {Provider: "cloudflare", ID: modelCFGemma},
	} {
		if _, err := normalizeCustomModels([]CustomModel{bad}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAnAddedCloudflareModelGoesToCloudflare(t *testing.T) {
	cfEnv(t)
	s := GetSiteSettings()
	s.Genie.CustomModels = []CustomModel{{Provider: "cloudflare", ID: "@cf/zai-org/glm-5.3-flash", Name: "GLM 5.3 Flash", Enabled: true, Free: false}}
	if err := SaveSiteSettings(s); err != nil {
		t.Fatal(err)
	}
	pub := GetSiteSettings().public().CustomModels
	if len(pub) != 1 || pub[0].Provider != providerCloudflare || pub[0].Free {
		t.Fatalf("the page is told: %+v", pub)
	}
	url, last, lastBody := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	old := cloudflareAPI
	cloudflareAPI = url
	t.Cleanup(func() { cloudflareAPI = old })
	w := proxyCall(t, `{"model":"@cf/zai-org/glm-5.3-flash","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || last.Header.Get("Authorization") != "Bearer cf-test-token" || !strings.Contains(*lastBody, "@cf/zai-org/glm-5.3-flash") {
		t.Fatalf("%d %s / %v / %s", w.Code, w.Body.String(), last.Header, *lastBody)
	}
}

func TestTheCloudflareTokenAndAccountCanBeKeptInTheDashboard(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	t.Setenv("OPENREPL_CLOUDFLARE_API_TOKEN", "")
	t.Setenv("OPENREPL_CLOUDFLARE_ACCOUNT_ID", "")
	t.Cleanup(func() { utils.SetCloudflareOverrides("", "") })

	url, _, _ := fakeUpstream(t, 200, `{"success":true}`)
	old := cloudflareAPI
	cloudflareAPI = url
	t.Cleanup(func() { cloudflareAPI = old })

	if w := postKey(h, "cloudflare-account", "0123456789abcdef0123456789abcdef"); w.Code != 200 {
		t.Fatalf("account: %d %s", w.Code, w.Body.String())
	}
	if w := postKey(h, "cloudflare", "cf-dash-token-0123456789"); w.Code != 200 {
		t.Fatalf("token: %d %s", w.Code, w.Body.String())
	}
	if !utils.CloudflareEnabled() || utils.CloudflareToken() != "cf-dash-token-0123456789" || utils.CloudflareAccountID() != "0123456789abcdef0123456789abcdef" ||
		utils.KeySource("cloudflare") != "dashboard" || utils.KeySource("cloudflare-account") != "dashboard" {
		t.Fatalf("in use: %q %q", utils.CloudflareToken(), utils.CloudflareAccountID())
	}
	for _, path := range []string{"/admin/keys", "/admin/settings", "/admin/health", "/admin/audit"} {
		body := get(h, path).Body.String()
		if strings.Contains(body, "cf-dash-token-0123456789") || strings.Contains(body, GetSiteSettings().Secrets.Cloudflare) {
			t.Errorf("%s shows the token or its ciphertext", path)
		}
	}
	if w := postKey(h, "cloudflare", ""); w.Code != 200 || utils.CloudflareToken() != "" {
		t.Fatalf("removing: %d %q", w.Code, utils.CloudflareToken())
	}
}

func TestTheDashboardListsTheCloudflareModelsWithTheirPrices(t *testing.T) {
	_, h := adminTestServer(t)
	cfEnv(t)
	t.Cleanup(func() { utils.SetCloudflareOverrides("", "") })

	// not set up: nothing is asked of Cloudflare
	t.Setenv("OPENREPL_CLOUDFLARE_API_TOKEN", "")
	var r cloudflareModelsReply
	decode(t, get(h, "/admin/cloudflare-models"), &r)
	if r.Configured || len(r.Models) != 0 {
		t.Fatalf("before the setup: %+v", r)
	}

	t.Setenv("OPENREPL_CLOUDFLARE_API_TOKEN", "cf-test-token")
	url, last, _ := fakeUpstream(t, 200, `{"success":true,"result":[
	  {"name":"@cf/qwen/qwen3-30b-a3b-fp8","properties":[{"property_id":"price","value":[{"unit":"per M input tokens","price":0.0509},{"unit":"per M output tokens","price":0.335}]}]},
	  {"name":"@cf/google/gemma-2b-it-lora","properties":[]},
	  {"name":"not-a-cloudflare-id","properties":[{"property_id":"price","value":[{"unit":"per M input tokens","price":1}]}]},
	  {"name":"@cf/google/gemma-4-26b-a4b-it","properties":[{"property_id":"price","value":[{"unit":"per M input tokens","price":0.1},{"unit":"per M output tokens","price":0.3}]}]}]}`)
	old := cloudflareAPI
	cloudflareAPI = url
	t.Cleanup(func() { cloudflareAPI = old })
	decode(t, get(h, "/admin/cloudflare-models"), &r)
	if !r.Configured || len(r.Models) != 2 || r.Models[0].ID != "@cf/google/gemma-4-26b-a4b-it" || r.Models[0].OutputPerM != 0.3 || r.Models[1].InputPerM != 0.0509 {
		t.Fatalf("models: %+v", r)
	}
	if last.Header.Get("Authorization") != "Bearer cf-test-token" {
		t.Errorf("Cloudflare was asked with %q", last.Header.Get("Authorization"))
	}
	if body := get(h, "/admin/cloudflare-models").Body.String(); strings.Contains(body, "cf-test-token") {
		t.Errorf("the token is in the reply: %s", body)
	}
}

func TestModelsCanStartInTheShortList(t *testing.T) {
	isolateSettings(t)
	s := GetSiteSettings()
	s.Genie.FeaturedModels = []string{modelCFGemma, modelMini, modelCFGemma, " "}
	n, err := s.normalize()
	if err != nil || len(n.Genie.FeaturedModels) != 2 || n.Genie.FeaturedModels[0] != modelCFGemma {
		t.Fatalf("%v %+v", err, n.Genie.FeaturedModels)
	}
	s.Genie.FeaturedModels = []string{"not-a-model"}
	if _, err := s.normalize(); err == nil {
		t.Error("a model that does not exist was accepted")
	}
	s.Genie.FeaturedModels = chatModelOrder
	s.Genie.FeaturedModels = append(append([]string{}, chatModelOrder...), chatModelOrder...)
	if n, err := s.normalize(); err != nil || len(n.Genie.FeaturedModels) != len(chatModelOrder) {
		t.Errorf("duplicates: %v %+v", err, n.Genie.FeaturedModels)
	}
	s.Genie.CustomModels = []CustomModel{{Provider: "openai", ID: "gpt-4.1-mini", Name: "4.1 mini", Enabled: true}, {Provider: "cloudflare", ID: "@cf/a/b", Name: "x", Enabled: true},
		{ID: "a/b", Name: "y", Enabled: true}, {ID: "c/d", Name: "z", Enabled: true}}
	s.Genie.FeaturedModels = append(append([]string{}, chatModelOrder...), "gpt-4.1-mini", "@cf/a/b", "a/b")
	if _, err := s.normalize(); err == nil {
		t.Error("more models than the short list holds were accepted")
	}
	s.Genie.FeaturedModels = []string{modelMini}
	if pub := func() publicSettings { s.Genie.DisabledModels = nil; return s.public() }(); len(pub.FeaturedModels) != 1 {
		t.Errorf("the page is told: %+v", pub.FeaturedModels)
	}
}
