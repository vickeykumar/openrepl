package containers

import "testing"

func TestAForkMayJoinOnlyAParentThatWasStartedHere(t *testing.T) {
	saved := HasCAPSysAdmin
	defer func() { HasCAPSysAdmin = saved }()
	HasCAPSysAdmin = true // a host where the fallback for unprivileged containers does not apply
	const pid = 987654
	if IsProcess(pid) {
		t.Fatal("a process nobody started was accepted")
	}
	// cgroups are not available here (Containers is empty), as on a cgroup v2 host
	TrackProcess(pid)
	if !IsProcess(pid) {
		t.Fatal("a running REPL was refused because it has no cgroup")
	}
	UntrackProcess(pid)
	if IsProcess(pid) {
		t.Fatal("a REPL that ended was still accepted")
	}
}

func TestTheWrappersTrackAndForget(t *testing.T) {
	saved := HasCAPSysAdmin
	defer func() { HasCAPSysAdmin = saved }()
	HasCAPSysAdmin = true
	const pid = 987655
	AddProcesstoNewSubCgroup("nosuchcommand", pid, false) // no container for it: only the bookkeeping here
	if !IsProcess(pid) {
		t.Fatal("not tracked after the REPL started")
	}
	DeleteProcessFromSubCgroup("nosuchcommand", pid)
	if IsProcess(pid) {
		t.Fatal("still tracked after the REPL ended")
	}
}
