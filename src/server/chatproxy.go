package server

import (
	"bytes"
	"log"
	"net/http"
	"net/url"
	"strings"
	"utils"
	"io"
	"cookie"
	"encoder"
	"github.com/pkg/errors"
)

var (
	openaiEndpoint = "https://api.openai.com/v1/chat/completions"
	authHeader    = "Authorization"
)

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
    chatBody, err := sanitizeChatBody(rawBody)
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

    var num_req_rem float64
    if !IsUserAdmin(rw, req) {
	    //recharge,  no restriction for admin
	    err = cookie.UpdateOpenApiRequestCountBalance(rw, req)
	    if err!=nil {
	    	log.Println("Error: updating request balance : ", err)
	    }
	    num_req_rem = cookie.GetOpenApiRequestCount(req)
	    log.Println("remaining request balance : ", num_req_rem)
	    if num_req_rem <= 0 {
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

	// Send request to OpenAI
    var customTransport = http.DefaultTransport
    proxyReq, err := http.NewRequest(req.Method, openaiEndpoint, bytes.NewReader(chatBody))
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
	proxyReq.Header.Set(authHeader, req.Header.Get(authHeader))


	// Send the proxy request using the custom transport
	resp, err := customTransport.RoundTrip(proxyReq)
	if err != nil {
		log.Println("Error: making request: ", err)
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

	if !IsUserAdmin(rw, req) {
		// roundtrip was success, decrease the request count by 1
		cookie.SetOpenApiRequestCount(rw, req, num_req_rem-1)
	}

	defer resp.Body.Close()

    // Copy the response status and headers to the response writer
    for key, values := range resp.Header {
        for _, value := range values {
            rw.Header().Add(key, value)
        }
    }
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