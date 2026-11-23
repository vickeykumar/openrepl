package gateway

import (
	"context"
	"net/http"
)

// A Preview request that arrived on the preview host (SiteSettings.PreviewBaseURL: /<token>/... there). The server rewrites its path to the site's own form
// (/preview/<token>/...) before it routes it; this mark says it came by the host, so that the addresses a program's pages are rewritten to, and a
// worker (which gets the mark as a trusted header), stay in the host's form.
type previewHostKey struct{}

// MarkPreviewHost marks r as a request of the preview host.
func MarkPreviewHost(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), previewHostKey{}, true))
}

// IsPreviewHost says whether r arrived on the preview host.
func IsPreviewHost(r *http.Request) bool {
	v, _ := r.Context().Value(previewHostKey{}).(bool)
	return v
}
