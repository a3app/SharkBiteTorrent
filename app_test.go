package main

import (
	"path/filepath"
	"testing"
)

func TestExistingAncestor(t *testing.T) {
	root := t.TempDir()
	if got := existingAncestor(root); got != root {
		t.Errorf("existing dir: got %q", got)
	}
	missing := filepath.Join(root, "Torrents", "deeper")
	if got := existingAncestor(missing); got != root {
		t.Errorf("deleted folder: got %q, want %q", got, root)
	}
	if got := existingAncestor(""); got != "" {
		t.Errorf("empty: got %q", got)
	}
}
