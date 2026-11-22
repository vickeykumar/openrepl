package utils

import "testing"

func TestInteractiveCommand(t *testing.T) {
	ts, ok := InteractiveCommand("ts-node")
	wantTS := []string{"ts-node", "--transpile-only", "--compiler-options", `{"module":"NodeNext","moduleResolution":"NodeNext"}`}
	if !ok || len(ts) != len(wantTS) {
		t.Fatalf("ts-node -> %v %v", ts, ok)
	}
	for i := range wantTS {
		if ts[i] != wantTS[i] {
			t.Fatalf("ts-node args = %v, want %v", ts, wantTS)
		}
	}
	ts[0] = "changed"
	if again, _ := InteractiveCommand("ts-node"); again[0] != "ts-node" {
		t.Error("the caller could change the TypeScript command")
	}

	alt, ok := InteractiveCommand("java")
	if !ok || alt[0] != "jshell" {
		t.Fatalf("java -> %v %v", alt, ok)
	}
	alt[0] = "changed"
	if again, _ := InteractiveCommand("java"); again[0] != "jshell" {
		t.Error("the caller could change the table")
	}
	for _, c := range []string{"python", "cling", "", "jshell"} {
		if _, ok := InteractiveCommand(c); ok {
			t.Errorf("%q has an interactive program of its own", c)
		}
	}
}
