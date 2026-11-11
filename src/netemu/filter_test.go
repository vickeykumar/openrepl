//go:build linux

package netemu

import (
	"syscall"
	"testing"
)

// run interprets the classic BPF program the way the kernel does, for the few instructions it uses.
func run(prog []sockFilter, nr int32, arch uint32, args [6]uint64) uint32 {
	data := make([]byte, 64)
	put32 := func(off int, v uint32) {
		data[off], data[off+1], data[off+2], data[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	put32(0, uint32(nr))
	put32(4, arch)
	for i, a := range args {
		put32(16+8*i, uint32(a))
		put32(20+8*i, uint32(a>>32))
	}
	var acc uint32
	for pc := 0; pc < len(prog); pc++ {
		ins := prog[pc]
		switch ins.code {
		case 0x20: // ld abs
			k := int(ins.k)
			acc = uint32(data[k]) | uint32(data[k+1])<<8 | uint32(data[k+2])<<16 | uint32(data[k+3])<<24
		case 0x15: // jeq
			if acc == ins.k {
				pc += int(ins.jt)
			} else {
				pc += int(ins.jf)
			}
		case 0x06:
			return ins.k
		default:
			panic("an instruction the test does not know")
		}
	}
	panic("the program ended without a verdict")
}

func TestTheFilterNotifiesOnlyWhereAnAddressIsInvolved(t *testing.T) {
	prog := buildFilter(rules())
	arch := auditArch()
	call := func(nr int, args ...uint64) uint32 {
		var a [6]uint64
		copy(a[:], args)
		return run(prog, int32(nr), arch, a)
	}
	notify, allow := uint32(retUserNotif), uint32(retAllow)
	for _, c := range []struct {
		name string
		nr   int
		args []uint64
		want uint32
	}{
		{"socket", syscall.SYS_SOCKET, nil, notify},
		{"bind", syscall.SYS_BIND, nil, notify},
		{"connect", syscall.SYS_CONNECT, nil, notify},
		{"getsockname", syscall.SYS_GETSOCKNAME, nil, notify},
		{"sendmsg", syscall.SYS_SENDMSG, nil, notify},
		{"recvmsg", syscall.SYS_RECVMSG, nil, notify},
		{"sendmmsg", sysSendmmsg, nil, notify},
		{"recvmmsg", sysRecvmmsg, nil, notify},
		{"accept4 without an address", syscall.SYS_ACCEPT4, []uint64{3, 0}, allow},
		{"accept4 with an address", syscall.SYS_ACCEPT4, []uint64{3, 0x7ffd0000}, notify},
		{"accept4 with an address above 4 GB", syscall.SYS_ACCEPT4, []uint64{3, 1 << 40}, notify},
		{"sendto without an address", syscall.SYS_SENDTO, []uint64{3, 1, 1, 0, 0, 0}, allow},
		{"sendto with an address", syscall.SYS_SENDTO, []uint64{3, 1, 1, 0, 0x1000, 16}, notify},
		{"recvfrom without an address", syscall.SYS_RECVFROM, []uint64{3, 1, 1, 0, 0, 0}, allow},
		{"recvfrom with an address", syscall.SYS_RECVFROM, []uint64{3, 1, 1, 0, 0x1000, 16}, notify},
		{"setsockopt at the socket level", syscall.SYS_SETSOCKOPT, []uint64{3, solSocket}, allow},
		{"setsockopt for TCP", syscall.SYS_SETSOCKOPT, []uint64{3, 6}, notify},
		{"getsockopt for IPv6", syscall.SYS_GETSOCKOPT, []uint64{3, 41}, notify},
		{"read", syscall.SYS_READ, nil, allow},
		{"write", syscall.SYS_WRITE, nil, allow},
		{"epoll_pwait", syscall.SYS_EPOLL_PWAIT, nil, allow},
		{"io_uring_setup", sysIoUringSetup, nil, retErrnoBase | uint32(syscall.ENOSYS)},
		{"io_uring_enter", sysIoUringEnter, nil, retErrnoBase | uint32(syscall.ENOSYS)},
	} {
		if got := call(c.nr, c.args...); got != c.want {
			t.Errorf("%s: verdict %#x, want %#x", c.name, got, c.want)
		}
	}
	// another architecture's calls (32-bit) are killed: they could carry an address the filter cannot read
	if got := run(prog, int32(syscall.SYS_SOCKET), 0x40000003, [6]uint64{}); got != retKillProcess {
		t.Errorf("a 32-bit call: verdict %#x", got)
	}
}
