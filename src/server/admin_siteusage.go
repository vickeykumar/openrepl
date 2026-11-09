package server

import (
	"net/http"
	"strconv"
	"time"

	"user"
)

// The numbers behind the dashboard's Site usage view and the usage columns of its
// Users table (siteusage.go holds the counting).

// genieAnswersThisMonth is how many answers an account has had from Genie this month.
func genieAnswersThisMonth(rec tokenRecord, now time.Time) int64 {
	var n int64
	for _, m := range rec.forMonth(monthOf(now)).Models {
		n += m.MAns + m.AAns
	}
	return n
}

func (server *Server) handleAdminSiteUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	n := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && (v == 7 || v == 30 || v == 90) {
		n = v
	}
	accounts, err := user.ListAccounts()
	if err != nil {
		adminError(w, http.StatusInternalServerError, "Could not read the accounts.")
		return
	}
	joins := make([]Join, 0, len(accounts))
	firsts := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		joins = append(joins, Join{Uid: a.Uid, At: a.FirstSignIn})
		firsts = append(firsts, a.FirstSignIn)
	}
	out := siteUsage.overview(n, firsts)
	out.Cohorts, out.Back7Pct = siteUsage.cohorts(joins, 4)
	adminJSON(w, http.StatusOK, out)
}

// adminUserUsage answers GET admin/users/<uid>/usage.
func (server *Server) adminUserUsage(w http.ResponseWriter, uid string) {
	up, err := user.FetchUserProfileData(uid)
	if err != nil {
		adminError(w, http.StatusNotFound, "No such account.")
		return
	}
	d, _ := siteUsage.userDetail(uid)
	rec := tokenTotals.user(uid)
	adminJSON(w, http.StatusOK, map[string]interface{}{
		"uid": uid, "email": up.Email, "name": up.Name,
		"firstSignIn": up.FirstSignIn, "lastSignIn": up.LastSignIn, "signIns": up.SignIns,
		"usage": d, "genieAnswers": genieAnswersThisMonth(rec, time.Now()),
	})
}
