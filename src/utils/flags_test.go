package utils

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigKeys(t *testing.T) {
	dir, err := ioutil.TempDir("", "configkeys")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "gotty")
	body := "mode = \"worker\"\nport = \"8080\"\npreferences {\n  font_size = 14\n}\n"
	if err := ioutil.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}

	keys := ConfigKeys(path)
	if !keys["port"] || !keys["mode"] || !keys["preferences"] {
		t.Errorf("keys = %v, want port, mode and preferences", keys)
	}
	if keys["address"] {
		t.Errorf("address is not in the file but is reported: %v", keys)
	}

	// A file that cannot be read or parsed reports nothing.
	if got := ConfigKeys(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Errorf("missing file: %v", got)
	}
	if err := ioutil.WriteFile(path, []byte("port = = ="), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ConfigKeys(path); len(got) != 0 {
		t.Errorf("broken file: %v", got)
	}
}
