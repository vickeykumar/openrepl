package netemu

import (
	"encoding/binary"
	"syscall"
	"testing"
)

func sockaddrIn(ip [4]byte, port uint16) []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint16(b[0:2], afInet)
	binary.BigEndian.PutUint16(b[2:4], port)
	copy(b[4:8], ip[:])
	return b
}

func sockaddrIn6(ip [16]byte, port uint16) []byte {
	b := make([]byte, 28)
	binary.LittleEndian.PutUint16(b[0:2], afInet6)
	binary.BigEndian.PutUint16(b[2:4], port)
	copy(b[8:24], ip[:])
	return b
}

func TestOnlyTheLocalMachineExists(t *testing.T) {
	mapped := [16]byte{10: 0xff, 11: 0xff, 12: 127, 15: 1}
	mappedAny := [16]byte{10: 0xff, 11: 0xff}
	mappedOut := [16]byte{10: 0xff, 11: 0xff, 12: 8, 13: 8, 14: 8, 15: 8}
	loop6 := [16]byte{15: 1}
	other6 := [16]byte{0: 0x20, 1: 0x01, 15: 1}
	cases := []struct {
		name string
		sa   []byte
		any  bool
		err  syscall.Errno
		port uint16
	}{
		{"127.0.0.1", sockaddrIn([4]byte{127, 0, 0, 1}, 8080), false, 0, 8080},
		{"another 127 address", sockaddrIn([4]byte{127, 9, 9, 9}, 1), false, 0, 1},
		{"0.0.0.0", sockaddrIn([4]byte{}, 80), true, 0, 80},
		{"an outside address", sockaddrIn([4]byte{1, 1, 1, 1}, 80), false, syscall.ENETUNREACH, 80},
		{"a private network address", sockaddrIn([4]byte{10, 0, 0, 5}, 80), false, syscall.ENETUNREACH, 80},
		{"::", sockaddrIn6([16]byte{}, 80), true, 0, 80},
		{"::1", sockaddrIn6(loop6, 81), false, 0, 81},
		{"::ffff:127.0.0.1", sockaddrIn6(mapped, 82), false, 0, 82},
		{"::ffff:0.0.0.0", sockaddrIn6(mappedAny, 83), true, 0, 83},
		{"::ffff:8.8.8.8", sockaddrIn6(mappedOut, 84), false, syscall.ENETUNREACH, 84},
		{"an outside IPv6 address", sockaddrIn6(other6, 85), false, syscall.ENETUNREACH, 85},
	}
	for _, c := range cases {
		a, e := Parse(c.sa)
		if e != c.err {
			t.Errorf("%s: error %v, want %v", c.name, e, c.err)
			continue
		}
		if e == 0 && (a.Port != c.port || a.Any != c.any) {
			t.Errorf("%s: got port %d any %v", c.name, a.Port, a.Any)
		}
	}
	if _, e := Parse([]byte{1, 0, 0, 0}); e != syscall.EAFNOSUPPORT {
		t.Errorf("a unix address: %v", e)
	}
	if _, e := Parse([]byte{2}); e != syscall.EINVAL {
		t.Errorf("a short address: %v", e)
	}
	if _, e := Parse(make([]byte, 10)[:10]); e == 0 {
		t.Errorf("an empty family was accepted")
	}
}

func TestAddressesAreBuiltAsTheFamilyExpectsThem(t *testing.T) {
	v4 := Build(afInet, 8080, false, false)
	if len(v4) != 16 || v4[4] != 127 || v4[7] != 1 || binary.BigEndian.Uint16(v4[2:4]) != 8080 {
		t.Errorf("IPv4 loopback: %v", v4)
	}
	if a := Build(afInet, 1, true, false); a[4] != 0 || a[7] != 0 {
		t.Errorf("IPv4 wildcard: %v", a)
	}
	v6 := Build(afInet6, 9, false, false)
	if len(v6) != 28 || v6[23] != 1 || v6[18] != 0 {
		t.Errorf("::1: %v", v6)
	}
	peer := Build(afInet6, 9, false, true)
	if peer[18] != 0xff || peer[19] != 0xff || peer[20] != 127 || peer[23] != 1 {
		t.Errorf("the peer of an IPv6 socket is IPv4-mapped: %v", peer)
	}
	// what is built is understood by Parse
	for _, sa := range [][]byte{v4, v6, peer} {
		if _, e := Parse(sa); e != 0 {
			t.Errorf("Parse(Build) = %v for %v", e, sa)
		}
	}
}

func TestANameHoldsTheWorkspaceAndThePort(t *testing.T) {
	n := Name("abc123", "t", 8080)
	if n != "@openrepl-ne/abc123/t/8080" {
		t.Fatal(n)
	}
	if p, ok := PortOf("abc123", n); !ok || p != 8080 {
		t.Errorf("PortOf = %d %v", p, ok)
	}
	// another workspace's name is not one of ours; nor is a malformed one
	for _, bad := range []string{
		Name("other", "t", 8080), "@openrepl-ne/abc123/x/80", "@openrepl-ne/abc123/t/99999", "@openrepl-ne/abc123/t/-1",
		"@other/abc123/t/80", "@openrepl-ne/abc123/t", "/tmp/sock", "",
	} {
		if _, ok := PortOf("abc123", bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}
