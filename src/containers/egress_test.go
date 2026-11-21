package containers

import "testing"

func TestEgressIsForSignedInNonAdminsOnlyAndOnlyWhenOn(t *testing.T) {
	t.Setenv("OPENREPL_EGRESS", "")
	if EgressWanted("bash", true, false) {
		t.Error("off by default")
	}
	t.Setenv("OPENREPL_EGRESS", "public")
	if EgressWanted("bash", false, false) {
		t.Error("a guest gets nothing")
	}
	if EgressWanted("bash", true, true) {
		t.Error("an admin reaches the network directly: no proxy")
	}
}
