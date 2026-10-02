package utils

import "sync"

// How many Genie (AI chat) requests a visitor may make per minute. GUEST_FACTOR
// and USER_FACTOR are the defaults; an admin can change them at /admin, and
// SetGenieRates applies the change at once.
var (
	genieMu    sync.RWMutex
	genieGuest = float64(GUEST_FACTOR)
	genieUser  = float64(USER_FACTOR)
)

// SetGenieRates sets the requests per minute for guests and for signed-in
// users. A value that is not positive restores that rate's default.
func SetGenieRates(guest, user float64) {
	if guest <= 0 {
		guest = GUEST_FACTOR
	}
	if user <= 0 {
		user = USER_FACTOR
	}
	genieMu.Lock()
	genieGuest, genieUser = guest, user
	genieMu.Unlock()
}

// GuestFactor is the requests per minute a guest gets.
func GuestFactor() float64 {
	genieMu.RLock()
	defer genieMu.RUnlock()
	return genieGuest
}

// UserFactor is the requests per minute a signed-in user gets.
func UserFactor() float64 {
	genieMu.RLock()
	defer genieMu.RUnlock()
	return genieUser
}
