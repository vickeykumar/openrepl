package persist

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRTDBTokensAreMintedWithTheDatabaseScopeAndKept(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	var asked int32
	var scope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&asked, 1)
		r.ParseForm()
		parts := strings.Split(r.Form.Get("assertion"), ".")
		if len(parts) == 3 {
			raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var c map[string]interface{}
			json.Unmarshal(raw, &c)
			scope, _ = c["scope"].(string)
		}
		w.Write([]byte(`{"access_token":"tok-1","expires_in":3600}`))
	}))
	defer srv.Close()
	acct, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "demo", "client_email": "svc@demo.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": srv.URL,
	})
	tokens, err := NewRTDBTokens(string(acct))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.ProjectID() != "demo" {
		t.Errorf("project %q", tokens.ProjectID())
	}
	for i := 0; i < 3; i++ {
		tok, err := tokens.Token()
		if err != nil || tok != "tok-1" {
			t.Fatalf("%q %v", tok, err)
		}
	}
	if asked != 1 {
		t.Errorf("Google was asked %d times, want 1 (the token is kept)", asked)
	}
	if !strings.Contains(scope, "firebase.database") {
		t.Errorf("scope %q", scope)
	}
	if _, err := NewRTDBTokens("not a key"); err == nil {
		t.Error("a bad key was accepted")
	}
}
