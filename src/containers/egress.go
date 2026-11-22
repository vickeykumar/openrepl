package containers

import (
	"log"
	"time"

	"egress"
)

// EgressWanted says whether a REPL gets the proxy (LLD 16 "Egress"): the proxy is on, the person is signed in and not an admin, and the REPL has
// no direct network because it runs in a namespace of its own (the Pi) or in the private network of the emulation (Render).
func EgressWanted(command string, signedIn, admin bool) bool {
	if !egress.Allowed(signedIn, admin) {
		return false
	}
	_, ownNS := Containers[command]
	return ownNS || NetEmuActive()
}

// StartEgress makes the proxy reachable for a REPL that was just started (process pid, in workspace). With a namespace of its own the listener is
// opened inside it (it needs the loopback to be up, so it is tried for a few seconds); otherwise on the emulation's name for the workspace.
func StartEgress(command, workspace string, pid int) {
	if _, ownNS := Containers[command]; ownNS {
		go func() {
			var err error
			for i := 0; i < 25; i++ {
				if err = egress.ServeInNamespace(pid); err == nil {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			logEgress("egress: no proxy in the namespace of process", pid, err)
		}()
		return
	}
	if err := egress.ServeWorkspace(workspace); err != nil {
		logEgress("egress: no proxy for the workspace", workspace, err)
	}
}

func logEgress(what string, who interface{}, err error) {
	log.Printf("WARNING: %s %v: %v", what, who, err)
}
