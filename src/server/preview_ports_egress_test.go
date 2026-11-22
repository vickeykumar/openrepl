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

// the server's own port is not a program of an admin
func TestThePreviewDoesNotListThePortOfTheServerItself(t *testing.T) {
	noteOwnPort("8080")
	defer func() { ownPortsMu.Lock(); delete(ownPorts, 8080); ownPortsMu.Unlock() }()
	got := withoutOwnPorts([]int{3000, 8080})
	if !reflect.DeepEqual(got, []int{3000}) {
		t.Fatalf("got %v", got)
	}
}
