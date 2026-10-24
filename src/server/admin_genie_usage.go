package server

import (
	"encoding/csv"
	"net/http"
	"sort"
	"strconv"

	"user"
)

// The Genie usage card of the dashboard: the tokens each signed-in user has spent,
// by model, this month or all time, with the cost worked out from the prices an
// admin set (or the cost OpenRouter reported). Visitors are one line, as they are
// counted together (tokens.go).
//
//	GET /admin/genie-usage?range=month|all           the table as JSON
//	GET /admin/genie-usage?range=month|all&format=csv   the same as a download

const maxUsageRows = 500

type usageModelRow struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Answers int64   `json:"answers"`
	In      int64   `json:"in"`
	Out     int64   `json:"out"`
	Cost    float64 `json:"cost"`
	Partial bool    `json:"partial,omitempty"` // no price and no reported cost
}

type usageUserRow struct {
	UID     string          `json:"uid,omitempty"`
	Email   string          `json:"email,omitempty"`
	Name    string          `json:"name,omitempty"`
	Answers int64           `json:"answers"`
	In      int64           `json:"in"`
	Out     int64           `json:"out"`
	Cost    float64         `json:"cost"`
	Partial bool            `json:"partial,omitempty"`
	Models  []usageModelRow `json:"models"`
}

type genieUsageReply struct {
	Range     string         `json:"range"` // "month" or "all"
	Month     string         `json:"month"`
	Users     []usageUserRow `json:"users"`
	Visitors  usageUserRow   `json:"visitors"`
	Total     usageUserRow   `json:"total"`
	PricesSet bool           `json:"pricesSet"`
	Truncated bool           `json:"truncated,omitempty"`
}

func modelNameOf(id string) string {
	if m, ok := modelByID(id); ok && m.Name != "" {
		return m.Name
	}
	return id
}

// usageRowOf is one record as a row, its models busiest first.
func usageRowOf(r tokenRecord, month bool, prices map[string]modelPrice) usageUserRow {
	row := usageUserRow{Models: []usageModelRow{}}
	for id, m := range r.Models {
		in, out, ans := m.AIn, m.AOut, m.AAns
		if month {
			in, out, ans = m.MIn, m.MOut, m.MAns
		}
		if ans == 0 {
			continue
		}
		cost, partial := m.cost(month, prices[id])
		row.Models = append(row.Models, usageModelRow{ID: id, Name: modelNameOf(id), Answers: ans, In: in, Out: out, Cost: cost, Partial: partial})
		row.Answers, row.In, row.Out, row.Cost = row.Answers+ans, row.In+in, row.Out+out, row.Cost+cost
		row.Partial = row.Partial || partial
	}
	sort.Slice(row.Models, func(i, j int) bool {
		a, b := row.Models[i], row.Models[j]
		if a.In+a.Out != b.In+b.Out {
			return a.In+a.Out > b.In+b.Out
		}
		return a.ID < b.ID
	})
	return row
}

func addRow(total *usageUserRow, r usageUserRow) {
	total.Answers, total.In, total.Out, total.Cost = total.Answers+r.Answers, total.In+r.In, total.Out+r.Out, total.Cost+r.Cost
	total.Partial = total.Partial || r.Partial
}

func buildGenieUsage(month bool) genieUsageReply {
	g := GetSiteSettings().Genie
	reply := genieUsageReply{Range: "all", Month: monthOf(tokenTotals.now()), Users: []usageUserRow{}, PricesSet: len(g.ModelPrices) > 0}
	if month {
		reply.Range = "month"
	}
	reply.Visitors = usageRowOf(tokenTotals.visitors(), month, g.ModelPrices)
	for uid, rec := range tokenTotals.every() {
		row := usageRowOf(rec, month, g.ModelPrices)
		if row.Answers == 0 {
			continue
		}
		row.UID = uid
		reply.Users = append(reply.Users, row)
	}
	sort.Slice(reply.Users, func(i, j int) bool {
		a, b := reply.Users[i], reply.Users[j]
		if a.Cost != b.Cost {
			return a.Cost > b.Cost
		}
		if a.In+a.Out != b.In+b.Out {
			return a.In+a.Out > b.In+b.Out
		}
		return a.UID < b.UID
	})
	for _, row := range reply.Users {
		addRow(&reply.Total, row)
	}
	addRow(&reply.Total, reply.Visitors)
	if len(reply.Users) > maxUsageRows {
		reply.Users, reply.Truncated = reply.Users[:maxUsageRows], true
	}
	for i := range reply.Users {
		if p, err := user.FetchUserProfileData(reply.Users[i].UID); err == nil {
			reply.Users[i].Email, reply.Users[i].Name = p.Email, p.Name
		}
	}
	return reply
}

func (server *Server) handleAdminGenieUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		adminError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	reply := buildGenieUsage(r.URL.Query().Get("range") != "all")
	if r.URL.Query().Get("format") != "csv" {
		w.Header().Set("Cache-Control", "no-store")
		adminJSON(w, http.StatusOK, reply)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="genie-usage-`+reply.Range+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	out := csv.NewWriter(w)
	out.Write([]string{"user", "email", "model", "answers", "tokens_sent", "tokens_written", "cost_usd", "cost_is_partial"})
	line := func(who, email string, m usageModelRow) {
		out.Write([]string{csvCell(who), csvCell(email), csvCell(m.Name), strconv.FormatInt(m.Answers, 10), strconv.FormatInt(m.In, 10),
			strconv.FormatInt(m.Out, 10), strconv.FormatFloat(m.Cost, 'f', 6, 64), strconv.FormatBool(m.Partial)})
	}
	for _, u := range reply.Users {
		who := u.Name
		if who == "" {
			who = u.UID
		}
		for _, m := range u.Models {
			line(who, u.Email, m)
		}
	}
	for _, m := range reply.Visitors.Models {
		line("Visitors", "", m)
	}
	out.Flush()
}
