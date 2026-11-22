// Package egress is the way out of a sandbox (LLD 16 "Egress"): a forward proxy that a REPL without a network of its own reaches through its
// private loopback. The REPL gets HTTP_PROXY and HTTPS_PROXY, so pip, npm, curl and git over https work, while everything that ignores them still
// has no route. The proxy resolves the destination itself and refuses what must never be reached from a sandbox.
package egress

import (
	"net"
	"os"
	"strings"
)

// Port is the loopback port the REPL sees the proxy on.
const Port = 3128

// EnvEgress is OPENREPL_EGRESS: off (the default) or public.
const EnvEgress = "OPENREPL_EGRESS"

// Mode is "off" or "public": anything else (and nothing) is off.
func Mode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvEgress))) {
	case "public", "on", "1", "true", "yes":
		return "public"
	}
	return "off"
}

// SiteSwitch is the admin's switch in Site settings (the server sets it; on a worker it is the gateway's, handed over with the site rules). The
// proxy is on when it or the environment variable says so.
var SiteSwitch func() bool

// Enabled says whether the proxy is switched on for the server.
func Enabled() bool { return Mode() == "public" || (SiteSwitch != nil && SiteSwitch()) }

var deniedNets = mustNets(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "64:ff9b::/96", "100::/64", "fc00::/7", "fe80::/10", "ff00::/8", "2001:db8::/32",
)

func mustNets(list ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range list {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// Denied says whether an address must not be reached from a sandbox: the host itself, private and link-local ranges (the cloud metadata
// address 169.254.169.254 among them), multicast and the other special ranges. An IPv4 address written as IPv6 is judged as IPv4.
func Denied(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range deniedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// DeniedPort says whether a destination port is refused whatever the address: outbound mail (25).
func DeniedPort(port int) bool { return port == 25 }
