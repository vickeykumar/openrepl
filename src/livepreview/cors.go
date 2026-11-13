package livepreview

import (
	"net/http"
	"strconv"
	"strings"
)

// The page in the frame has no origin (it is sandboxed), so every request it makes to its own program is cross-origin for
// the browser, and a request that is not "simple" (a POST of JSON, a PUT, a DELETE, a header of its own) is first asked
// about with an OPTIONS. A program written for a page of its own origin does not answer that (a Flask or Express app
// answers it without the CORS headers), and the request would never be sent. The preview answers it, as the browser
// would never have asked it of a page on the program's own address.

// Preflight answers a CORS preflight request (an OPTIONS that names the method it is for) and reports whether it did.
func Preflight(rw http.ResponseWriter, req *http.Request) bool {
	if req.Method != http.MethodOptions || req.Header.Get("Access-Control-Request-Method") == "" {
		return false
	}
	h := rw.Header()
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
	if want := req.Header.Get("Access-Control-Request-Headers"); want != "" {
		h.Set("Access-Control-Allow-Headers", want)
	} else {
		h.Set("Access-Control-Allow-Headers", "*")
	}
	h.Set("Access-Control-Max-Age", "600")
	h.Add("Vary", "Access-Control-Request-Headers")
	rw.WriteHeader(http.StatusNoContent)
	return true
}

// ownAddress reads an absolute address that names the program's own port on the machine, as the program itself would write
// it (http://127.0.0.1:5000/x, http://localhost:5000/x, http://0.0.0.0:5000/x), and gives the rest ("/x").
func ownAddress(loc string, port int) (string, bool) {
	p := strconv.Itoa(port)
	for _, host := range []string{"127.0.0.1", "localhost", "0.0.0.0", "[::1]", "[::]"} {
		base := "http://" + host + ":" + p
		if loc == base {
			return "/", true
		}
		if strings.HasPrefix(loc, base+"/") || strings.HasPrefix(loc, base+"?") {
			rest := strings.TrimPrefix(loc, base)
			if !strings.HasPrefix(rest, "/") {
				rest = "/" + rest
			}
			return rest, true
		}
	}
	return "", false
}
