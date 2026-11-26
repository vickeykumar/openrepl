package server

import (
	"encoding/csv"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"ideusage"
	"user"
)

// The history and the accounts that need a look, for admins (canvas "IDE data usage: daily history and top users", LLD 20 section 10).
// GET /admin/ide-usage?report=<days>: the days of all signed-in accounts and a table of the accounts, with the flags and the watch list.
// GET /admin/ide-usage?acct=<uid>&days=<n>: one account. POST /admin/ide-usage?watch=1|0&uid=: the watch list.

const maxIDEReportRows = 200

type ideName struct {
	Name, Email string
	First       int64
	at          time.Time
}

var (
	ideNameMu    sync.Mutex
	ideNameCache = map[string]ideName{}
)

// ideNameOf is the name and email of an account, kept for ten minutes (a profile is read from the user database).
func ideNameOf(uid string) ideName {
	ideNameMu.Lock()
	n, ok := ideNameCache[uid]
	ideNameMu.Unlock()
	if ok && time.Since(n.at) < 10*time.Minute {
		return n
	}
	n = ideName{at: time.Now()}
	if up, err := user.FetchUserProfileData(uid); err == nil {
		n.Name, n.Email, n.First = up.Name, up.Email, up.FirstSignIn
	}
	ideNameMu.Lock()
	if len(ideNameCache) > 5000 {
		ideNameCache = map[string]ideName{}
	}
	ideNameCache[uid] = n
	ideNameMu.Unlock()
	return n
}

type ideWatched struct {
	UID   string `json:"uid"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

type ideThresholds struct {
	LimitDays   int  `json:"limitDays"`
	Jump        int  `json:"jump"`
	NearPct     int  `json:"nearPct"`
	Stops       int  `json:"stops"`
	Log         bool `json:"log"`
	HistoryDays int  `json:"historyDays"`
	MinJumpMB   int  `json:"minJumpMB"`
}

func ideThresholdsNow() ideThresholds {
	c := ideusage.ConfigFunc()
	return ideThresholds{LimitDays: c.FlagLimitDays, Jump: c.FlagJump, NearPct: c.FlagNearPct, Stops: c.FlagStops, Log: c.FlagLog, HistoryDays: c.HistoryDays, MinJumpMB: int(ideusage.FlagMinJump / ideusage.MB)}
}

// ideFlagsNow is the accounts to open today, with their names.
func ideFlagsNow() []ideusage.Flag {
	watch := GetSiteSettings().IDEWatch
	flags := ideMeter.Flags(watch)
	for i := range flags {
		n := ideNameOf(flags[i].UID)
		flags[i].Name, flags[i].Email = n.Name, n.Email
	}
	return flags
}

type ideReportReply struct {
	ideusage.Report
	AccountsTotal int             `json:"accountsTotal"`
	Flags         []ideusage.Flag `json:"flags"`
	Watch         []ideWatched    `json:"watch"`
	Thresholds    ideThresholds   `json:"thresholds"`
	On            bool            `json:"on"`
}

func (server *Server) adminIDEReport(w http.ResponseWriter, r *http.Request, days int) {
	rep := ideMeter.Report(days)
	total := len(rep.Accounts)
	if total > maxIDEReportRows {
		rep.Accounts = rep.Accounts[:maxIDEReportRows]
	}
	for i := range rep.Accounts {
		n := ideNameOf(rep.Accounts[i].UID)
		rep.Accounts[i].Name, rep.Accounts[i].Email = n.Name, n.Email
	}
	if r.URL.Query().Get("format") == "csv" {
		writeIDEReportCSV(w, rep)
		return
	}
	reply := ideReportReply{Report: rep, AccountsTotal: total, Flags: ideFlagsNow(), Thresholds: ideThresholdsNow(), On: ideusage.ConfigFunc().On}
	reply.Watch = []ideWatched{}
	for _, uid := range GetSiteSettings().IDEWatch {
		n := ideNameOf(uid)
		reply.Watch = append(reply.Watch, ideWatched{UID: uid, Name: n.Name, Email: n.Email})
	}
	if reply.Flags == nil {
		reply.Flags = []ideusage.Flag{}
	}
	json.NewEncoder(w).Encode(reply)
}

func (server *Server) adminIDEAccount(w http.ResponseWriter, r *http.Request, uid string, days int) {
	v, ok := ideMeter.Account(uid, days)
	n := ideNameOf(uid)
	if r.URL.Query().Get("format") == "csv" {
		writeIDEAccountCSV(w, v, n)
		return
	}
	watched := false
	for _, u := range GetSiteSettings().IDEWatch {
		watched = watched || u == uid
	}
	c := ideusage.ConfigFunc()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"account": v, "known": ok, "uid": uid, "name": n.Name, "email": n.Email, "firstSignIn": n.First,
		"watched": watched, "limit": c.UserBytes, "period": days, "historyDays": c.HistoryDays,
	})
}

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// setIDEWatch puts an account on the watch list or takes it off, leaving everything else in the settings as it is.
func setIDEWatch(uid string, on bool) error {
	cur := GetSiteSettings()
	list := []string{}
	for _, u := range cur.IDEWatch {
		if u != uid {
			list = append(list, u)
		}
	}
	if on {
		if len(list) >= maxIDEWatch {
			return errIDEWatchFull
		}
		list = append(list, uid)
	}
	sort.Strings(list)
	cur.IDEWatch = list
	return SaveSiteSettings(cur, -1)
}

func writeIDEReportCSV(w http.ResponseWriter, rep ideusage.Report) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="openrepl-data-usage.csv"`)
	cw := csv.NewWriter(w)
	head := []string{"name", "email", "period_days", "total_bytes", "avg_bytes_per_day", "days_at_limit", "peak_bytes", "peak_day", "today_bytes", "terminal_bytes", "genie_bytes", "files_bytes", "editor_bytes", "preview_bytes"}
	cw.Write(head)
	for _, a := range rep.Accounts {
		cw.Write([]string{csvCell(a.Name), csvCell(a.Email), strconv.Itoa(rep.Period), strconv.FormatInt(a.Total, 10), strconv.FormatInt(a.Avg, 10), strconv.Itoa(a.LimitDays),
			strconv.FormatInt(a.Peak, 10), a.PeakDay, strconv.FormatInt(a.Today, 10),
			strconv.FormatInt(a.Parts["terminal"], 10), strconv.FormatInt(a.Parts["genie"], 10), strconv.FormatInt(a.Parts["files"], 10), strconv.FormatInt(a.Parts["editor"], 10), strconv.FormatInt(a.Parts["preview"], 10)})
	}
	cw.Flush()
}

func writeIDEAccountCSV(w http.ResponseWriter, v ideusage.AcctView, n ideName) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="openrepl-data-usage-account.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"day", "total_bytes", "terminal_bytes", "genie_bytes", "files_bytes", "editor_bytes", "preview_bytes"})
	for _, d := range v.Days {
		cw.Write([]string{d.Day, strconv.FormatInt(d.Total, 10), strconv.FormatInt(d.Parts["terminal"], 10), strconv.FormatInt(d.Parts["genie"], 10),
			strconv.FormatInt(d.Parts["files"], 10), strconv.FormatInt(d.Parts["editor"], 10), strconv.FormatInt(d.Parts["preview"], 10)})
	}
	cw.Flush()
}

const maxIDEWatch = 50

var errIDEWatchFull = jsonErr("at most 50 accounts can be watched")

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

// ---- the line in the server log ------------------------------------------------------------------------------------------------

var (
	ideLogMu   sync.Mutex
	ideLogDone = map[string]bool{} // "day|uid|kind": already written
)

// logIDEFlags writes a line for each account flagged, once for each account, kind and day, when the setting is on. The line names the account
// and says why: no sizes of what it did, never a file or a message.
func logIDEFlags() {
	c := ideusage.ConfigFunc()
	if !c.On || !c.FlagLog {
		return
	}
	day := time.Now().UTC().Format("2006-01-02")
	ideLogMu.Lock()
	defer ideLogMu.Unlock()
	for k := range ideLogDone {
		if len(k) < 10 || k[:10] != day {
			delete(ideLogDone, k)
		}
	}
	for _, f := range ideFlagsNow() {
		if f.Kind == "watched" {
			continue
		}
		k := day + "|" + f.UID + "|" + f.Kind
		if ideLogDone[k] {
			continue
		}
		ideLogDone[k] = true
		who := f.Name
		if who == "" {
			who = f.UID
		}
		log.Printf("IDE data usage: %s needs a look (%s): %s", who, f.Kind, f.Text)
	}
}
