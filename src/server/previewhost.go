package server

import (
	"net/http"
	"net/url"
	"strings"

	"gateway"
	"trusted"
)

// The preview host (SiteSettings.PreviewBaseURL, for example https://preview.openrepl.com): the frames of the Preview can be served from an origin of
// their own, with the addresses /<token>/f/<path> and /<token>/p/<port>/... instead of /preview/<token>/... on the site.
//
// It is a thin layer in front of the routes that exist: on that host every path is turned into the site's own form (/preview/<token>/...) and marked
// (gateway.MarkPreviewHost), then served by the same handlers, so the token, the storage, the libraries, the programs and the routing to the worker that
// runs the workspace are the ones of the site. Only the preview is reachable on that host: whatever the path, it ends up below /preview/ (a path that
// is no token ends as "the preview ended" or 404), never at the site's pages, the admin or the terminals. The site's own /preview/<token>/... stays as
// it was (old links, a worker's own port), and /preview/<token>/... is accepted on the host as well.

// sameHost says whether the Host header of a request names the host of base: the host names are compared, not the scheme or the port.
func sameHost(hostHeader, base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h, err := url.Parse("//" + hostHeader)
	return err == nil && strings.EqualFold(h.Hostname(), u.Hostname())
}

// wrapPreviewHost is the outermost layer of the site (and of a worker): see above. prefix is the path the site is mounted at ("/").
func (server *Server) wrapPreviewHost(next http.Handler, prefix string) http.Handler {
	if prefix == "" {
		prefix = "/"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := GetSiteSettings().previewBaseURL()
		if base == "" || !sameHost(r.Host, base) {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if !strings.HasPrefix(p, prefix+"preview/") {
			if p == "/" || p == "" {
				http.NotFound(w, r)
				return
			}
			r2 := *r
			u := *r.URL
			u.Path = strings.TrimSuffix(prefix, "/") + "/preview/" + strings.TrimPrefix(p, "/")
			u.RawPath = ""
			r2.URL = &u
			r = &r2
		}
		next.ServeHTTP(w, gateway.MarkPreviewHost(r))
	})
}

// onPreviewHost says whether a request arrived on the preview host: here (the mark of wrapPreviewHost), or at the gateway (the trusted header a
// worker believes only on a request that came over the tunnel).
func onPreviewHost(r *http.Request) bool {
	return gateway.IsPreviewHost(r) || (isTrusted(r) && r.Header.Get(trusted.HeaderPreviewHost) == "1")
}

// previewPath is the address of a preview below its token as the request names it: /preview/<token> on the site, /<token> on the preview host.
func previewPath(r *http.Request, token string) string {
	if onPreviewHost(r) {
		return "/" + token
	}
	return "/preview/" + token
}

// publicPath is the path of a request in the form its visitor used: the internal /preview/<token>/... turned back to /<token>/... on the preview host.
func publicPath(r *http.Request, p string) string {
	if onPreviewHost(r) {
		if i := strings.Index(p, "/preview/"); i >= 0 {
			return p[:i] + "/" + p[i+len("/preview/"):]
		}
	}
	return p
}
