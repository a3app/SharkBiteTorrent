package search

import "testing"

func TestDedupe(t *testing.T) {
	rs := dedupe([]Result{
		{Title: "A", Size: 1, Indexer: "x"},
		{Title: "A", Size: 1, Indexer: "x"}, // exact repeat
		{Title: "A", Size: 1, Indexer: "y"}, // same release, other site: kept
		{Title: "A", Size: 2, Indexer: "x"}, // different size: kept
	})
	if len(rs) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(rs), rs)
	}
}
