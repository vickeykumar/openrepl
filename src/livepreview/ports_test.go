package livepreview

import (
	"reflect"
	"testing"
)

const procTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1388 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1234 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1235 1 0000000000000000 100 0 0 10 0
   2: 0100007F:C350 0100007F:1388 01 00000000:00000000 00:00000000 00000000  1000        0 1236 1 0000000000000000 100 0 0 10 0
   3: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1237 1 0000000000000000 100 0 0 10 0
   4: 00000000:1388 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1238 1 0000000000000000 100 0 0 10 0
`

func TestListeningTCPPortsFromAProcFile(t *testing.T) {
	// 0x1388 = 5000 and 0x1F90 = 8080 listen; the connected socket (state 01) and port 80 (below MinPort) do not
	if got := ParseTCP(procTCP); !reflect.DeepEqual(got, []int{5000, 8080}) {
		t.Errorf("got %v", got)
	}
	if got := ParseTCP(""); len(got) != 0 {
		t.Errorf("empty: %v", got)
	}
	if got := ParseTCP("garbage\n1 2\n"); len(got) != 0 {
		t.Errorf("garbage: %v", got)
	}
}

const procUnix = `Num       RefCount Protocol Flags    Type St Inode Path
0000000000000000: 00000002 00000000 00010000 0001 01 11111 @openrepl-ne/aaa/t/5000
0000000000000000: 00000002 00000000 00010000 0001 01 11112 @openrepl-ne/bbb/t/5000
0000000000000000: 00000003 00000000 00000000 0001 03 11113 @openrepl-ne/aaa/t/5000
0000000000000000: 00000002 00000000 00010000 0001 01 11114 @openrepl-ne/aaa/t/8080
0000000000000000: 00000002 00000000 00010000 0001 01 11115 @openrepl-ne/aaa/u/9999
0000000000000000: 00000002 00000000 00010000 0001 01 11116 @openrepl-ne/aaa/t/80
0000000000000000: 00000002 00000000 00010000 0001 01 11117 /run/some.sock
`

func TestListeningPortsOfOneWorkspaceUnderTheEmulation(t *testing.T) {
	// only the workspace aaa, only TCP (t), only listening sockets (flag 0x10000), from MinPort up
	if got := ParseUnixAbstract(procUnix, "@openrepl-ne/aaa/t/"); !reflect.DeepEqual(got, []int{5000, 8080}) {
		t.Errorf("aaa: %v", got)
	}
	if got := ParseUnixAbstract(procUnix, "@openrepl-ne/bbb/t/"); !reflect.DeepEqual(got, []int{5000}) {
		t.Errorf("bbb: %v", got)
	}
	if got := ParseUnixAbstract(procUnix, "@openrepl-ne/ccc/t/"); len(got) != 0 {
		t.Errorf("a workspace with none: %v", got)
	}
}
