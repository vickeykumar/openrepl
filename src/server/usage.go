package server

import (
	"encoding/json"
	"math"
	"net/http"

	"cookie"
)

// What a visitor has left of Genie, for the usage line of the Genie panel:
//
//	GET /chat/usage                          asked when the panel opens
//	X-OpenREPL-Usage: {...}                  on every answer of /chat/completions
//
// Both carry the same JSON. The page counts the recharge itself between
// answers (left grows by perMinute, up to cap), so it does not have to ask.
// Asking is free: nothing is saved and nothing is charged.

const usageHeader = "X-OpenREPL-Usage"

type agentUsage struct {
	Used int `json:"used"` // tasks started in the last hour
	// PerHour is the tasks a user may start in an hour; 0 means no limit.
	PerHour int `json:"perHour"`
	// NextFreeSeconds is when the oldest of them stops counting.
	NextFreeSeconds int `json:"nextFreeSeconds"`
	MaxSteps        int `json:"maxSteps"`
}

type genieUsage struct {
	Left      float64 `json:"left"`      // requests left now; the next one is taken while this is above 0
	Cap       float64 `json:"cap"`       // the most a visitor holds
	PerMinute float64 `json:"perMinute"` // the recharge
	// Unlimited is true for an admin, who is not held to the balance.
	Unlimited bool `json:"unlimited,omitempty"`
	SignedIn  bool `json:"signedIn"`
	// Agent is there for a signed-in user where agent mode is on.
	Agent *agentUsage `json:"agent,omitempty"`
}

// usageOf works out the usage of the visitor behind a request. left is the
// balance when the caller has just worked it out (the proxy, after charging);
// nil reads it from the cookie.
func usageOf(req *http.Request, isAdmin bool, left *float64) genieUsage {
	now, max, perMinute := cookie.OpenApiRequestBalance(req)
	if left != nil {
		now = *left
	}
	u := genieUsage{
		Left:      math.Round(now*100) / 100,
		Cap:       math.Round(max*100) / 100,
		PerMinute: perMinute,
		Unlimited: isAdmin,
	}
	g := GetSiteSettings().Genie
	if uid, ok := agentUser(req); ok {
		u.SignedIn = true
		if !g.AgentDisabled {
			used, next := agents.started(uid)
			perHour := g.agentTasksPerHour()
			if isAdmin {
				perHour = 0
			}
			u.Agent = &agentUsage{Used: used, PerHour: perHour, NextFreeSeconds: int(math.Ceil(next.Seconds())), MaxSteps: g.agentMaxSteps()}
		}
	}
	return u
}

func (u genieUsage) header() string {
	out, _ := json.Marshal(u)
	return string(out)
}

func handleChatUsage(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		rw.Header().Set("Allow", "GET")
		rw.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Write([]byte(usageOf(req, IsUserAdmin(rw, req), nil).header()))
}
