package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"

	"utils"
)

// API keys an admin saves in the dashboard (OpenAI, OpenRouter).
//
// They are kept in the settings document (settings.json, or MongoDB) as
// ciphertext, AES-256-GCM under a key derived from OPENREPL_SECRET,
// which is never stored with them. Each instance decrypts them into memory
// when it reads the settings (applyKeyOverrides) and utils.OpenAIKey and
// utils.OpenRouterKey then prefer them to the environment's. They are write
// only: no API reply carries a key, a ciphertext or a part of one, and the
// audit log says only that one was replaced or removed.
//
// Without the secret the dashboard cannot keep keys and the environment's keys
// are used, as before. If the secret later changes, the stored keys cannot be
// read: they are ignored, the health page says so, and the environment's keys
// are used until the keys are entered again.

// StoredKeys is the encrypted part of the settings document.
type StoredKeys struct {
	OpenAI     string `json:"openai,omitempty"`
	OpenRouter string `json:"openrouter,omitempty"`
}

const (
	keyOpenAI     = "openai"
	keyOpenRouter = "openrouter"

	minKeyLen = 8
	maxKeyLen = 512
)

var errNoSettingsSecret = errors.New("OPENREPL_SECRET is not set on the server, so keys cannot be kept here")

// deriveKeyBox is the AES-256 key for the secret. The secret is a long random
// token (not a password a person types), so a keyed hash with a fixed label is
// enough.
func deriveKeyBox(secret string) []byte {
	sum := sha256.Sum256([]byte("openrepl/settings-keys/v1\x00" + secret))
	return sum[:]
}

// sealKey encrypts a key for the given provider. The provider is bound into the
// ciphertext, so one field cannot be moved into the other.
func sealKey(secret, provider, key string) (string, error) {
	if secret == "" {
		return "", errNoSettingsSecret
	}
	block, err := aes.NewCipher(deriveKeyBox(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(key), []byte(provider))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openKey decrypts what sealKey made. It fails for another secret, another
// provider or damaged data.
func openKey(secret, provider, sealed string) (string, error) {
	if secret == "" {
		return "", errNoSettingsSecret
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKeyBox(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("the stored key is too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(provider))
	if err != nil {
		return "", errors.New("the stored key cannot be decrypted with this OPENREPL_SECRET")
	}
	return string(plain), nil
}

// keyProblem is what the stored keys of the settings now in use cannot do:
// "" when all is well.
var keysProblem string

// applyKeyOverrides decrypts the stored keys of s and hands them to utils. A
// key that cannot be read is skipped, the problem is remembered for the health
// page, and the environment's key stays in use.
func applyKeyOverrides(k *StoredKeys) {
	var openai, openrouter string
	problem := ""
	if k != nil {
		secret := utils.Secret()
		for _, f := range []struct {
			provider, sealed string
			out              *string
		}{{keyOpenAI, k.OpenAI, &openai}, {keyOpenRouter, k.OpenRouter, &openrouter}} {
			if f.sealed == "" {
				continue
			}
			plain, err := openKey(secret, f.provider, f.sealed)
			if err != nil {
				problem = "a key saved in the dashboard (" + f.provider + ") cannot be used: " + err.Error()
				log.Println("settings:", problem)
				continue
			}
			*f.out = plain
		}
	}
	utils.SetKeyOverrides(openai, openrouter)
	settingsMu.Lock()
	keysProblem = problem
	settingsMu.Unlock()
}

// ---- checking a key with its provider -------------------------------------------

var (
	openAIKeyCheckURL     = "https://api.openai.com/v1/models"
	openRouterKeyCheckURL = "https://openrouter.ai/api/v1/key"
)

// checkKey asks the provider whether it accepts the key, with a request that
// costs nothing. Only a 401 means the key is wrong: a restricted key may be
// refused (403) here and still work for chat, and a provider that does not
// answer says nothing about the key. warning is for the admin to read.
func checkKey(provider, key string) (rejected bool, warning string) {
	url := openAIKeyCheckURL
	if provider == keyOpenRouter {
		url = openRouterKeyCheckURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, "The key could not be checked with the provider."
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "The provider could not be reached to check the key. It was saved as it is."
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return true, ""
	case resp.StatusCode == http.StatusForbidden:
		return false, "The provider accepted the key but refused this check. If chat works, all is well."
	case resp.StatusCode >= 300:
		return false, fmt.Sprintf("The provider answered %d to the check. The key was saved as it is.", resp.StatusCode)
	}
	return false, ""
}

// validKeyText rejects what cannot be a key: too short or long, or holding
// spaces or control characters (a pasted line break, usually).
func validKeyText(key string) error {
	if len(key) < minKeyLen || len(key) > maxKeyLen {
		return fmt.Errorf("a key is between %d and %d characters", minKeyLen, maxKeyLen)
	}
	for _, r := range key {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r > unicode.MaxASCII {
			return errors.New("the key holds a space, a line break or a character a key does not have")
		}
	}
	return nil
}

// ---- saving ----------------------------------------------------------------------

// setStoredKey replaces (key != "") or removes (key == "") the dashboard key of
// a provider, leaving everything else in the settings as it is.
func setStoredKey(provider, key string) error {
	secret := utils.Secret()
	cur := GetSiteSettings()
	k := StoredKeys{}
	if cur.Secrets != nil {
		k = *cur.Secrets
	}
	sealed := ""
	if key != "" {
		var err error
		if sealed, err = sealKey(secret, provider, key); err != nil {
			return err
		}
	}
	switch provider {
	case keyOpenAI:
		k.OpenAI = sealed
	case keyOpenRouter:
		k.OpenRouter = sealed
	default:
		return fmt.Errorf("%q is not a provider", provider)
	}
	if k == (StoredKeys{}) {
		cur.Secrets = nil
	} else {
		cur.Secrets = &k
	}
	return SaveSiteSettings(cur, -1)
}

// keyStatus is what the dashboard may know about a key: never the key.
type keyStatus struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Set      bool   `json:"set"`
	Source   string `json:"source"` // dashboard, env, file or ""
	Stored   bool   `json:"stored"` // a key saved in the dashboard exists (it may be unreadable)
	Problem  string `json:"problem,omitempty"`
}

type keysReply struct {
	// CanStore is false without OPENREPL_SECRET.
	CanStore bool        `json:"canStore"`
	Keys     []keyStatus `json:"keys"`
}

func currentKeysReply() keysReply {
	cur := GetSiteSettings()
	settingsMu.Lock()
	problem := keysProblem
	settingsMu.Unlock()
	stored := func(p string) bool {
		if cur.Secrets == nil {
			return false
		}
		return map[string]bool{keyOpenAI: cur.Secrets.OpenAI != "", keyOpenRouter: cur.Secrets.OpenRouter != ""}[p]
	}
	out := keysReply{CanStore: utils.Secret() != ""}
	for _, p := range []struct{ id, name string }{{keyOpenAI, "OpenAI"}, {keyOpenRouter, "OpenRouter"}} {
		st := keyStatus{Provider: p.id, Name: p.name, Source: utils.KeySource(p.id), Stored: stored(p.id)}
		key := utils.OpenAIKey()
		if p.id == keyOpenRouter {
			key = utils.OpenRouterKey()
		}
		st.Set = key != ""
		if st.Stored && st.Source != "dashboard" {
			st.Problem = problem
		}
		out.Keys = append(out.Keys, st)
	}
	return out
}

// handleAdminKeys lists the keys (GET) and replaces or removes one (POST:
// {"provider": "openai", "key": "sk-..."}, an empty key removes it). It runs
// behind adminAPI, like the settings.
func (server *Server) handleAdminKeys(rw http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		adminJSON(rw, http.StatusOK, currentKeysReply())
	case http.MethodPost:
		req.Body = http.MaxBytesReader(rw, req.Body, 8*1024)
		var in struct {
			Provider string `json:"provider"`
			Key      string `json:"key"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			adminError(rw, http.StatusBadRequest, "The request was not valid JSON.")
			return
		}
		if in.Provider != keyOpenAI && in.Provider != keyOpenRouter {
			adminError(rw, http.StatusBadRequest, "Choose OpenAI or OpenRouter.")
			return
		}
		if utils.Secret() == "" {
			adminError(rw, http.StatusConflict, errNoSettingsSecret.Error()+". Set it and restart.")
			return
		}
		key := strings.TrimSpace(in.Key)
		warning := ""
		if key != "" {
			if err := validKeyText(key); err != nil {
				adminError(rw, http.StatusBadRequest, "That is not a key: "+err.Error()+".")
				return
			}
			var rejected bool
			if rejected, warning = checkKey(in.Provider, key); rejected {
				adminError(rw, http.StatusBadRequest, "The provider refused this key. Nothing was changed.")
				return
			}
		}
		if err := setStoredKey(in.Provider, key); err != nil {
			switch err {
			case errSettingsConflict:
				adminError(rw, http.StatusConflict, "Someone else changed the settings just now. Reload and try again.")
			case errSettingsUnavailable:
				adminError(rw, http.StatusServiceUnavailable, "The settings store is not reachable right now, so nothing was saved.")
			default:
				log.Println("saving a key failed: ", err)
				adminError(rw, http.StatusInternalServerError, "Could not save the key. Please try again.")
			}
			return
		}
		name := map[string]string{keyOpenAI: "OpenAI", keyOpenRouter: "OpenRouter"}[in.Provider]
		if key == "" {
			server.audit(req, "settings", name+" key removed from the dashboard")
		} else {
			server.audit(req, "settings", name+" key replaced")
		}
		reply := struct {
			keysReply
			Warning string `json:"warning,omitempty"`
		}{currentKeysReply(), warning}
		adminJSON(rw, http.StatusOK, reply)
	default:
		rw.Header().Set("Allow", "GET, POST")
		adminError(rw, http.StatusMethodNotAllowed, "Use GET or POST.")
	}
}
