package catalog

import (
	"strings"
	"testing"
	"time"
)

func testObservation(pathID IndexedPathID, relative, hash, key string) Observation {
	location, err := NewLocation(pathID, relative)
	if err != nil {
		panic(err)
	}
	return Observation{Location: location, FileKey: FileKey(key), Title: "Title", MTime: 1, Size: 4, SHA256: hash, Body: []byte("body")}
}

func TestDocument_ID002_ContentChangePreservesID(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := testObservation(1, "a.md", strings.Repeat("a", 64), "1:1")
	document, err := NewDocument("019-test", first, now)
	if err != nil {
		t.Fatal(err)
	}
	second := testObservation(1, "a.md", strings.Repeat("b", 64), "1:1")
	second.Body, second.Size, second.MTime = []byte("changed"), 7, 2
	if err := document.Observe(second, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if document.ID != "019-test" {
		t.Fatalf("ID changed to %q", document.ID)
	}
	if document.Index.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("hash was not updated")
	}
}

func TestDocument_RelocateCountsAsSourceModification(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	document, err := NewDocument("019-test", testObservation(1, "a.md", strings.Repeat("a", 64), "1:1"), now)
	if err != nil {
		t.Fatal(err)
	}
	modifiedAt := now.Add(time.Hour)
	relocated := testObservation(1, "renamed.md", strings.Repeat("a", 64), "1:1")
	if err := document.Relocate(relocated.Location, relocated, modifiedAt); err != nil {
		t.Fatal(err)
	}
	if document.Location.RelativePath != "renamed.md" || !document.Index.SourceUpdatedAt.Equal(modifiedAt) {
		t.Fatalf("relocate did not update source modification time: %+v", document)
	}
}

func TestDocument_ID003_MissingAndUntrackedPreserveID(t *testing.T) {
	now := time.Now().UTC()
	document, err := NewDocument("019-test", testObservation(1, "a.md", strings.Repeat("a", 64), "1:1"), now)
	if err != nil {
		t.Fatal(err)
	}
	document.MarkMissing(now.Add(time.Second))
	if document.ID != "019-test" || document.Status != DocumentMissing {
		t.Fatalf("unexpected missing document: %+v", document)
	}
	document.MarkUntracked(now.Add(2 * time.Second))
	if document.ID != "019-test" || document.Status != DocumentUntracked {
		t.Fatalf("unexpected untracked document: %+v", document)
	}
}
