package server

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"utils"
)

const (
	testSecret = "a-long-random-server-secret-for-tests"
	dashKey    = "sk-dashboard-key-0123456789"
)

func TestKeysAreEncryptedAndBoundToTheirProvider(t *testing.T) {
	sealed, err := sealKey(testSecret, keyOpenAI, dashKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, dashKey) || strings.Contains(sealed, "dashboard") {
		t.Fatalf("the ciphertext shows the key: %s", sealed)
	}
	if got, err := openKey(testSecret, keyOpenAI, sealed); err != nil || got != dashKey {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := openKey("another-secret", keyOpenAI, sealed); err == nil {
		t.Error("opened with another secret")
	}
	if _, err := openKey(testSecret, keyOpenRouter, sealed); err == nil {
		t.Error("an OpenAI key was opened as an OpenRouter key")
	}
	if _, err := openKey(testSecret, keyOpenAI, sealed[:len(sealed)-3]); err == nil {
		t.Error("opened damaged data")
	}
	if _, err := sealKey("", keyOpenAI, dashKey); err != errNoSettingsSecret {
		t.Errorf("sealed without a secret: %v", err)
	}
	other, _ := sealKey(testSecret, keyOpenAI, dashKey)
	if other == sealed {
		t.Error("two seals of the same key are identical (the nonce is not random)")
	}
}

// provider stands in for OpenAI's and OpenRouter's key checks.
func keyProvider(t *testing.T, status int) (calls *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	oa, or := openAIKeyCheckURL, openRouterKeyCheckURL
	openAIKeyCheckURL, openRouterKeyCheckURL = srv.URL, srv.URL
	t.Cleanup(func() { openAIKeyCheckURL, openRouterKeyCheckURL = oa, or })
	return &seen
}

func keysEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENREPL_SECRET", testSecret)
	t.Setenv("OPENREPL_OPENAI_API_KEY", "sk-from-env")
	t.Setenv("OPENREPL_OPENROUTER_API_KEY", "")
}

func postKey(h http.Handler, provider, key string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]string{"provider": provider, "key": key})
	return post(h, "/admin/keys", string(b))
}

func TestADashboardKeyWinsOverTheEnvironmentAndNeverComesBack(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	calls := keyProvider(t, 200)

	w := postKey(h, "openai", " "+dashKey+" ")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "Bearer "+dashKey {
		t.Fatalf("the provider was asked with %v", *calls)
	}
	if utils.OpenAIKey() != dashKey || utils.KeySource("openai") != "dashboard" {
		t.Fatalf("in use: %q from %q", utils.OpenAIKey(), utils.KeySource("openai"))
	}

	// nothing the dashboard can read holds the key or its ciphertext
	stored := GetSiteSettings().Secrets
	if stored == nil || stored.OpenAI == "" {
		t.Fatal("nothing was stored")
	}
	for _, path := range []string{"/admin/keys", "/admin/settings", "/admin/gateway", "/admin/health", "/admin/audit"} {
		body := get(h, path).Body.String()
		if strings.Contains(body, dashKey) || strings.Contains(body, stored.OpenAI) || strings.Contains(body, "sk-from-env") {
			t.Errorf("%s shows a key or its ciphertext: %.200s", path, body)
		}
	}
	if strings.Contains(w.Body.String(), dashKey) || strings.Contains(w.Body.String(), stored.OpenAI) {
		t.Errorf("the reply to the save shows the key: %s", w.Body.String())
	}
	var r keysReply
	decode(t, get(h, "/admin/keys"), &r)
	if !r.CanStore || r.Keys[0].Source != "dashboard" || !r.Keys[0].Set || !r.Keys[0].Stored {
		t.Fatalf("status: %+v", r)
	}
	if !strings.Contains(get(h, "/admin/audit").Body.String(), "OpenAI key replaced") {
		t.Error("the change is not in the audit log")
	}

	// the file holds the ciphertext and not the key
	data, err := ioutil.ReadFile(SETTINGS_FILE)
	if err != nil || strings.Contains(string(data), dashKey) || !strings.Contains(string(data), stored.OpenAI) {
		t.Fatalf("settings.json: %v %s", err, data)
	}
}

func TestTheSettingsFormCannotTouchTheKeys(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	keyProvider(t, 200)
	postKey(h, "openai", dashKey)
	before := *GetSiteSettings().Secrets

	// saving the form leaves the keys alone, and a form that names keys is ignored
	if w := post(h, "/admin/settings", `{"colorOfTheDay":true,"secrets":{"openai":"AAAA","openrouter":"BBBB"}}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	after := GetSiteSettings()
	if after.Secrets == nil || *after.Secrets != before || !after.ColorOfTheDay {
		t.Fatalf("keys after a form save: %+v (before %+v)", after.Secrets, before)
	}
	if utils.OpenAIKey() != dashKey {
		t.Fatalf("the key in use changed: %q", utils.OpenAIKey())
	}
}

func TestKeysNeedTheSecret(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	t.Setenv("OPENREPL_SECRET", "")
	keyProvider(t, 200)
	w := postKey(h, "openai", dashKey)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "OPENREPL_SECRET") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var r keysReply
	decode(t, get(h, "/admin/keys"), &r)
	if r.CanStore || r.Keys[0].Source != "env" {
		t.Fatalf("status: %+v", r)
	}
}

func TestBadKeysAreRefused(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	calls := keyProvider(t, 200)
	for name, key := range map[string]string{
		"too short":      "sk-1",
		"has a space":    "sk-abc def-0123456789",
		"has a newline":  "sk-abc\ndef-0123456789",
		"not ascii":      "sk-ключ-0123456789",
		"far too long":   strings.Repeat("a", 600),
		"nothing but ws": "   ",
	} {
		w := postKey(h, "openai", key)
		// "   " is an empty key, which removes: nothing is stored either way
		if name != "nothing but ws" && w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("the provider was asked about keys that cannot be keys: %v", *calls)
	}
	if w := postKey(h, "bing", dashKey); w.Code != 400 {
		t.Errorf("unknown provider: %d", w.Code)
	}
	if GetSiteSettings().Secrets != nil || utils.OpenAIKey() != "sk-from-env" {
		t.Fatal("a refused key changed something")
	}
}

func TestAKeyTheProviderRefusesIsNotSaved(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	keyProvider(t, 401)
	if w := postKey(h, "openai", dashKey); w.Code != 400 || !strings.Contains(w.Body.String(), "refused") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if GetSiteSettings().Secrets != nil || utils.OpenAIKey() != "sk-from-env" {
		t.Fatal("a refused key was saved")
	}
}

func TestAKeyThatCannotBeCheckedIsSavedWithAWarning(t *testing.T) {
	for status, want := range map[int]string{403: "refused this check", 500: "answered 500"} {
		_, h := adminTestServer(t)
		keysEnv(t)
		keyProvider(t, status)
		w := postKey(h, "openai", dashKey)
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("status %d: %d %s", status, w.Code, w.Body.String())
		}
		if utils.OpenAIKey() != dashKey {
			t.Fatalf("status %d: not saved", status)
		}
	}
}

func TestRemovingAKeyFallsBackToTheEnvironment(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	keyProvider(t, 200)
	postKey(h, "openai", dashKey)
	postKey(h, "openrouter", "sk-or-dashboard-0123")
	if utils.OpenRouterKey() != "sk-or-dashboard-0123" {
		t.Fatalf("openrouter %q", utils.OpenRouterKey())
	}
	if w := postKey(h, "openai", ""); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if utils.OpenAIKey() != "sk-from-env" || utils.KeySource("openai") != "env" {
		t.Fatalf("after removing: %q from %q", utils.OpenAIKey(), utils.KeySource("openai"))
	}
	if utils.OpenRouterKey() != "sk-or-dashboard-0123" {
		t.Fatal("removing one key removed the other")
	}
	postKey(h, "openrouter", "")
	if GetSiteSettings().Secrets != nil {
		t.Fatalf("empty secrets are kept: %+v", GetSiteSettings().Secrets)
	}
	if !strings.Contains(get(h, "/admin/audit").Body.String(), "OpenAI key removed") {
		t.Error("the removal is not in the audit log")
	}
}

func TestAChangedSecretMakesStoredKeysUnreadableAndSaysSo(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	keyProvider(t, 200)
	postKey(h, "openai", dashKey)

	// the server restarts with another secret
	t.Setenv("OPENREPL_SECRET", "a-different-secret")
	applyKeyOverrides(GetSiteSettings().Secrets)
	if utils.OpenAIKey() != "sk-from-env" {
		t.Fatalf("an unreadable key was used: %q", utils.OpenAIKey())
	}
	health := get(h, "/admin/health").Body.String()
	if !strings.Contains(health, "Dashboard keys") || !strings.Contains(health, "cannot be decrypted") {
		t.Errorf("health does not say so: %.400s", health)
	}
	var r keysReply
	decode(t, get(h, "/admin/keys"), &r)
	if !r.Keys[0].Stored || r.Keys[0].Problem == "" {
		t.Fatalf("status: %+v", r.Keys[0])
	}
	// entering the key again fixes it
	if w := postKey(h, "openai", dashKey); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if utils.OpenAIKey() != dashKey {
		t.Fatal("the new key is not in use")
	}
}

func TestTheProxyCallsTheProviderWithTheDashboardKey(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	keyProvider(t, 200)
	postKey(h, "openai", dashKey)
	url, last, _ := fakeUpstream(t, 200, `{"choices":[{"message":{"content":"ok"}}]}`)
	old := openaiEndpoint
	openaiEndpoint = url
	t.Cleanup(func() { openaiEndpoint = old })
	if w := proxyCall(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if last.Header.Get("Authorization") != "Bearer "+dashKey {
		t.Fatalf("OpenAI was called with %q", last.Header.Get("Authorization"))
	}
}

func TestAnotherInstanceTakesTheKeysFromTheStore(t *testing.T) {
	keysEnv(t)
	f := &fakeStore{}
	withStore(t, f)
	sealed, _ := sealKey(testSecret, keyOpenRouter, "sk-or-shared-0123456789")
	f.put(t, SiteSettings{Secrets: &StoredKeys{OpenRouter: sealed}})
	syncSettingsOnce()
	if utils.OpenRouterKey() != "sk-or-shared-0123456789" || utils.KeySource("openrouter") != "dashboard" {
		t.Fatalf("openrouter: %q from %q", utils.OpenRouterKey(), utils.KeySource("openrouter"))
	}
	// and the other instance removes it
	f.put(t, SiteSettings{})
	syncSettingsOnce()
	if utils.OpenRouterKey() != "" {
		t.Fatalf("a removed key is still in use: %q", utils.OpenRouterKey())
	}
}

func TestGatewayParametersAndSettingsNeverHoldKeys(t *testing.T) {
	_, h := adminTestServer(t)
	keysEnv(t)
	keyProvider(t, 200)
	postKey(h, "openai", dashKey)
	var r settingsReply
	decode(t, get(h, "/admin/settings"), &r)
	if r.Secrets != nil {
		t.Fatalf("secrets in the settings reply: %+v", r.Secrets)
	}
	if strings.Contains(get(h, "/settings.js").Body.String(), "secrets") {
		t.Error("secrets in the public settings")
	}
}
