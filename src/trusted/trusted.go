// Package trusted defines the identity headers a gateway adds to requests it
// forwards to a worker. A worker may believe them only on requests that
// arrived over the authenticated tunnel; the gateway removes any copy a
// client sent. See docs/lld/11-distributed-execution.md.
package trusted

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
)

const (
	// Prefix is shared by every trusted header.
	Prefix = "X-Openrepl-"

	HeaderUID     = "X-Openrepl-Uid"
	HeaderGuest   = "X-Openrepl-Guest"
	HeaderHomeID  = "X-Openrepl-Home-Id"
	HeaderPriv    = "X-Openrepl-Priv"
	HeaderSession = "X-Openrepl-Session"
)

// Identity is who a forwarded request belongs to, as decided by the gateway.
type Identity struct {
	UID       string // empty for a guest
	Guest     string // guest id, set when UID is empty
	HomeID    string // home directory name of a signed-in user
	Privilege string // the user-mode value the server passes to the REPL
	Session   string // execution-context key, for logs
}

// Strip removes every trusted header from h.
func Strip(h http.Header) {
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), Prefix) {
			delete(h, name)
		}
	}
}

// Set replaces the trusted headers in h with id.
func Set(h http.Header, id Identity) {
	Strip(h)
	set := func(name, value string) {
		if value != "" {
			h.Set(name, value)
		}
	}
	set(HeaderUID, id.UID)
	set(HeaderGuest, id.Guest)
	set(HeaderHomeID, id.HomeID)
	set(HeaderPriv, id.Privilege)
	set(HeaderSession, id.Session)
}

// FromHeader reads the identity a gateway put in h.
func FromHeader(h http.Header) Identity {
	return Identity{
		UID:       h.Get(HeaderUID),
		Guest:     h.Get(HeaderGuest),
		HomeID:    h.Get(HeaderHomeID),
		Privilege: h.Get(HeaderPriv),
		Session:   h.Get(HeaderSession),
	}
}

// ErrNoWorkspace means the identity names neither a user home nor a guest.
var ErrNoWorkspace = errors.New("trusted: identity has no home id or guest id")

// HomeDir returns the workspace directory for id under base. A signed-in
// user gets base/<home id>, the same name the gateway would use, and a guest
// gets base/guest-<guest id>, so every request of one guest resolves to the
// same directory without a shared cookie.
func HomeDir(base string, id Identity) (string, error) {
	name := ""
	switch {
	case id.UID != "" && id.HomeID != "":
		name = id.HomeID
	case id.UID == "" && id.Guest != "":
		name = "guest-" + id.Guest
	default:
		return "", ErrNoWorkspace
	}
	if !validName(name) {
		return "", errors.New("trusted: invalid workspace name")
	}
	return filepath.Join(base, name), nil
}

// validName rejects anything that could leave the base directory.
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00")
}
