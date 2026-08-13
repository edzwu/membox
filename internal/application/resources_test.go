package application_test

import (
	"context"
	"path/filepath"
	"testing"

	"membox/internal/application"
	"membox/internal/application/port"
	"membox/internal/bootstrap"
)

func TestCanonicalizeResourceURLRemovesTrackingAndFragment(t *testing.T) {
	first, err := application.CanonicalizeResourceURL("HTTPS://Example.COM:443/path?utm_source=inbox&b=2&a=1#section")
	if err != nil {
		t.Fatal(err)
	}
	second, err := application.CanonicalizeResourceURL("https://example.com/path?a=1&b=2")
	if err != nil {
		t.Fatal(err)
	}
	if first != "https://example.com/path?a=1&b=2" || first != second {
		t.Fatalf("canonical URLs differ: %q %q", first, second)
	}
	root, err := application.CanonicalizeResourceURL("https://example.com")
	if err != nil || root != "https://example.com/" {
		t.Fatalf("root canonicalization = %q, %v", root, err)
	}
}

func TestExtractResourceURLsDeduplicatesMarkdownAndBareURLs(t *testing.T) {
	rows := application.ExtractResourceURLs([]string{
		"- [Useful article](https://Example.com/post?utm_source=x)",
		"- again https://example.com/post#comments.",
		"- docs https://docs.example.com/a_(b)).",
	})
	if len(rows) != 2 {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	if rows[0].CanonicalURL != "https://example.com/post" || rows[0].Title != "Useful article" {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1].CanonicalURL != "https://docs.example.com/a_(b)" {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestResourceIngestIsIdempotentAndAssessmentControlsRanking(t *testing.T) {
	service, err := bootstrap.Open(filepath.Join(t.TempDir(), "membox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx := context.Background()

	first, err := service.IngestResourceLines(ctx, application.ResourceIngestOptions{
		Lines:      []string{"- [Article](https://example.com/read?utm_campaign=test)"},
		SourceFile: "inbox.md", SourceCommit: "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.IngestResourceLines(ctx, application.ResourceIngestOptions{
		Lines: []string{"duplicate https://example.com/read#part"}, SourceCommit: "def",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Inserted) != 1 || len(retry.Inserted) != 0 || len(retry.Existing) != 1 {
		t.Fatalf("unexpected ingest results: first=%+v retry=%+v", first, retry)
	}
	if retry.Existing[0].SourceCommit != "abc" || retry.Existing[0].Title != "Article" {
		t.Fatalf("first provenance/title not retained: %+v", retry.Existing[0])
	}

	other, err := service.IngestResourceLines(ctx, application.ResourceIngestOptions{
		Lines: []string{"https://example.com/core"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assessed, err := service.AssessResources(ctx, []port.ResourceAssessment{
		{ID: first.Inserted[0].ID, Priority: "M", Score: 0.8, Reason: "useful"},
		{ID: other.Inserted[0].ID, Priority: "H", Score: 0.7, Reason: "primary source"},
	})
	if err != nil || len(assessed) != 2 {
		t.Fatalf("assessment = %+v, %v", assessed, err)
	}
	listed, err := service.ListResources(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].Priority != "H" || listed[0].CanonicalURL != "https://example.com/core" {
		t.Fatalf("ranking = %+v", listed)
	}
}
