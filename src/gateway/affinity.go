package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gorilla/securecookie"
)

// AffinityCookie carries a guest's random id. It is signed so that a client
// cannot pick another guest's id.
const AffinityCookie = "or-aff"

// Affinity issues and reads the guest-id cookie.
type Affinity struct {
	sc     *securecookie.SecureCookie
	maxAge int
}

// NewAffinity signs the cookie with secret. maxAge is the cookie lifetime.
func NewAffinity(secret []byte, maxAge time.Duration) *Affinity {
	return &Affinity{
		sc:     securecookie.New(secret, nil),
		maxAge: int(maxAge / time.Second),
	}
}

// GuestID returns the guest id in the request cookie, or "" if there is none
// or its signature is invalid.
func (a *Affinity) GuestID(r *http.Request) string {
	c, err := r.Cookie(AffinityCookie)
	if err != nil {
		return ""
	}
	var id string
	if err := a.sc.Decode(AffinityCookie, c.Value, &id); err != nil {
		return ""
	}
	return id
}

// guestIDBytes is the random part of a guest id: 5 bytes, 10 hex characters.
// The id also names the guest's workspace folder on a worker
// (guest-<id>), which the user sees in the file browser, so it is kept about
// as short as the folder name a single server creates. It does not need to be
// secret: a client cannot use an id without the cookie's signature.
const guestIDBytes = 5

// NewGuestID returns a random guest id.
func NewGuestID() (string, error) {
	buf := make([]byte, guestIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Set stores the guest id in a signed cookie on the response.
func (a *Affinity) Set(w http.ResponseWriter, id string) error {
	enc, err := a.sc.Encode(AffinityCookie, id)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     AffinityCookie,
		Value:    enc,
		Path:     "/",
		MaxAge:   a.maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Issue creates a new guest id and sets its cookie on the response.
func (a *Affinity) Issue(w http.ResponseWriter) (string, error) {
	id, err := NewGuestID()
	if err != nil {
		return "", err
	}
	return id, a.Set(w, id)
}
