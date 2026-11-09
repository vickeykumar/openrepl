package persist

import (
	"os"
	"testing"
)

// The local copies of the remote databases (replica.go) go to a folder of the tests, not to /opt/gotty.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "persist-replica-")
	if err != nil {
		panic(err)
	}
	replicaDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
