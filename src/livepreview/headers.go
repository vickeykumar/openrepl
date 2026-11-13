package livepreview

import "net/http"

// SandboxHeaders are set on every answer of the preview route. The page is code the visitor (or a guest) wrote, on
// OpenREPL's own domain, so it must never act as OpenREPL: the sandbox (no allow-same-origin) gives it an origin of
// its own that is nothing, and the header applies it even when the page is opened as a whole tab. Cross-origin
// requests of the page to its own server need CORS, without credentials.
func SandboxHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Expose-Headers", "*")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
}
