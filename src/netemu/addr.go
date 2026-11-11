// Package netemu gives every workspace its own private network, without privilege (LLD 16).
//
// A REPL that has no kernel namespaces of its own (a host that refuses privileged containers, such as
// Render) would share one network with every other REPL: the same port could be used by one user only,
// and users could reach each other's servers. Here each AF_INET/AF_INET6 socket of such a REPL is
// really an AF_UNIX socket bound to an ABSTRACT name that holds the workspace and the port
// ("@openrepl-ne/<workspace>/<t|u>/<port>"). The kernel then does what it is good at (listen, accept,
// epoll, buffers, "connection refused", "address in use") and a supervisor in the server only answers the
// system calls that carry an address. The REPL can name only its own workspace's ports because the
// supervisor writes the prefix, and only the local machine exists: anything else is "network unreachable".
//
// The REPL starts through landlock-helper, which installs a seccomp filter (the notifications go to the
// server over a socket) and then becomes the REPL. See session.go and filter.go.
package netemu

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

const (
	afUnix  = 1
	afInet  = 2
	afInet6 = 10

	// ephemeral ports are chosen where Linux chooses them
	ephemeralFirst = 32768
	ephemeralLast  = 60999

	namePrefix = "@openrepl-ne/"
)

// Addr is an address as a REPL names it.
type Addr struct {
	Family uint16
	Port   uint16
	Any    bool // the wildcard (0.0.0.0 or ::)
}

// Parse reads a sockaddr_in or sockaddr_in6. Only the local machine exists: 0.0.0.0, 127.x.x.x, ::, ::1 and
// ::ffff:127.x.x.x (or ::ffff:0.0.0.0). Anything else is outside the workspace: ENETUNREACH. A family that
// is not IP is EAFNOSUPPORT.
func Parse(b []byte) (Addr, syscall.Errno) {
	if len(b) < 2 {
		return Addr{}, syscall.EINVAL
	}
	switch binary.LittleEndian.Uint16(b[:2]) {
	case afInet:
		if len(b) < 16 {
			return Addr{}, syscall.EINVAL
		}
		a := Addr{Family: afInet, Port: binary.BigEndian.Uint16(b[2:4])}
		ip := b[4:8]
		switch {
		case ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] == 0:
			a.Any = true
		case ip[0] == 127:
		default:
			return a, syscall.ENETUNREACH
		}
		return a, 0
	case afInet6:
		if len(b) < 28 {
			return Addr{}, syscall.EINVAL
		}
		a := Addr{Family: afInet6, Port: binary.BigEndian.Uint16(b[2:4])}
		ip := b[8:24]
		if allZero(ip) {
			a.Any = true
			return a, 0
		}
		if allZero(ip[:15]) && ip[15] == 1 {
			return a, 0 // ::1
		}
		if allZero(ip[:10]) && ip[10] == 0xff && ip[11] == 0xff && (ip[12] == 127 || allZero(ip[12:16])) {
			a.Any = allZero(ip[12:16]) // ::ffff:0.0.0.0 is the IPv4 wildcard
			return a, 0                // ::ffff:127.x.x.x
		}
		return a, syscall.ENETUNREACH
	}
	return Addr{}, syscall.EAFNOSUPPORT
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// Build makes the sockaddr a REPL expects for a socket of its family: the loopback (or the wildcard). For a
// peer (remote) of an IPv6 socket it is the IPv4-mapped loopback, which both families understand.
func Build(family, port uint16, any, remote bool) []byte {
	if family == afInet6 {
		b := make([]byte, 28)
		binary.LittleEndian.PutUint16(b[0:2], afInet6)
		binary.BigEndian.PutUint16(b[2:4], port)
		switch {
		case any:
		case remote:
			b[18], b[19] = 0xff, 0xff
			b[20] = 127
			b[23] = 1
		default:
			b[23] = 1
		}
		return b
	}
	b := make([]byte, 16)
	binary.LittleEndian.PutUint16(b[0:2], afInet)
	binary.BigEndian.PutUint16(b[2:4], port)
	if !any {
		b[4] = 127
		b[7] = 1
	}
	return b
}

// Name is the abstract name of a port in a workspace; proto is "t" (stream) or "u" (datagram). The leading
// "@" stands for the NUL byte of an abstract name, as Go writes it.
func Name(workspace, proto string, port uint16) string {
	return fmt.Sprintf("%s%s/%s/%d", namePrefix, workspace, proto, port)
}

// PortOf reads the port out of a name made by Name, when it belongs to the workspace.
func PortOf(workspace, name string) (uint16, bool) {
	if !strings.HasPrefix(name, namePrefix) {
		return 0, false
	}
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[1] != workspace || (parts[2] != "t" && parts[2] != "u") {
		return 0, false
	}
	p, err := strconv.Atoi(parts[3])
	if err != nil || p < 0 || p > 65535 {
		return 0, false
	}
	return uint16(p), true
}
