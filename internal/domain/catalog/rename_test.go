package catalog

import (
	"strings"
	"testing"
	"time"
)

func documentForRename(t *testing.T, id, relative, hash, key string) *Document {
	t.Helper()
	document, err := NewDocument(DocumentID(id), testObservation(1, relative, hash, key), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestRenameReconciler_REN001_UniqueFileKeyWins(t *testing.T) {
	old := documentForRename(t, "old", "a.md", strings.Repeat("a", 64), "1:9")
	incoming := testObservation(1, "b.md", strings.Repeat("b", 64), "1:9")
	result := ReconcileRenames([]*Document{old}, []Observation{incoming})
	if len(result.Relocations) != 1 || result.Relocations[0].Reason != "file-key" {
		t.Fatalf("unexpected reconciliation: %+v", result)
	}
	if len(result.New) != 0 || len(result.Missing) != 0 {
		t.Fatalf("matched entries remained unmatched")
	}
}

func TestRenameReconciler_REN002_UniqueExactHash(t *testing.T) {
	hash := strings.Repeat("a", 64)
	old := documentForRename(t, "old", "a.md", hash, "")
	incoming := testObservation(1, "b.md", hash, "")
	result := ReconcileRenames([]*Document{old}, []Observation{incoming})
	if len(result.Relocations) != 1 || result.Relocations[0].Reason != "exact-hash" {
		t.Fatalf("unexpected reconciliation: %+v", result)
	}
}

func TestRenameReconciler_REN003_AmbiguousHashDoesNotMerge(t *testing.T) {
	hash := strings.Repeat("a", 64)
	old := documentForRename(t, "old", "a.md", hash, "")
	incoming := []Observation{testObservation(1, "b.md", hash, ""), testObservation(1, "c.md", hash, "")}
	result := ReconcileRenames([]*Document{old}, incoming)
	if len(result.Relocations) != 0 {
		t.Fatalf("ambiguous files were merged: %+v", result.Relocations)
	}
	if len(result.New) != 2 || len(result.Missing) != 1 || len(result.Candidates) != 2 {
		t.Fatalf("unexpected ambiguous result: %+v", result)
	}
}
