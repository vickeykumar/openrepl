package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// What an admin can do to a running session: end its terminals, or move it to
// another node.

// trackTerminal records an open terminal of a session and returns the function
// that forgets it. cancel ends the terminal.
func (rt *Router) trackTerminal(key string, cancel context.CancelFunc) func() {
	rt.tmu.Lock()
	rt.termSeq++
	id := rt.termSeq
	if rt.terms[key] == nil {
		rt.terms[key] = make(map[uint64]context.CancelFunc)
	}
	rt.terms[key][id] = cancel
	rt.tmu.Unlock()
	return func() {
		rt.tmu.Lock()
		delete(rt.terms[key], id)
		if len(rt.terms[key]) == 0 {
			delete(rt.terms, key)
		}
		rt.tmu.Unlock()
		cancel()
	}
}

// terminalsOf is how many terminals a session has open.
func (rt *Router) terminalsOf(key string) int {
	rt.tmu.Lock()
	defer rt.tmu.Unlock()
	return len(rt.terms[key])
}

// endTerminals ends every terminal of a session and returns how many there were.
func (rt *Router) endTerminals(key string) int {
	rt.tmu.Lock()
	cancels := make([]context.CancelFunc, 0, len(rt.terms[key]))
	for _, c := range rt.terms[key] {
		cancels = append(cancels, c)
	}
	rt.tmu.Unlock()
	for _, c := range cancels {
		c()
	}
	return len(cancels)
}

// awayFor is how long until the sessions of a backend that is not reachable
// are placed again; 0 while the backend is connected.
func (rt *Router) awayFor(id string) time.Duration {
	if b, ok := rt.Backend(id); ok && b.State() != Offline {
		return 0
	}
	return rt.retryIn(id)
}

// ErrNoSession means no live session has the key.
var ErrNoSession = errors.New("no such session")

// EndSession ends a session: its terminals are closed and its context is
// forgotten, so the visitor's next request places it again. Files are not
// touched. It returns how many terminals were closed.
func (rt *Router) EndSession(key string) (int, error) {
	if _, ok := rt.registry.Resolve(key); !ok {
		return 0, ErrNoSession
	}
	n := rt.endTerminals(key)
	rt.registry.Release(key)
	return n, nil
}

// MoveSession places a session on another node: its terminals are closed, and
// its next request goes to the node, which is first sent the gateway's copy of
// the home. The old node's copy is dropped. It needs workspace sync, which is
// what keeps that copy.
func (rt *Router) MoveSession(key, to string) (int, error) {
	if rt.homeOf == nil {
		return 0, errors.New("moving a session needs workspace sync")
	}
	ec, ok := rt.registry.Resolve(key)
	if !ok {
		return 0, ErrNoSession
	}
	if ec.Home == "" {
		return 0, errors.New("the session has no home to move")
	}
	if to == ec.BackendID {
		return 0, errors.New("the session is already on that node")
	}
	b, found := rt.Backend(to)
	if !found {
		return 0, fmt.Errorf("no node %q is connected", to)
	}
	if b.State() != Online {
		return 0, fmt.Errorf("node %q is %s and takes no sessions", to, strings.ToLower(b.State().String()))
	}
	n := rt.endTerminals(key)
	rt.registry.Release(key)
	moved := rt.registry.Create(Identity{Key: ec.Key, UID: ec.UID, GuestID: ec.GuestID, Home: ec.Home}, to)
	if moved.BackendID != to {
		return n, errors.New("the visitor's session was placed again at the same moment; try once more")
	}
	if ec.UID != "" && rt.pins != nil {
		rt.pins.Set(ec.UID, to)
	}
	if rt.onMoved != nil {
		rt.onMoved(ec.Home, ec.BackendID, to)
	}
	log.Printf("gateway: session %s moved from %s to %s by an admin", key, ec.BackendID, to)
	return n, nil
}
