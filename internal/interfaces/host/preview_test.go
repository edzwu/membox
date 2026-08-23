package host

import (
	"strings"
	"testing"
)

func TestDocumentPreview_PrefersSummaryMarkerOverExcerpt(t *testing.T) {
	body := []byte(`---
kind: "summary"
---

> Long original excerpt about raw pointers and love poetry.
>
> More excerpt paragraphs that must not appear in the card.

Its declaration doesn’t indicate whether it points to a single object or to an array.

**总结：**

原始指针难以管理资源，应优先使用智能指针。
`)
	got := DocumentPreview(body, 220)
	if got != "原始指针难以管理资源，应优先使用智能指针。" {
		t.Fatalf("preview=%q", got)
	}
	for _, bad := range []string{"Poets", "declaration", "excerpt", "love poetry"} {
		if strings.Contains(got, bad) {
			t.Fatalf("preview leaked excerpt %q: %q", bad, got)
		}
	}
}

func TestDocumentPreview_SkipsBlockquotesWithoutMarker(t *testing.T) {
	body := []byte(`> quoted selection

My actual note body goes here.
`)
	got := DocumentPreview(body, 220)
	if got != "My actual note body goes here." {
		t.Fatalf("preview=%q", got)
	}
}
