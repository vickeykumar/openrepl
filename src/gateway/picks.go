package gateway

// How the weighted random choice has been distributing sessions. Only the
// picker's choices are counted: a signed-in user who goes back to the worker
// that holds their files was not picked, and counting them would hide
// whether the choice itself follows the weights.

func (rt *Router) countPick(backendID string) {
	rt.pickMu.Lock()
	rt.picks[backendID]++
	rt.pickMu.Unlock()
}

// pickCounts returns a copy of the counts and their sum. A backend that has
// left still has its count in the sum.
func (rt *Router) pickCounts() (map[string]int64, int64) {
	rt.pickMu.Lock()
	defer rt.pickMu.Unlock()
	out := make(map[string]int64, len(rt.picks))
	var total int64
	for id, n := range rt.picks {
		out[id] = n
		total += n
	}
	return out, total
}

func (rt *Router) pickTotal() int64 {
	_, total := rt.pickCounts()
	return total
}
