package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"wsync"
)

func TestWorkspaceTreeUsesDistinctAbsoluteIDsForNestedEntries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("descriptor-backed workspace tree is Linux-only")
	}
	base := t.TempDir()
	root := filepath.Join(base, "user-home")
	if err := os.MkdirAll(filepath.Join(root, "New node", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "New node", "nested", "test.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	home, err := wsync.OpenHome(base, filepath.Base(root), false)
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()

	tree, err := workspaceTree(home, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Id != root {
		t.Fatalf("root ID = %q, want %q", tree.Id, root)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("root children = %d, want 1", len(tree.Children))
	}
	node := tree.Children[0]
	if node.Id != filepath.Join(root, "New node") {
		t.Fatalf("nested directory ID = %q", node.Id)
	}
	if len(node.Children) != 1 || node.Children[0].Id != filepath.Join(root, "New node", "nested") {
		t.Fatalf("nested tree IDs are not workspace-relative absolute paths: %+v", node)
	}
	file := node.Children[0].Children
	if len(file) != 1 || file[0].Id != filepath.Join(root, "New node", "nested", "test.go") {
		t.Fatalf("file ID is not a workspace-relative absolute path: %+v", node.Children[0])
	}
}
