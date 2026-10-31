package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"
	"utils"
)

// The text models of the Cloudflare account, for the dashboard's add-model card: it offers their
// exact ids (which keep the @cf/ prefix) and prices. Cloudflare has no "free" flag on a model: the
// free plan is a daily allowance for the whole account, so the admin decides what to tag Free.

type cloudflareModel struct {
	ID         string  `json:"id"`
	InputPerM  float64 `json:"inputPerM,omitempty"`  // dollars for a million tokens sent
	OutputPerM float64 `json:"outputPerM,omitempty"` // dollars for a million tokens written
}

type cloudflareModelsReply struct {
	// Configured is false until the token and the account id are both set.
	Configured bool              `json:"configured"`
	Models     []cloudflareModel `json:"models"`
	Warning    string            `json:"warning,omitempty"`
}

// parseCloudflareModels reads the model search reply, keeping the models that have a price (the
// others, such as the LoRA adapters, are not general chat models).
func parseCloudflareModels(body []byte) []cloudflareModel {
	var in struct {
		Result []struct {
			Name       string `json:"name"`
			Properties []struct {
				ID    string          `json:"property_id"`
				Value json.RawMessage `json:"value"`
			} `json:"properties"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &in) != nil {
		return nil
	}
	out := []cloudflareModel{}
	for _, m := range in.Result {
		if !cloudflareModelIDPattern.MatchString(m.Name) {
			continue
		}
		cm := cloudflareModel{ID: m.Name}
		for _, p := range m.Properties {
			if p.ID != "price" {
				continue
			}
			var prices []struct {
				Unit  string  `json:"unit"`
				Price float64 `json:"price"`
			}
			if json.Unmarshal(p.Value, &prices) != nil {
				continue
			}
			for _, pr := range prices {
				switch pr.Unit {
				case "per M input tokens":
					cm.InputPerM = pr.Price
				case "per M output tokens":
					cm.OutputPerM = pr.Price
				}
			}
		}
		if cm.InputPerM > 0 || cm.OutputPerM > 0 {
			out = append(out, cm)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// handleAdminCloudflareModels lists the account's text models (GET). It runs behind adminAPI.
func (server *Server) handleAdminCloudflareModels(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		rw.Header().Set("Allow", "GET")
		adminError(rw, http.StatusMethodNotAllowed, "Use GET.")
		return
	}
	reply := cloudflareModelsReply{Models: []cloudflareModel{}}
	if !utils.CloudflareEnabled() {
		adminJSON(rw, http.StatusOK, reply)
		return
	}
	reply.Configured = true
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudflareModelsURL(utils.CloudflareAccountID())+"?task=Text%20Generation&per_page=100", nil)
	if err != nil {
		adminJSON(rw, http.StatusOK, reply)
		return
	}
	r.Header.Set("Authorization", "Bearer "+utils.CloudflareToken())
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		reply.Warning = "Cloudflare could not be reached, so its model list is not shown. You can still type an id."
		adminJSON(rw, http.StatusOK, reply)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		reply.Warning = "Cloudflare did not give its model list (it answered " + http.StatusText(resp.StatusCode) + "). You can still type an id."
		adminJSON(rw, http.StatusOK, reply)
		return
	}
	reply.Models = parseCloudflareModels(body)
	if reply.Models == nil {
		reply.Models = []cloudflareModel{}
	}
	adminJSON(rw, http.StatusOK, reply)
}
