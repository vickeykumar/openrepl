package containers

import (
	"os"
	"testing"
)

func TestNamespacesWeAreInAlreadyAreNotEntered(t *testing.T) {
	if _, err := os.Stat("/proc/self/ns/user"); err != nil {
		t.Skip("no /proc/self/ns here")
	}
	// a process in the same namespaces as this one (this process itself): nothing to enter
	if flags := differingNamespaceFlags(os.Getpid()); len(flags) != 0 {
		t.Fatalf("flags for our own namespaces: %v", flags)
	}
	// a process that cannot be looked at: all of them, as before
	if flags := differingNamespaceFlags(0x7ffffff0); len(flags) != len(ns_flags) {
		t.Fatalf("flags for a process that is not there: %v", flags)
	}
}
