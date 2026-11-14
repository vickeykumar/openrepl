package persist

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// OAuth tokens for the Firebase Realtime Database REST API, from the service account key the operator gave (the same kind of key
// as Firestore's, parsed the same way): the server signs a JWT with the key and trades it at Google for a short token. A
// service account is not bound by the database's security rules, which is what lets a job on the server clean up what the
// browsers of the users left behind.

// RTDBTokens gives access tokens for one service account.
type RTDBTokens struct {
	acct   *serviceAccount
	rsaKey *rsa.PrivateKey
	http   *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewRTDBTokens reads the key from raw (the JSON, its base64, or the path of a file).
func NewRTDBTokens(raw string) (*RTDBTokens, error) {
	a, err := parseCredentials(raw)
	if err != nil {
		return nil, err
	}
	key, err := parsePrivateKey(a.PrivateKey)
	if err != nil {
		return nil, err
	}
	return &RTDBTokens{acct: a, rsaKey: key, http: &http.Client{Timeout: 15 * time.Second}}, nil
}

// ProjectID is the Google Cloud project the key belongs to.
func (t *RTDBTokens) ProjectID() string { return t.acct.ProjectID }

// Token returns a token, asking Google for a new one a minute before the old one ends.
func (t *RTDBTokens) Token() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && time.Now().Before(t.expires.Add(-time.Minute)) {
		return t.token, nil
	}
	now := time.Now()
	enc := func(v interface{}) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	unsigned := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(map[string]interface{}{
		"iss": t.acct.ClientEmail, "scope": "https://www.googleapis.com/auth/firebase.database https://www.googleapis.com/auth/userinfo.email",
		"aud": t.acct.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, t.rsaKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)}}
	resp, err := t.http.PostForm(t.acct.TokenURI, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	json.Unmarshal(body, &tok)
	if resp.StatusCode != 200 || tok.AccessToken == "" {
		return "", fmt.Errorf("Google refused the service account (%d %s %s)", resp.StatusCode, tok.Error, tok.Description)
	}
	t.token = tok.AccessToken
	t.expires = now.Add(time.Duration(tok.ExpiresIn) * time.Second)
	return t.token, nil
}
