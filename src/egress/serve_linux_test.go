//go:build linux

package egress

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"netemu"
)

// the emulation's name for 127.0.0.1:Port of a workspace is where the proxy listens: a connect there reaches it, and a CONNECT to a private
// address is refused
func TestWorkspaceListenerAnswersOnTheEmulationsName(t *testing.T) {
	ws := t.TempDir()
	if err := ServeWorkspace(ws); err != nil {
		t.Fatal(err)
	}
	if err := ServeWorkspace(ws); err != nil { // a second call is not an error
		t.Fatal(err)
	}
	c, err := net.DialTimeout("unix", fmt.Sprintf("@openrepl-ne/%s/t/%d", netemu.WorkspaceID(ws), Port), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "CONNECT 10.0.0.1:443 HTTP/1.1\r\nHost: 10.0.0.1:443\r\n\r\n")
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	n, _ := c.Read(buf)
	if !strings.Contains(string(buf[:n]), "403") {
		t.Errorf("a private address through the proxy answered %q", buf[:n])
	}
}
