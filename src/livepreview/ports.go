package livepreview

import (
	"sort"
	"strconv"
	"strings"
)

// MinPort is the lowest port a preview reaches: the ports below it belong to the machine's own services.
const MinPort = 1024

// ParseTCP reads /proc/<pid>/net/tcp or tcp6 (the text of the file) and returns the ports something listens on, from
// MinPort up, once each. A line is "sl local_address rem_address st ...", the address "IP:PORT" in hex, and state 0A is
// LISTEN.
func ParseTCP(data string) []int {
	seen := map[int]bool{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[3] != "0A" {
			continue
		}
		i := strings.LastIndexByte(f[1], ':')
		if i < 0 {
			continue
		}
		port, err := strconv.ParseInt(f[1][i+1:], 16, 32)
		if err != nil || port < MinPort || port > 65535 {
			continue
		}
		seen[int(port)] = true
	}
	return sortedPorts(seen)
}

// ParseUnixAbstract reads /proc/net/unix and returns the ports a workspace listens on under the network emulation
// (LLD 16): its listening sockets are abstract names "@<prefix><port>" (prefix: "@openrepl-ne/<workspace>/t/").
// A line is "Num RefCount Protocol Flags Type St Inode Path"; Flags has 0x10000 for a socket that listens.
func ParseUnixAbstract(data, prefix string) []int {
	seen := map[int]bool{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasPrefix(f[7], prefix) {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&0x10000 == 0 {
			continue
		}
		port, err := strconv.Atoi(f[7][len(prefix):])
		if err != nil || port < MinPort || port > 65535 {
			continue
		}
		seen[port] = true
	}
	return sortedPorts(seen)
}

func sortedPorts(seen map[int]bool) []int {
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
