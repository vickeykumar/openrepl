package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/url"
)

// HomeSigKey is the query key that carries the signature of a shared workspace.
const HomeSigKey = "hsig"

// SignHome signs the path of a workspace with the server's secret. The master's file browser gets it with
// the root of its tree, and the collaboration link carries it to the collaborator, whose requests name the
// master's workspace with ?homedir=<path>&hsig=<signature>. "" when the server has no secret.
func SignHome(homedir string) string {
	secret := Secret()
	if secret == "" || homedir == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("openrepl-shared-home\x00" + homedir))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidHomeSig reports whether sig is the signature of homedir.
func ValidHomeSig(homedir, sig string) bool {
	want := SignHome(homedir)
	return want != "" && hmac.Equal([]byte(want), []byte(sig))
}

// SharedHome is the workspace a request names with ?homedir=, when its signature is valid. A request that
// names a workspace without a valid signature is told apart from one that names none (named is true), so
// the caller can log it, and it falls back to the caller's own workspace.
func SharedHome(form url.Values) (homedir string, named, ok bool) {
	if !form.Has(HOME_DIR_KEY) {
		return "", false, false
	}
	homedir = form.Get(HOME_DIR_KEY)
	if ValidHomeSig(homedir, form.Get(HomeSigKey)) {
		return homedir, true, true
	}
	log.Println("WARN: ignoring a workspace named without a valid signature: ", homedir)
	return "", true, false
}
