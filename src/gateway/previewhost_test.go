package gateway

import (
	"net/http/httptest"
	"testing"

	"trusted"
)

// A worker gets the mark of the preview host only from the gateway: the header a client sent is dropped, the mark of the request is forwarded
func TestTheMarkOfThePreviewHostReachesAWorkerAndAClientsCopyDoesNot(t *testing.T) {
	b := &RemoteBackend{cfg: RemoteConfig{ID: "w1"}}
	forged := httptest.NewRequest("GET", "/preview/T/f/x", nil)
	forged.Header.Set(trusted.HeaderPreviewHost, "1")
	b.direct(forged)
	if forged.Header.Get(trusted.HeaderPreviewHost) != "" {
		t.Error("a client's copy of the header reached the worker")
	}
	marked := MarkPreviewHost(httptest.NewRequest("GET", "/preview/T/f/x", nil))
	b.direct(marked)
	if marked.Header.Get(trusted.HeaderPreviewHost) != "1" {
		t.Error("the mark did not reach the worker")
	}
	if IsPreviewHost(httptest.NewRequest("GET", "/", nil)) {
		t.Error("an unmarked request is not of the preview host")
	}
}
