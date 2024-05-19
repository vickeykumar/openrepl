package server

import (
	"encoding/base64"
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
	defaultToken  string
	openaiEndpoint = "https://api.openai.com/v1/chat/completions"
	authHeader    = "Authorization"
	host = "localhost"
)

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

func init() {
	encodedtoken := utils.GitConfig["user.OpenaiAPIKey"]
	tokenbytes, err := base64.StdEncoding.DecodeString(encodedtoken)
	if err==nil {
		defaultToken = strings.TrimSpace(string(tokenbytes))
	}
	//log.Println("read token: ", defaultToken)
	hostread := utils.GitConfig["user.host"]
	if (hostread!="") {
		host = hostread
	}
	log.Println("hosted on: ", host)
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
        		"Unauthorized Request Access Token.",
        		"StatusUnauthorized",
        		"StatusUnauthorized",
        		"",
        	),
        )
        return err
	}
	access_token_key := string(access_keybytes)
	if access_token_key!=defaultToken {
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
	req.Header.Set(authHeader, "Bearer "+defaultToken)
	req.Header.Set("Content-Type", "application/json")
	return nil
}

func handleChatProxy(rw http.ResponseWriter, req *http.Request) {
	log.Println("req header: ", "origin: ",req.Header.Get("Origin"), req.Header.Get("Referer"))
	originHost, _ := extractHostFromHeader(req.Header.Get("Origin"))
	refererHost, _ := extractHostFromHeader(req.Header.Get("Referer"))
	if !strings.Contains(refererHost, host) || !strings.Contains(originHost, host) {
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
    proxyReq, err := http.NewRequest(req.Method, openaiEndpoint, req.Body)
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