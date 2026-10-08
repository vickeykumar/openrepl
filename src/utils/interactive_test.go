package utils

import "testing"

func TestInteractiveCommand(t *testing.T) {
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
