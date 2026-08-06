package filesystem

import "testing"

func TestExtractTitleUsesFrontmatterAndIgnoresFencedOutput(t *testing.T) {
	body := []byte("---\ntitle: \"EzRaft: Build a Distributed KV Store in 100 Lines\"\n---\n\n## Article\n\n```console\n# null\n```\n")
	if got, want := extractTitle(body, "/notes/null.md"), "EzRaft: Build a Distributed KV Store in 100 Lines"; got != want {
		t.Fatalf("extractTitle() = %q, want %q", got, want)
	}
}

func TestExtractTitleSkipsFencedFakeHeading(t *testing.T) {
	body := []byte("~~~shell\n# null\n~~~\n\n# Real title\n")
	if got, want := extractTitle(body, "/notes/fallback.md"), "Real title"; got != want {
		t.Fatalf("extractTitle() = %q, want %q", got, want)
	}
}

func TestExtractTitleFallsBackToFilename(t *testing.T) {
	body := []byte("---\ntitle: null\n---\n\n## No H1\n")
	if got, want := extractTitle(body, "/notes/use-this-name.markdown"), "use-this-name"; got != want {
		t.Fatalf("extractTitle() = %q, want %q", got, want)
	}
}
