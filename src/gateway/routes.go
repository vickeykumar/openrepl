package gateway

import "sync"

// RouteMap records keys that belong to one backend no matter which session
// sends them: a fork link's jid names a process, and a shared session's
// homedir names a directory, on one particular machine.
type RouteMap struct {
	mu sync.RWMutex
	m  map[string]string // kind + "\x00" + key -> backend id
}

func NewRouteMap() *RouteMap {
	return &RouteMap{m: make(map[string]string)}
}

func routeKey(kind, key string) string { return kind + "\x00" + key }

// Set records that the backend owns the key.
func (rm *RouteMap) Set(kind, key, backendID string) {
	rm.mu.Lock()
	rm.m[routeKey(kind, key)] = backendID
	rm.mu.Unlock()
}

// Delete removes the key if the backend still owns it.
func (rm *RouteMap) Delete(kind, key, backendID string) {
	rm.mu.Lock()
	if rm.m[routeKey(kind, key)] == backendID {
		delete(rm.m, routeKey(kind, key))
	}
	rm.mu.Unlock()
}

// DropBackend removes every key the backend owns.
func (rm *RouteMap) DropBackend(backendID string) {
	rm.mu.Lock()
	for k, b := range rm.m {
		if b == backendID {
			delete(rm.m, k)
		}
	}
	rm.mu.Unlock()
}

// Lookup returns the backend that owns the key.
func (rm *RouteMap) Lookup(kind, key string) (string, bool) {
	rm.mu.RLock()
	b, ok := rm.m[routeKey(kind, key)]
	rm.mu.RUnlock()
	return b, ok
}

// Len returns the number of recorded keys.
func (rm *RouteMap) Len() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.m)
}
