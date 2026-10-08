package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"utils"
	"io"
	"cookie"
	"encoder"
	"github.com/pkg/errors"
)

var (
	openaiEndpoint     = "https://api.openai.com/v1/chat/completions"
	openrouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	authHeader         = "Authorization"
)

// How long a model may take. Cloudflare in front of the site gives up on a
// reply at about 100 seconds, so both stay below that; a request that runs out
// of time is answered with "isn't available right now". OpenRouter's Gemma
// answers in 5 to 10 seconds, so a minute is generous. An admin can change the
// OpenRouter one (settings, genie.openRouterTimeoutSec); openRouterTimeout, when
// not zero, overrides it, for tests.
var (
	openAITimeout     = 90 * time.Second
	openRouterTimeout time.Duration
)

// modelSwitchedOff answers a request for a model an admin switched off. The
// type is the same as for an unreachable model; the code tells the page apart,
// so that it offers no "Try again".
func modelSwitchedOff(rw http.ResponseWriter, req *http.Request, model chatModel) {
	rw.Header().Set("Content-Type", "application/json")
	handleChatProxyError(rw, req, http.StatusServiceUnavailable,
		NewErrorResponse(
			model.Name+" is switched off right now",
			"model_unavailable",
			"model_disabled",
			"",
		),
	)
}

// modelUnavailable answers a request for a model that cannot be reached, in
// the shape of an OpenAI error. The Genie panel and the New question dialog
// look for the type "model_unavailable" to offer another model or a retry. The
// reason (a missing key, a refusal from OpenRouter, a timeout) goes to the log,
// not to the visitor.
func modelUnavailable(rw http.ResponseWriter, req *http.Request, model chatModel) {
	rw.Header().Set("Content-Type", "application/json")
	handleChatProxyError(rw, req, http.StatusServiceUnavailable,
		NewErrorResponse(
			model.Name+" isn't available right now",
			"model_unavailable",
			"model_unavailable",
			"",
		),
	)
}

// openAIToken and chatHost are looked up when they are needed, not at start-up:
// main loads the env file after package initialisation, and a setting that
// came from it would be missed by an init().
func openAIToken() string { return utils.OpenAIKey() }

func chatHost() string {
	if h := utils.Host(); h != "" {
		return h
	}
	return "localhost"
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param,omitempty"` // Pointer to string, omitempty makes it optional
	Code    string  `json:"code"`
}

func NewErrorResponse(message, msgtype, errcode, param string) *ErrorResponse {
	var p *string = nil
	if param!="" {
		p = &param
	}
	err := ErrorDetail{
		Message: message,
		Type: msgtype,
		Param: p,
		Code: errcode,
	}
	return &ErrorResponse{
		Error: err,
	}
}

func extractHostFromHeader(headerValue string) (string, error) {
    parsedURL, err := url.Parse(headerValue)
    if err != nil {
        return "", err
    }
    return parsedURL.Host, nil
}

func getAccessTokenFromAuthHeader(req *http.Request) string {
	// if auth header is empty
	if req.Header.Get(authHeader) == "" {
		return ""
	} else {
		log.Println("token found in request")
		bearerHeader := req.Header.Get(authHeader)
		arr := strings.Split(bearerHeader, " ")
		if len(arr) == 2 {
			return arr[1]
		}
	}
	return ""
}

func handleChatProxyError(rw http.ResponseWriter, req *http.Request, status int, err *ErrorResponse) {
	rw.WriteHeader(status)
	rw.Write(utils.JsonMarshal(err))
}

func handleModifyHeaderRequest(rw http.ResponseWriter, req *http.Request) error {
	access_token := getAccessTokenFromAuthHeader(req)
	if access_token=="" {
		handleChatProxyError(rw, req, http.StatusBadRequest, 
        	NewErrorResponse(
        		"You didn't provide an Acess Token Key. You need to provide your Acess Token Key in an Authorization header using Bearer auth (i.e. Authorization: Bearer YOUR_KEY)",
        		"invalid_request_error",
        		"StatusBadRequest",
        		"",
        	),
        )
        return errors.New("empty Access Token Key found in header: "+access_token)
	}
	_, cookie_secret := cookie.GetOpenApiAccessToken(req) 
	// cookie token should be same as token derived from header
	if string(cookie_secret)=="" {
		cookie_secret = cookie.SECRET_KEY
	}
	access_keybytes, err := encoder.Decrypt([]byte(access_token), cookie_secret)
	if err!=nil {
		handleChatProxyError(rw, req, http.StatusUnauthorized, 
        	NewErrorResponse(
        		"Unauthorized or Expired Request Access Token. please refresh to start again...",
        		"StatusUnauthorized",
        		"StatusUnauthorized",
        		"",
        	),
        )
        return err
	}
	access_token_key := string(access_keybytes)
	if access_token_key!=openAIToken() {
		// unautorized request
		handleChatProxyError(rw, req, http.StatusUnauthorized, 
        	NewErrorResponse(
        		"Unauthorized Request Access Token.",
        		"StatusUnauthorized",
        		"StatusUnauthorized",
        		"",
        	),
        )
        return errors.New("Unauthorized Request Access Token: "+access_token_key)
	}
	// all sorted its a valid request
	req.Header.Del(authHeader)
	req.Header.Set(authHeader, "Bearer "+openAIToken())
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func handleChatProxy(rw http.ResponseWriter, req *http.Request) {
	// An admin can switch Genie off for everybody but admins.
	if GetSiteSettings().Genie.Disabled && !IsUserAdmin(rw, req) {
		handleChatProxyError(rw, req, http.StatusServiceUnavailable,
			NewErrorResponse(
				"Genie is switched off for now. Please try again later.",
				"GenieDisabled",
				"StatusServiceUnavailable",
				"",
			),
		)
		return
	}
	log.Println("req header: ", "origin: ",req.Header.Get("Origin"), req.Header.Get("Referer"))
	originHost, _ := extractHostFromHeader(req.Header.Get("Origin"))
	refererHost, _ := extractHostFromHeader(req.Header.Get("Referer"))
	if host := chatHost(); !strings.Contains(refererHost, host) || !strings.Contains(originHost, host) {
        handleChatProxyError(rw, req, http.StatusForbidden, 
        	NewErrorResponse(
        		"Origin not allowed.",
        		"invalid_domain_origin",
        		"forbiddend",
        		"",
        	),
        )
		return
    }

    err := handleModifyHeaderRequest(rw, req)
    if err!=nil {
    	// errors are already handler in above handler,  just log and return
    	log.Println("Error handling handleModifyHeaderRequest: ", err)
    	return
    }

    // Read the body and send on only what the Genie panel offers: a model from
    // the short list, its own settings and capped answer sizes (chatmodels.go).
    rawBody, err := io.ReadAll(http.MaxBytesReader(rw, req.Body, maxChatBodyBytes))
    if err != nil {
    	handleChatProxyError(rw, req, http.StatusRequestEntityTooLarge,
        	NewErrorResponse(
        		"This conversation is too long to send. Start a new one.",
        		"invalid_request_error",
        		"request_too_large",
        		"",
        	),
        )
    	return
    }
    // A request in agent mode (agent.go): signed-in users, within a task's steps
    // and the hourly count. The first step of a task is the one that is charged.
    isAdmin := IsUserAdmin(rw, req)
    run, agentReq, aerr := agentStart(req, rawBody, isAdmin)
    if aerr != nil {
    	handleChatProxyError(rw, req, aerr.Status, aerr.response())
    	return
    }
    succeeded := false
    defer func() {
    	if run != nil && !succeeded {
    		agents.undo(run) // the model could not answer: give the step back
    	}
    }()
    if run != nil {
    	rawBody = agentReq
    }
    chargeable := run == nil || run.first

    // A right-click action on selected code (action.go): the server writes the
    // messages from the fields of the request.
    if run == nil {
    	actionBody, isAction, xerr := actionStart(req, rawBody)
    	if xerr != nil {
    		handleChatProxyError(rw, req, xerr.Status, xerr.response())
    		return
    	}
    	if isAction {
    		rawBody = actionBody
    	}
    	// the practice coach (coach.go), likewise
    	coachBody, isCoach, cerr := coachStart(req, rawBody)
    	if cerr != nil {
    		handleChatProxyError(rw, req, cerr.Status, cerr.response())
    		return
    	}
    	if isCoach {
    		rawBody = coachBody
    	}
    }

    // A request from the Genie panel or the blog editor may ask for what the
    // site knows about itself (knowledge_proxy.go).
    rawBody, ctxResult := addKnowledge(rawBody, theKnowledge())
    chatBody, model, err := sanitizeChatBody(rawBody)
    if err != nil {
    	handleChatProxyError(rw, req, http.StatusBadRequest,
        	NewErrorResponse(
        		err.Error(),
        		"invalid_request_error",
        		"invalid_request",
        		"",
        	),
        )
    	return
    }

    // A model an admin switched off is refused before anything is charged or
    // sent on (admins are not an exception: the picker hides it for them too).
    if GetSiteSettings().Genie.ModelDisabled(model.ID) {
        modelSwitchedOff(rw, req, model)
        return
    }

    // Where the request goes: OpenAI, or OpenRouter for the open model. Without
    // the OpenRouter key the open model is not available, and nobody is charged.
    upstreamURL, upstreamKey, timeout := openaiEndpoint, openAIToken(), openAITimeout
    if model.Provider == providerOpenRouter {
        upstreamURL, upstreamKey, timeout = openrouterEndpoint, utils.OpenRouterKey(), openRouterTimeoutSetting()
        if openRouterTimeout > 0 {
            timeout = openRouterTimeout
        }
        if upstreamKey == "" {
            log.Println("Error: ", model.ID, "was asked for, but", utils.EnvOpenRouterKey, "is not set")
            modelUnavailable(rw, req, model)
            return
        }
    }

    var num_req_rem float64
    if !isAdmin {
	    //recharge,  no restriction for admin
	    err = cookie.UpdateOpenApiRequestCountBalance(rw, req)
	    if err!=nil {
	    	log.Println("Error: updating request balance : ", err)
	    }
	    num_req_rem = cookie.GetOpenApiRequestCount(req)
	    log.Println("remaining request balance : ", num_req_rem)
	    if chargeable && num_req_rem <= 0 {
	    	handleChatProxyError(rw, req, http.StatusTooManyRequests, 
	        	NewErrorResponse(
	        		"Number of Requests Exceeded for this user, please try again after sometimes. If you are a guest user, please login to get more Requests.",
	        		"TooManyRequests",
	        		"StatusTooManyRequests",
	        		"",
	        	),
	        )
			return
	    }
	}

	// Send the request on
    var customTransport = http.DefaultTransport
    ctx, cancel := context.WithTimeout(req.Context(), timeout)
    defer cancel()
    proxyReq, err := http.NewRequestWithContext(ctx, req.Method, upstreamURL, bytes.NewReader(chatBody))
	if err != nil {
		log.Println("Error: making request: ", err)
		handleChatProxyError(rw, req, http.StatusInternalServerError, 
        	NewErrorResponse(
        		"StatusInternalServerError",
        		"StatusInternalServerError",
        		"StatusInternalServerError",
        		"",
        	),
        )
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set(authHeader, "Bearer "+upstreamKey)
	if model.Provider == providerOpenRouter {
		proxyReq.Header.Set("X-Title", "OpenREPL")
	}


	// Send the proxy request using the custom transport
	resp, err := customTransport.RoundTrip(proxyReq)
	if err != nil {
		log.Println("Error: making request: ", err)
		if model.Provider == providerOpenRouter {
			modelUnavailable(rw, req, model)
			return
		}
		handleChatProxyError(rw, req, http.StatusInternalServerError, 
        	NewErrorResponse(
        		"StatusInternalServerError: "+err.Error(),
        		"StatusInternalServerError",
        		"StatusInternalServerError",
        		"",
        	),
        )
		return
	}

	defer resp.Body.Close()

	if model.Provider == providerOpenRouter && resp.StatusCode >= 400 {
		// no host could answer (404), no credit (402), a limit (429) or a host
		// error: the visitor is told the model is not available, not charged
		// a request, and the reason is logged
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Println("Error: OpenRouter answered", resp.StatusCode, "for", model.ID, ":", string(detail))
		modelUnavailable(rw, req, model)
		return
	}

	if resp.StatusCode < 400 {
		succeeded = true
	}
	// A task whose first step the model could not answer is given back
	// (the deferred undo above), so it is not charged either.
	if chargeable && !isAdmin && (run == nil || succeeded) {
		// roundtrip was success, decrease the request count by 1
		num_req_rem--
		cookie.SetOpenApiRequestCount(rw, req, num_req_rem)
	}

    // Copy the response status and headers to the response writer
    for key, values := range resp.Header {
        for _, value := range values {
            rw.Header().Add(key, value)
        }
    }
    // what the answer was based on, when it was asked for
    var exposed []string
    if h := ctxResult.header(); h != "" {
        rw.Header().Set(contextHeader, h)
        exposed = append(exposed, contextHeader)
    }
    // the task and its step, for a request in agent mode
    if run != nil {
        rw.Header().Set(agentTaskHeader, run.token)
        rw.Header().Set(agentStepHeader, fmt.Sprintf("%d/%d", run.step, run.max))
        exposed = append(exposed, agentTaskHeader, agentStepHeader)
    }
    // what the visitor has left after this answer, for the panel's usage line
    rw.Header().Set(usageHeader, usageOf(req, isAdmin, &num_req_rem).header())
    exposed = append(exposed, usageHeader)
    if len(exposed) > 0 {
        rw.Header().Set("Access-Control-Expose-Headers", strings.Join(exposed, ", "))
    }
    // the upstream status as well, so that a refusal is not shown as a success
    rw.WriteHeader(resp.StatusCode)
    // Copy the response body to the response writer
    _, err = io.Copy(rw, resp.Body)
    if err != nil {
        log.Println("Error: copying response body", err)
        handleChatProxyError(rw, req, http.StatusInternalServerError, 
        	NewErrorResponse(
        		"StatusInternalServerError",
        		"StatusInternalServerError",
        		"StatusInternalServerError",
        		"",
        	),
        )
        return
    }
}