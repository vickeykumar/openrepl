package gateway

import "time"

// homeActivity is what the gateway knows about the use of one home.
type homeActivity struct {
	last      time.Time // the last request for the home
	terminals int       // terminals open now
}

func (rt *Router) activityOf(home string) *homeActivity {
	a := rt.activity[home]
	if a == nil {
		a = &homeActivity{}
		rt.activity[home] = a
	}
	return a
}

// touchHome records that a request used the home.
func (rt *Router) touchHome(home string) {
	if home == "" {
		return
	}
	rt.amu.Lock()
	rt.activityOf(home).last = time.Now()
	rt.amu.Unlock()
}

func (rt *Router) terminalOpened(home string) {
	if home == "" {
		return
	}
	rt.amu.Lock()
	a := rt.activityOf(home)
	a.terminals++
	a.last = time.Now()
	rt.amu.Unlock()
}

func (rt *Router) terminalClosed(home string) {
	if home == "" {
		return
	}
	rt.amu.Lock()
	a := rt.activityOf(home)
	if a.terminals > 0 {
		a.terminals--
	}
	a.last = time.Now()
	rt.amu.Unlock()
}

// Expired is a guest session that ended because its home was idle.
type Expired struct {
	Key     string
	Home    string
	Backend string
}

// ExpireGuests ends the guest sessions whose home has had no request and no
// open terminal for ttl, and returns them so that the caller can delete the
// home on the gateway and tell the worker that held it to do the same.
// Signed-in users never expire. It does nothing unless the router was given
// HomeOf (workspace sync).
func (rt *Router) ExpireGuests(now time.Time, ttl time.Duration) []Expired {
	if rt.homeOf == nil {
		return nil
	}
	var out []Expired
	for _, ec := range rt.registry.Snapshot() {
		if ec.UID != "" || ec.Home == "" {
			continue
		}
		rt.amu.Lock()
		a := rt.activityOf(ec.Home)
		busy := a.terminals > 0
		last := a.last
		rt.amu.Unlock()
		if busy {
			continue
		}
		if last.IsZero() {
			last = ec.CreatedAt
		}
		if now.Sub(last) <= ttl {
			continue
		}
		rt.registry.Release(ec.Key)
		rt.amu.Lock()
		delete(rt.activity, ec.Home)
		rt.amu.Unlock()
		out = append(out, Expired{Key: ec.Key, Home: ec.Home, Backend: ec.BackendID})
	}
	return out
}
