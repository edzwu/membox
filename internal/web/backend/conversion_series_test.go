package backend

import (
	"strings"
	"testing"
)

func TestConversionIdentityAndKind(t *testing.T) {
	cases := []struct {
		filename string
		id       string
		kind     string
		label    string
	}{
		{
			"just-for-fun-pdf-01a004095c0c70e687738be6b34259ce.md",
			"01a004095c0c70e687738be6b34259ce",
			"index",
			"just-for-fun",
		},
		{
			"just-for-fun-pdf-01a004095c0c70e687738be6b34259ce-chapter-012.md",
			"01a004095c0c70e687738be6b34259ce",
			"chapter",
			"just-for-fun ch.12",
		},
		{
			"fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea-part-introduction.md",
			"01a001106cb479db952c02e705f8c0ea",
			"part",
			"fooled-by-randomness intro",
		},
		{
			"ordinary-note.md",
			"",
			"other",
			"ordinary-note",
		},
	}
	for _, tc := range cases {
		id, ok := conversionIdentity(tc.filename)
		if tc.id == "" {
			if ok {
				t.Fatalf("%s: expected no identity, got %q", tc.filename, id)
			}
		} else if !ok || id != tc.id {
			t.Fatalf("%s: identity=%q ok=%v want %q", tc.filename, id, ok, tc.id)
		}
		if kind := conversionItemKind(tc.filename); kind != tc.kind {
			t.Fatalf("%s: kind=%q want %q", tc.filename, kind, tc.kind)
		}
		if label := conversionDisplayLabel(tc.filename, ""); label != tc.label {
			t.Fatalf("%s: label=%q want %q", tc.filename, label, tc.label)
		}
	}
}

func TestTOCFilenameOrderAndSeriesLess(t *testing.T) {
	index := strings.Join([]string{
		"# book",
		"1. [Intro](book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-part-introduction.md)",
		"2. [Ch1](book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-001.md)",
		"3. [Ch2](book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-002.md)",
	}, "\n")
	order := tocFilenameOrder(index)
	if order["book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-002.md"] != 2 {
		t.Fatalf("toc order=%v", order)
	}
	items := []conversionSeriesItem{
		{Filename: "book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-002.md", Kind: "chapter"},
		{Filename: "book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.md", Kind: "index"},
		{Filename: "book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-001.md", Kind: "chapter"},
		{Filename: "book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-part-introduction.md", Kind: "part"},
	}
	// Sort like production.
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if seriesLess(items[j], items[i], order) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	want := []string{
		"book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.md",
		"book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-part-introduction.md",
		"book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-001.md",
		"book-pdf-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-chapter-002.md",
	}
	for i, name := range want {
		if items[i].Filename != name {
			t.Fatalf("sorted[%d]=%q want %q (%v)", i, items[i].Filename, name, items)
		}
	}
}
