package server

import (
	"reflect"
	"testing"

	"egress"
)

// the proxy's own listener is the server's, not a program of the user: Preview does not offer it
func TestThePreviewDoesNotListTheProxyPort(t *testing.T) {
	got := withoutPort([]int{3000, egress.Port, 8080}, egress.Port)
	if !reflect.DeepEqual(got, []int{3000, 8080}) {
		t.Fatalf("got %v", got)
	}
	if got := withoutPort(nil, egress.Port); len(got) != 0 || got == nil {
		t.Fatalf("an empty list stays an empty list, not nil: %#v", got)
	}
}
