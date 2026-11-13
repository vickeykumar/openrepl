package gateway

import "testing"

// The calls that name the workspace of a session go to the node that runs it, not to the site with the cookie alone.
func TestTheCallsOfThePreviewAreExecutionBound(t *testing.T) {
	for rel, want := range map[string]bool{
		"ws":              true,
		"ws_filebrowser":  true,
		"upload_file":     true,
		"preview_token":   true,
		"preview_ports":   true,
		"preview_cookies": true,
		"preview":         false, // the frame's address carries its own token
		"preview/x/f/a":   false,
		"about.html":      false,
		"preview_ports_x": false,
	} {
		if got := IsExecutionBound(rel); got != want {
			t.Errorf("IsExecutionBound(%q) = %v, want %v", rel, got, want)
		}
	}
}
