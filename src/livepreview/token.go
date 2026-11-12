// Package livepreview serves the Preview pane (docs/preview-pane-design.md): the pages and programs of a workspace, shown
// in a sandboxed frame.
//
// The page in the frame has no origin of its own (sandbox without allow-same-origin), so the browser sends none of
// OpenREPL's cookies with its requests. What says "this is that visitor's workspace" is a token in the address: the
// editor page, which is signed in or is a guest with a workspace, asks the server for one and the pane loads
// /preview/<token>/... A token is the workspace and an end time, signed with the server's secret; it cannot be made
// for another workspace and it ends by itself.
package livepreview

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// TokenLife is how long a token works. The pane asks for a new one before it ends.
const TokenLife = 2 * time.Hour

type claims struct {
	Home string `json:"h"`
	End  int64  `json:"e"`
}

func mac(secret, payload string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("openrepl-preview\x00" + payload))
	return hex.EncodeToString(m.Sum(nil))
}

// NewToken makes a token for the workspace home, valid until now+life. secret is the server's secret.
func NewToken(secret, home string, now time.Time, life time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("the server has no secret to sign a preview with")
	}
	if home == "" {
		return "", errors.New("no workspace")
	}
	raw, _ := json.Marshal(claims{Home: home, End: now.Add(life).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + mac(secret, payload), nil
}

// Home reads a token: the workspace it names, when it is signed with secret and has not ended.
func Home(secret, token string, now time.Time) (home string, ok bool) {
	if secret == "" {
		return "", false
	}
	i := strings.IndexByte(token, '.')
	if i <= 0 {
		return "", false
	}
	payload, sig := token[:i], token[i+1:]
	if !hmac.Equal([]byte(mac(secret, payload)), []byte(sig)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	var c claims
	if json.Unmarshal(raw, &c) != nil || c.Home == "" || now.Unix() >= c.End {
		return "", false
	}
	return c.Home, true
}
