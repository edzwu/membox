package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"membox"
)

func TestNameFilterShortUUIDMatchesOnlyDocumentID(t *testing.T) {
	// Typing the 4-char short ID shown in the tree must not also match every
	// converted Markdown file that embeds the same hex tail in its filename.
	const pdfID = "019ffe54-a513-7da8-bfac-0ae403223ccf"
	items := documentItems([]membox.DocumentView{
		{ID: pdfID, Title: "AI Agents", Path: "/pdfs/book.pdf", RelativePath: "book.pdf", MediaType: "application/pdf"},
		{ID: "index", Path: "/notes/pdf-019ffe54a5137da8bfac0ae403223ccf.md", RelativePath: "pdf-019ffe54a5137da8bfac0ae403223ccf.md", MediaType: "text/markdown"},
		{ID: "chapter", Path: "/notes/pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md", RelativePath: "pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md", MediaType: "text/markdown"},
		{ID: "other-3ccf-tail", Title: "unrelated", Path: "/tmp/other.md", RelativePath: "other.md"},
	})
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = items
	model.searchMode = searchModeName
	model.inputVisible = true
	model.input.SetValue("3ccf")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != pdfID {
		got := make([]string, 0, len(model.filtered))
		for _, it := range model.filtered {
			got = append(got, it.document.ID)
		}
		t.Fatalf("short UUID filter 3ccf = %v, want only %s", got, pdfID)
	}
	// Non-hex queries still match title/path.
	model.input.SetValue("Agents")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != pdfID {
		t.Fatalf("title filter Agents = %+v", model.filtered)
	}
}

func TestDocumentItemsMarksPDFWhenStableConvertedIndexExists(t *testing.T) {
	const pdfID = "019ffe54-a513-7da8-bfac-0ae403223ccf"
	documents := []membox.DocumentView{
		{ID: pdfID, Title: "AI Agents", Path: "/pdfs/book.pdf", RelativePath: "book.pdf", MediaType: "application/pdf"},
		{ID: "index", Path: "/notes/pdf-019ffe54a5137da8bfac0ae403223ccf.md", RelativePath: "pdf-019ffe54a5137da8bfac0ae403223ccf.md", MediaType: "text/markdown"},
		{ID: "chapter", Path: "/notes/pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md", RelativePath: "pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md", MediaType: "text/markdown"},
	}
	items := documentItems(documents)
	if !items[0].pdfConverted {
		t.Fatal("PDF was not marked converted when its stable index exists")
	}
	if items[1].pdfConverted || items[2].pdfConverted {
		t.Fatalf("Markdown documents received PDF conversion markers: %+v", items)
	}

	// After the logical-id boundary, DocumentView.ID is a short suffix — still must match.
	shortDocs := []membox.DocumentView{
		{ID: "223ccf", Title: "AI Agents", Path: "/pdfs/book.pdf", RelativePath: "book.pdf", MediaType: "application/pdf"},
		{ID: "index", Path: "/notes/book-pdf-019ffe54a5137da8bfac0ae403223ccf.md", RelativePath: "book-pdf-019ffe54a5137da8bfac0ae403223ccf.md", MediaType: "text/markdown"},
	}
	shortItems := documentItems(shortDocs)
	if !shortItems[0].pdfConverted {
		t.Fatal("PDF with logical short id was not marked converted")
	}

	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 24
	model.items, model.filtered = items, items
	model.resize()
	if view := model.treePreviewView(); !strings.Contains(view, "◆") {
		t.Fatalf("tree does not show converted PDF marker: %q", view)
	}
}

func TestConvertedTreeLabelStripsIdentityAndShortensSuffixes(t *testing.T) {
	cases := map[string]string{
		"fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea.md":                   "fooled-by-randomness",
		"fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea-chapter-014.md":       "fooled-by-randomness ch.14",
		"fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea-part-introduction.md": "fooled-by-randomness intro",
		"fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea-part-afterword.md":    "fooled-by-randomness afterword",
		"just-for-fun--pdf-01a004095c0c70e687738be6b34259ce-chapter-000.md":              "just-for-fun ch.0",
		"labuladong的算法小抄官方完整版-pdf-01a00084ee897c54b45fee07fa0a9119.md":                   "labuladong的算法小抄官方完整版",
		"ordinary-note.md":                  "ordinary-note.md",
		"my-notes-pdf-but-not-generated.md": "my-notes-pdf-but-not-generated.md",
	}
	for filename, want := range cases {
		if got := convertedTreeLabel(filename); got != want {
			t.Fatalf("convertedTreeLabel(%q)=%q, want %q", filename, got, want)
		}
	}
}

func TestTreeItemLabelPrefersTitleForUntitledMarkdown(t *testing.T) {
	untitled := item{
		document: membox.DocumentView{
			ID: "01a01cf6-ceaf-7173-966c-04073d03c9b4", Title: "bitmask 技巧",
			Path: "/tmp/untitled.md", MediaType: "text/markdown",
		},
		filename: "untitled.md",
	}
	if got := treeItemLabel(untitled); got != "bitmask 技巧" {
		t.Fatalf("untitled tree label = %q, want catalog title", got)
	}
	named := item{
		document: membox.DocumentView{
			ID: "doc-named", Title: "Other Title", Path: "/tmp/bitmask-技巧.md", MediaType: "text/markdown",
		},
		filename: "bitmask-技巧.md",
	}
	if got := treeItemLabel(named); got != "bitmask-技巧.md" {
		t.Fatalf("named markdown must stay filesystem-authoritative: %q", got)
	}
}

func TestConvertedPDFIDRejectsChapterAndMalformedNames(t *testing.T) {
	if id, ok := convertedPDFID("AI-Agents中文版-pdf-019ffe54a5137da8bfac0ae403223ccf.md"); !ok || id != "019ffe54-a513-7da8-bfac-0ae403223ccf" {
		t.Fatalf("readable converted filename resolved as (%q,%v)", id, ok)
	}
	// Legacy double-dash names are still recognized; LastIndex lands on the
	// dash pair right before the identity.
	if id, ok := convertedPDFID("AI-Agents中文版--pdf-019ffe54a5137da8bfac0ae403223ccf.md"); !ok || id != "019ffe54-a513-7da8-bfac-0ae403223ccf" {
		t.Fatalf("double-dash converted filename resolved as (%q,%v)", id, ok)
	}
	if id, ok := convertedPDFID("fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea.md"); !ok || id != "01a00110-6cb4-79db-952c-02e705f8c0ea" {
		t.Fatalf("single-dash readable converted filename resolved as (%q,%v)", id, ok)
	}
	for _, filename := range []string{
		"AI-Agents-pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md",
		"AI-Agents--pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md",
		"fooled-by-randomness-pdf-01a001106cb479db952c02e705f8c0ea-chapter-001.md",
		"pdf-019ffe54a5137da8bfac0ae403223ccf-chapter-001.md",
		"pdf-not-a-uuid.md",
		"ordinary.md",
	} {
		if id, ok := convertedPDFID(filename); ok {
			t.Fatalf("convertedPDFID(%q)=%q, want rejected", filename, id)
		}
	}
}

func TestModel_DefaultShowsTreeAndPreview(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md"}})
	model.refreshFilter()
	model.resize()
	view := model.View()
	if !strings.Contains(view, "Alpha") {
		t.Fatalf("default view does not contain tree item: %q", view)
	}
	if !strings.Contains(view, "Loading documents") && !strings.Contains(view, "membox") {
		t.Fatalf("default view does not contain preview placeholder: %q", view)
	}
}

func TestModel_DefaultShowsUUIDAndOriginalFilename(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 140, 24
	model.items = documentItems([]membox.DocumentView{{
		ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "# title in document",
		Path:         "/tmp/Online_normalizer_calculation_for_softmax_2083425580487839744.report.md",
		RelativePath: "Online_normalizer_calculation_for_softmax_2083425580487839744.report.md",
	}})
	model.refreshFilter()
	model.resize()
	view := model.View()
	if !strings.Contains(view, "dabf") {
		t.Fatalf("tree does not contain UUID: %q", view)
	}
	if !strings.Contains(view, "Online_normaliz") || !strings.Contains(view, "softmax_20") {
		t.Fatalf("tree does not contain recognizable original filename: %q", view)
	}
	firstLine := strings.Split(view, "\n")[0]
	if strings.Contains(firstLine, "# title in document") {
		t.Fatalf("tree replaced filename with Markdown title: %q", firstLine)
	}
}

func TestModel_PDFTreeShowsFileSize(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 24
	model.mediaScope = mediaScopePDF
	model.items = documentItems([]membox.DocumentView{{
		ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Paper", Path: "/tmp/paper.pdf",
		MediaType: "application/pdf", Size: 8 << 20,
	}})
	model.refreshFilter()
	if view := model.treePreviewView(); !strings.Contains(view, "8.0 MiB") {
		t.Fatalf("PDF tree does not show size column: %q", view)
	}
}

func TestModel_TreeShowsDatesWhenSpaceAllows(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 180, 24
	created := time.Date(2024, 1, 3, 12, 0, 0, 0, time.Local)
	updated := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	model.items = documentItems([]membox.DocumentView{{
		ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md",
		CreatedAt: created, UpdatedAt: updated,
	}})
	model.refreshFilter()
	view := model.treePreviewView()
	if !strings.Contains(view, "2024-01-03  2026-08-02") {
		t.Fatalf("tree does not contain document source dates: %q", view)
	}
}

func TestModel_FocusedDocumentShowsDetailsPanel(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	updated := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	model.items = documentItems([]membox.DocumentView{{
		ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md",
		UpdatedAt: updated,
	}})
	model.refreshFilter()
	model.detailsVisible = true
	view := model.View()
	if !strings.Contains(view, "019fbe56-64c3-7e3c-861d-4da66742dabf") {
		t.Fatalf("details missing full UUID: %q", view)
	}
	if !strings.Contains(view, "/tmp/alpha.md") {
		t.Fatalf("details missing disk path: %q", view)
	}
	if !strings.Contains(view, "2026-08-02") {
		t.Fatalf("details missing document modified date: %q", view)
	}
}

func TestModel_SingleSpaceTogglesDetailsPane(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md"}})
	model.refreshFilter()
	if model.detailsVisible {
		t.Fatal("details pane should be hidden by default")
	}
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	if !model.detailsVisible {
		t.Fatal("space did not show details pane")
	}
	updated, _ = model.Update(space)
	model = updated.(Model)
	if model.detailsVisible {
		t.Fatal("second space did not hide details pane")
	}
}

func TestModel_DetailsToggleKeepsSelectedItemVisible(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 20
	var docs []membox.DocumentView
	for i := 0; i < 30; i++ {
		docs = append(docs, membox.DocumentView{ID: fmt.Sprintf("019fbe56-64c3-7e3c-861d-4da66742%04d", i), Title: fmt.Sprintf("Doc %02d", i), Path: fmt.Sprintf("/tmp/doc-%02d.md", i), RelativePath: fmt.Sprintf("doc-%02d.md", i)})
	}
	model.items = documentItems(docs)
	model.refreshFilter()
	// select the first item, scroll to top
	model.selected = 0
	model.scrollTop = 0
	// open details pane (shrinks visible area)
	model.detailsVisible = true
	model.keepSelectionVisible()
	// selected item 0 must still be visible (scrollTop should stay 0)
	if model.scrollTop != 0 {
		t.Fatalf("opening details pushed first item off screen: scrollTop=%d", model.scrollTop)
	}
	view := model.treePreviewView()
	if !strings.Contains(view, "doc-00.md") {
		t.Fatalf("first item not visible after details toggle: %q", view[:200])
	}
}

func TestModel_SlashOpensFilterInput(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	slash := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
	updated, _ := model.Update(slash)
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeSearch {
		t.Fatalf("/ did not open filter input: visible=%v mode=%s", model.inputVisible, model.inputMode)
	}
	if model.detailsVisible {
		t.Fatal("/ should not toggle details")
	}
}

func TestModel_SpaceTogglesDetailsAndSlashOpensFilter(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	if !model.detailsVisible || model.inputVisible {
		t.Fatalf("space should toggle details only: details=%v input=%v", model.detailsVisible, model.inputVisible)
	}
	slash := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}}
	updated, _ = model.Update(slash)
	model = updated.(Model)
	if !model.inputVisible || !model.inputActive || model.inputMode != inputModeSearch {
		t.Fatalf("/ did not show filter input")
	}
	// Spaces remain available for multi-word filter text once the input is open.
	updated, _ = model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	if !model.inputVisible || !model.inputActive || model.input.Value() != "  " {
		t.Fatalf("spaces in active filter changed input state: visible=%v active=%v value=%q", model.inputVisible, model.inputActive, model.input.Value())
	}
}

func TestModel_DefaultSortIsNewestFirstAndSTriggersSummarize(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha", Path: "/tmp/alpha.md", UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)},
		{ID: "charlie", Path: "/tmp/charlie.md", UpdatedAt: time.Time{}},
		{ID: "bravo", Path: "/tmp/bravo.md", UpdatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)},
	})
	model.refreshFilter()
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"bravo", "alpha", "charlie"}) {
		t.Fatalf("default order is not newest first: %v", got)
	}
	model.selected = 0
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if command == nil {
		t.Fatal("s did not schedule a summarize command")
	}
	if !model.summarizing["bravo"] {
		t.Fatalf("summarize in-flight flag missing: %v", model.summarizing)
	}
	updated, _ = model.Update(command())
	model = updated.(Model)
	if model.summarizing["bravo"] {
		t.Fatal("summarize in-flight flag not cleared")
	}
	if model.filtered[0].document.Summary != "fake summary" {
		t.Fatalf("summary not applied: %+v", model.filtered[0].document)
	}
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"bravo", "alpha", "charlie"}) {
		t.Fatalf("summarize disturbed newest-first order: %v", got)
	}
}

func TestModel_PinToggleMovesDocumentToTopAndShowsMarker(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha", Path: "/tmp/alpha.md"},
		{ID: "beta", Path: "/tmp/beta.md"},
	})
	model.refreshFilter()
	model.selected = 1

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	model = updated.(Model)
	if !model.loading || command == nil {
		t.Fatal("p did not schedule pin toggle")
	}
	message := togglePinCmd(context.Background(), app, "beta")().(pinMsg)
	updated, _ = model.Update(message)
	model = updated.(Model)
	if !model.filtered[0].document.Pinned || model.filtered[0].document.ID != "beta" || model.selected != 0 {
		t.Fatalf("pinned document did not move to top: selected=%d filtered=%+v", model.selected, model.filtered)
	}
	if view := model.treePreviewView(); !strings.Contains(view, "▌") {
		t.Fatalf("pinned document has no marker: %q", view)
	}

	message = togglePinCmd(context.Background(), app, "beta")().(pinMsg)
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.filtered[0].document.ID != "alpha" || model.filtered[1].document.Pinned {
		t.Fatalf("unfixed document did not return to name order: %+v", model.filtered)
	}
	if view := model.treePreviewView(); strings.Contains(view, "▌") {
		t.Fatalf("unfixed document still has marker: %q", view)
	}
}

func TestModel_PinnedRowStaysVisibleWhenDetailsReduceTreeHeight(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 10
	documents := []membox.DocumentView{{ID: "pinned", Path: "/tmp/pinned.md", Pinned: true}}
	for index := 0; index < 20; index++ {
		documents = append(documents, membox.DocumentView{ID: fmt.Sprintf("doc-%02d", index), Path: fmt.Sprintf("/tmp/doc-%02d.md", index)})
	}
	model.items = documentItems(documents)
	model.refreshFilter()
	model.selected = len(model.filtered) - 1
	model.keepSelectionVisible()
	if view := model.treePreviewView(); !strings.Contains(view, "pinned.md") || !strings.Contains(view, "doc-19.md") {
		t.Fatalf("pinned or selected row missing before details: %q", view)
	}

	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	if !model.detailsVisible {
		t.Fatal("space did not open details")
	}
	view := model.View()
	if lines := strings.Split(strings.TrimRight(view, "\n"), "\n"); len(lines) != model.height {
		t.Fatalf("details view height=%d, want terminal height=%d: %q", len(lines), model.height, view)
	}
	if !strings.Contains(view, "pinned.md") || !strings.Contains(view, "doc-19.md") {
		t.Fatalf("details height hid pinned or selected row from full view: %q", view)
	}
	indices := model.treeVisibleIndices()
	if len(indices) == 0 || indices[0] != 0 {
		t.Fatalf("pinned row is not reserved at top: %v", indices)
	}
}

func TestModel_PreviewShowsBodyNotIndexSummary(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "with-summary", Path: "/tmp/a.md", Summary: "要点总结"},
		{ID: "without", Path: "/tmp/b.md"},
	})
	model.refreshFilter()

	// Index summaries stay out of the preview pane; body is rendered instead.
	updated, _ := model.Update(previewMsg{documentID: "with-summary", content: "# 原文 body"})
	model = updated.(Model)
	if model.rawContent != "# 原文 body" {
		t.Fatalf("preview must keep the Markdown body, got %q", model.rawContent)
	}
	if !strings.Contains(model.preview.View(), "原文") {
		t.Fatalf("rendered preview missing body: %q", model.preview.View())
	}

	updated, _ = model.Update(previewMsg{documentID: "without", content: "# body only"})
	model = updated.(Model)
	if model.rawContent != "# body only" {
		t.Fatalf("preview without summary must show the body, got %q", model.rawContent)
	}
}

func TestModel_PinnedDocumentsStayFirstInNewestSort(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "old-pinned", Path: "/tmp/old.md", Pinned: true, UpdatedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)},
		{ID: "new", Path: "/tmp/new.md", UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)},
	})
	model.refreshFilter()
	if model.filtered[0].document.ID != "old-pinned" {
		t.Fatalf("newest sort displaced pinned document: %+v", model.filtered)
	}
}

func TestModel_HomeAndEndMoveToTreeBoundaries(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 8
	for index := 0; index < 20; index++ {
		model.items = append(model.items, documentItems([]membox.DocumentView{{
			ID: fmt.Sprintf("doc-%02d", index), Path: fmt.Sprintf("/tmp/doc-%02d.md", index),
		}})...)
	}
	model.refreshFilter()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if model.selected != len(model.filtered)-1 || model.scrollTop == 0 {
		t.Fatalf("End did not focus final tree row: selected=%d top=%d", model.selected, model.scrollTop)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.selected != 0 || model.scrollTop != 0 {
		t.Fatalf("Home did not focus first tree row: selected=%d top=%d", model.selected, model.scrollTop)
	}

	// While the filter is focused, Home/End stay off the tree — esc first.
	model.selected = 3
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeSearch
	model.input.Focus()
	model.input.SetValue("doc")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if model.selected != 3 || model.input.Value() != "doc" {
		t.Fatalf("End with active filter moved tree: selected=%d input=%q", model.selected, model.input.Value())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.selected != 3 || model.input.Value() != "doc" {
		t.Fatalf("Home with active filter moved tree: selected=%d input=%q", model.selected, model.input.Value())
	}
}

func TestModel_DeleteConfirmationDeletesFocusedFile(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{{ID: "doc-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if !model.deleteConfirm || !strings.Contains(model.View(), "Move alpha.md to trash?") {
		t.Fatalf("delete confirmation missing: %q", model.View())
	}
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = updated.(Model)
	if !model.loading || command == nil {
		t.Fatal("delete was not scheduled")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.deleteConfirm || !strings.HasPrefix(model.statusMessage, "Moved deleted.md to trash • restore: mm trash restore") {
		t.Fatalf("delete did not complete: confirm=%v status=%q", model.deleteConfirm, model.statusMessage)
	}
}

func TestModel_DeleteKeepsFocusOnAdjacentFile(t *testing.T) {
	docs := []membox.DocumentView{
		{ID: "pinned", Title: "Pinned", Path: "/tmp/pinned.md", Pinned: true, UpdatedAt: time.Date(2026, 1, 4, 0, 0, 0, 0, time.Local)},
		{ID: "before", Title: "Before", Path: "/tmp/before.md", UpdatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.Local)},
		{ID: "deleted", Title: "Deleted", Path: "/tmp/deleted.md", UpdatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)},
		{ID: "after", Title: "After", Path: "/tmp/after.md", UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)},
	}
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems(docs)
	model.refreshFilter()
	model.selected = 2 // deleted; its next row is after

	updated, _ := model.Update(deleteResultMsg{documentID: "deleted", path: "/tmp/deleted.md"})
	model = updated.(Model)
	selected, ok := model.selectedDocument()
	if !ok || selected.ID != "after" {
		t.Fatalf("delete jumped away from next neighbor: selected=%+v index=%d", selected, model.selected)
	}

	// The following async list reload must preserve that adjacent selection,
	// rather than falling back to the first pinned row.
	remaining := []membox.DocumentView{docs[0], docs[1], docs[3]}
	updated, _ = model.Update(documentsMsg{sequence: model.listSequence, documents: remaining})
	model = updated.(Model)
	selected, ok = model.selectedDocument()
	if !ok || selected.ID != "after" {
		t.Fatalf("reload lost adjacent selection: selected=%+v index=%d", selected, model.selected)
	}

	// If the deleted row is last, the closest valid neighbor is the row above.
	model.items = documentItems(remaining)
	model.refreshFilter()
	model.selected = len(model.filtered) - 1
	updated, _ = model.Update(deleteResultMsg{documentID: "after", path: "/tmp/after.md"})
	model = updated.(Model)
	selected, ok = model.selectedDocument()
	if !ok || selected.ID != "before" {
		t.Fatalf("deleting last row did not select previous neighbor: selected=%+v index=%d", selected, model.selected)
	}
}

func TestModel_DeleteConfirmationCanBeCanceled(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{{ID: "doc-alpha", Title: "Alpha", Path: "/tmp/alpha.md"}})
	model.refreshFilter()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	model = updated.(Model)
	if model.deleteConfirm || model.statusMessage != "Delete canceled" {
		t.Fatalf("delete cancel failed: confirm=%v status=%q", model.deleteConfirm, model.statusMessage)
	}
}

func TestModel_CtrlPCyclesMediaScope(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "doc-md", Title: "Markdown", Path: "/tmp/note.md", MediaType: "text/markdown"},
		{ID: "doc-pdf", Title: "PDF", Path: "/tmp/paper.pdf", MediaType: "application/pdf"},
		{ID: "doc-image", Title: "Image", Path: "/tmp/diagram.png", MediaType: "image/png"},
	})
	model.refreshFilter()
	for index, expected := range []struct {
		scope string
		count int
		badge string
	}{
		{mediaScopeMarkdown, 1, "MARKDOWN ONLY"},
		{mediaScopePDF, 1, "PDF ONLY"},
		{mediaScopeImage, 1, "IMAGES ONLY"},
		{mediaScopeAll, 3, ""},
	} {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
		model = updated.(Model)
		if model.mediaScope != expected.scope || len(model.filtered) != expected.count {
			t.Fatalf("cycle %d: scope=%s count=%d, want %s/%d", index, model.mediaScope, len(model.filtered), expected.scope, expected.count)
		}
		if badge := model.mediaScopeBadge(); expected.badge != "" && !strings.Contains(badge, expected.badge) {
			t.Fatalf("cycle %d badge=%q, want %q", index, badge, expected.badge)
		}
	}
}

func TestModel_PDFPreviewNeverReadsBinaryIntoTerminal(t *testing.T) {
	app := &fakeApp{
		previewPath: "/tmp/paper.pdf",
		readBody:    []byte("%PDF-1.4\nstream\x1b[2Jbinary"),
	}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 24
	model.resize()
	model.items = documentItems([]membox.DocumentView{{
		ID: "doc-pdf", Title: "Paper", Path: app.previewPath, MediaType: "application/pdf",
	}})
	model.refreshFilter()

	// loadPreview debounces; flush the tick then the real load cmd.
	debounced := model.loadPreview()
	updated, loadCmd := model.Update(debounced())
	model = updated.(Model)
	if loadCmd == nil {
		t.Fatal("expected preview load command after debounce")
	}
	message := loadCmd()
	preview, ok := message.(previewMsg)
	if !ok {
		t.Fatalf("loadPreview returned %T, want previewMsg", message)
	}
	if app.readCount != 0 {
		t.Fatalf("PDF preview read %d binary bodies, want 0", app.readCount)
	}
	if preview.documentID != "doc-pdf" || !strings.Contains(preview.content, "PDF document") || strings.Contains(preview.content, "%PDF-") {
		t.Fatalf("unsafe PDF preview: %+v", preview)
	}

	updated, _ = model.Update(preview)
	model = updated.(Model)
	view := model.treePreviewView()
	if !strings.Contains(view, "paper.pdf") || !strings.Contains(view, "PDF document") {
		t.Fatalf("PDF preview should preserve tree and show safe placeholder: %q", view)
	}
	if strings.Contains(view, "%PDF-") || strings.Contains(view, "\x1b[2J") {
		t.Fatalf("PDF binary leaked into terminal view: %q", view)
	}
}

func TestModel_PreviewCacheServesSecondLoad(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.resize()
	model.items = documentItems([]membox.DocumentView{{
		ID: "doc-cache", Title: "Cached", Path: "/tmp/cached.md", RelativePath: "cached.md", Size: 42,
	}})
	model.refreshFilter()
	model.putPreviewCache("doc-cache", 42, model.preview.Width, "# body\n", "body")

	cmd := model.loadPreview()
	if cmd != nil {
		t.Fatal("cache hit should not schedule a disk load")
	}
	if model.rawDocumentID != "doc-cache" || model.renderedPreview != "body" {
		t.Fatalf("cache was not applied: id=%q rendered=%q", model.rawDocumentID, model.renderedPreview)
	}
}

func TestModel_FilterKeystrokeDoesNotReloadSamePreview(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.resize()
	model.items = documentItems([]membox.DocumentView{{
		ID: "doc-keep", Title: "Keep", Path: "/tmp/keep.md", RelativePath: "keep.md",
	}})
	model.refreshFilter()
	model.rawDocumentID = "doc-keep"
	model.rawContent = "hello world"
	model.renderedPreview = "hello world"
	model.renderedWidth = model.preview.Width
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeSearch
	model.input.Focus()
	model.input.SetValue("hel")

	cmd := model.filterChanged(nil)
	if cmd != nil {
		// A batch that only carries a nil input command is fine; a loadPreview tick is not.
		msg := cmd()
		if _, ok := msg.(previewDebounceMsg); ok {
			t.Fatal("filter keystroke re-triggered preview load for the same document")
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, part := range batch {
				if part == nil {
					continue
				}
				if _, ok := part().(previewDebounceMsg); ok {
					t.Fatal("filter keystroke re-triggered preview load for the same document")
				}
			}
		}
	}
}

func TestModel_CtrlPCyclesMediaScopeWithoutChangingInputMode(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	if model.inputMode != inputModeCmd || !model.inputVisible {
		t.Fatalf("ctrl+p changed/closed command input: mode=%s visible=%v", model.inputMode, model.inputVisible)
	}
	if model.mediaScope != mediaScopeMarkdown {
		t.Fatalf("ctrl+p did not change media scope: %s", model.mediaScope)
	}
}

func TestModel_DoubleSpaceAlwaysOpensFilterInput(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	model = updated.(Model)
	if model.inputMode != inputModeAgent {
		t.Fatalf("ctrl+k did not reach agent mode: %s", model.inputMode)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.inputVisible || model.inputMode != inputModeAgent {
		t.Fatalf("esc did not preserve hidden agent mode: visible=%v mode=%s", model.inputVisible, model.inputMode)
	}
	// "/" always reopens the FILTER input, never the last-closed mode.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeSearch {
		t.Fatalf("/ should open the filter input: visible=%v mode=%s", model.inputVisible, model.inputMode)
	}
}

func TestModel_CommandPaletteProgressiveDisclosure(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{{ID: "doc-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeCmd || model.cmdMenuVisible {
		t.Fatalf(": did not open quiet command input: visible=%v mode=%s menu=%v", model.inputVisible, model.inputMode, model.cmdMenuVisible)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if !model.cmdMenuVisible || len(model.cmdSuggestions) == 0 || model.cmdSuggestions[0].Value != "doc" {
		t.Fatalf("first tab did not disclose top-level commands: %+v", model.cmdSuggestions)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.input.Value() != "topic " || model.cmdMenuVisible {
		t.Fatalf("topic was not inserted progressively: input=%q menu=%v", model.input.Value(), model.cmdMenuVisible)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if len(model.cmdSuggestions) == 0 || model.cmdSuggestions[0].Value != "create" {
		t.Fatalf("second tab did not disclose topic verbs: %+v", model.cmdSuggestions)
	}
	for model.cmdSuggestions[model.cmdSelected].Value != "add" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.input.Value() != "topic add " {
		t.Fatalf("topic add was not inserted: %q", model.input.Value())
	}
}

func TestModel_SlashClearStillClearsFiltersInSearchMode(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.dateFilters = []dateFilter{{Label: "+2026"}}
	model.textFilters = []textFilter{{Value: "alpha", Mode: searchModeName}}
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("/clear")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.dateFilters) != 0 || len(model.textFilters) != 0 {
		t.Fatalf("/clear did not clear filters: dates=%+v text=%+v", model.dateFilters, model.textFilters)
	}
}

func TestModel_CommandExecutionClearsInputAndHistoryNavigates(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeCmd
	model.input.Focus()
	model.input.SetValue("topic list")
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("topic list did not schedule")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.input.Value() != "" || len(model.commandHistory) != 1 || model.commandHistory[0] != "topic list" {
		t.Fatalf("command input/history not updated: input=%q history=%+v", model.input.Value(), model.commandHistory)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input.Value() != "topic list" {
		t.Fatalf("up did not recall command: %q", model.input.Value())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.input.Value() != "" {
		t.Fatalf("down did not return to empty draft: %q", model.input.Value())
	}
}

func TestModel_FilterInputHistoryLocksTreeNavigation(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbe56-64c3-7e3c-861d-000000000001", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019fbe56-64c3-7e3c-861d-000000000002", Title: "Beta", Path: "/tmp/beta.md", RelativePath: "beta.md"},
	})
	model.refreshFilter()
	model.selected = 0
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeSearch
	model.input.Focus()
	model.historyIndex = len(model.filterHistory)

	// Pin a filter tag so it lands in history, then reopen filter for history nav.
	model.input.SetValue("alpha")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.filterHistory) != 1 || model.filterHistory[0] != "alpha" {
		t.Fatalf("enter did not record filter history: %+v", model.filterHistory)
	}
	if model.selected != 0 {
		t.Fatalf("enter moved tree selection: %d", model.selected)
	}
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.historyIndex = len(model.filterHistory)

	// ↑ recalls history; selection must stay put while the filter is focused.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input.Value() != "alpha" {
		t.Fatalf("up did not recall filter: %q", model.input.Value())
	}
	if model.selected != 0 {
		t.Fatalf("up moved tree selection while filter focused: %d", model.selected)
	}

	// ↓ returns to the empty draft; pgdown/home stay no-ops for the tree.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.input.Value() != "" {
		t.Fatalf("down did not clear filter draft: %q", model.input.Value())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.selected != 0 {
		t.Fatalf("paging keys moved tree while filter focused: %d", model.selected)
	}

	// Esc unlocks the tree; ↓ then moves selection.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.inputActive || model.inputVisible {
		t.Fatalf("esc did not blur filter: active=%v visible=%v", model.inputActive, model.inputVisible)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.selected != 1 {
		t.Fatalf("down after esc did not move tree: %d", model.selected)
	}
}

func TestModel_CommandFuzzyArgumentSuggestionsIncludeUUIDAndFilename(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{{ID: "870abcdef-1234", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()
	var matches []commandSuggestion
	for _, suggestion := range model.commandArgumentSuggestions([]string{"link", "list"}, 2, "870a") {
		if suggestion.Value != "@selected" {
			matches = append(matches, suggestion)
		}
	}
	if len(matches) != 1 || matches[0].Value != "870abcdef-1234" || !strings.Contains(matches[0].Description, "Alpha") {
		t.Fatalf("fuzzy suggestions=%+v", matches)
	}
}

func TestModel_CommandTabShowsFuzzyDocumentChoices(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{
		{ID: "9c4fabcdef-1111", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "9c4fabcdef-2222", Title: "Alpha Review", Path: "/tmp/alpha-review.md", RelativePath: "alpha-review.md"},
	})
	model.refreshFilter()
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeCmd
	model.input.Focus()
	model.input.SetValue("link add 9c4f")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if !model.cmdMenuVisible || len(model.cmdSuggestions) != 3 {
		t.Fatalf("fuzzy menu did not show conflicts: visible=%v suggestions=%+v", model.cmdMenuVisible, model.cmdSuggestions)
	}
	view := model.inputView()
	if !strings.Contains(view, "9c4f") || !strings.Contains(view, "alpha.md") || !strings.Contains(view, "alpha-review.md") {
		t.Fatalf("menu does not show uuid and filename: %q", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.input.Value() != "link add 9c4fabcdef-1111 " {
		t.Fatalf("selected conflict was not inserted: %q", model.input.Value())
	}
}

func TestModel_AgentModeUsesBadgeAndPlaceholderError(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	model = updated.(Model)
	if model.inputMode != inputModeAgent || !strings.Contains(model.modeBadge(), "AGENT") {
		t.Fatalf("ctrl+k did not open agent mode: mode=%s badge=%q", model.inputMode, model.modeBadge())
	}
	if !strings.Contains(model.input.Placeholder, "agent") {
		t.Fatalf("agent mode still shows placeholder %q", model.input.Placeholder)
	}
	if strings.Contains(model.input.Placeholder, "filter documents") {
		t.Fatalf("agent mode should not reuse the filter placeholder: %q", model.input.Placeholder)
	}
	cmdModel := New(context.Background(), &fakeApp{}, fakeLauncher{})
	updated, _ = cmdModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	cmdModel = updated.(Model)
	if cmdModel.inputMode != inputModeCmd || !strings.Contains(cmdModel.input.Placeholder, "command") || strings.Contains(cmdModel.input.Placeholder, "filter documents") {
		t.Fatalf(": should open cmd mode with a command placeholder: mode=%s placeholder=%q", cmdModel.inputMode, cmdModel.input.Placeholder)
	}
	model.input.SetValue("summarize current document")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.filterErr == nil || !strings.Contains(model.filterErr.Error(), "not ready") {
		t.Fatalf("agent not-ready error missing: %v", model.filterErr)
	}
}

func TestModel_LinkListShowsGraphFocusCardsByDirection(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 30
	focus := membox.DocumentView{ID: "focus", Title: "Focus", Path: "/tmp/focus.md", Summary: "focus summary"}
	outgoing := membox.DocumentView{ID: "out", Title: "Outgoing", Path: "/tmp/out.md", Summary: "out summary"}
	incoming := membox.DocumentView{ID: "in", Title: "Incoming", Path: "/tmp/in.md", Summary: "in summary"}
	updated, _ := model.Update(graphFocusMsg{documentID: focus.ID, cards: []membox.DocumentView{focus, outgoing, incoming}, incoming: 1})
	model = updated.(Model)
	view := model.View()
	if model.viewMode != viewBoard || !strings.Contains(view, "● focus") || !strings.Contains(view, "├─ →") || !strings.Contains(view, "└─ ←") {
		t.Fatalf("graph focus board missing thread markers: %q", view)
	}
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") {
		t.Fatalf("graph list should not use card borders: %q", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = updated.(Model)
	if model.graphFocusID != "" || model.viewMode != viewTree {
		t.Fatalf("q did not leave graph focus: focus=%s mode=%s", model.graphFocusID, model.viewMode)
	}
}

func TestGraphFocusCmdExpandsPDFConversionChapters(t *testing.T) {
	const pdfID = "019ffe54-a513-7da8-bfac-0ae403223ccf"
	const indexID = "index-doc"
	index := membox.DocumentView{
		ID: indexID, Title: "TOC", Path: "/notes/book-pdf-019ffe54a5137da8bfac0ae403223ccf.md",
		RelativePath: "book-pdf-019ffe54a5137da8bfac0ae403223ccf.md", MediaType: "text/markdown",
	}
	ch1 := membox.DocumentView{ID: "ch1", Title: "Chapter 1", Path: "/notes/ch1.md", MediaType: "text/markdown"}
	ch2 := membox.DocumentView{ID: "ch2", Title: "Chapter 2", Path: "/notes/ch2.md", MediaType: "text/markdown"}
	app := &fakeApp{
		graph: membox.DocumentGraphView{
			Focus:    membox.DocumentView{ID: pdfID, Title: "Book PDF", Path: "/pdfs/book.pdf", MediaType: "application/pdf"},
			Outgoing: []membox.DocumentView{index},
		},
		graphs: map[string]membox.DocumentGraphView{
			indexID: {
				Focus:    index,
				Outgoing: []membox.DocumentView{ch1, ch2},
			},
		},
	}
	msg := graphFocusCmd(context.Background(), app, pdfID, "thread")().(graphFocusMsg)
	if msg.err != nil {
		t.Fatalf("graphFocusCmd err: %v", msg.err)
	}
	ids := make([]string, 0, len(msg.cards))
	for _, card := range msg.cards {
		ids = append(ids, card.ID)
	}
	want := []string{pdfID, indexID, "ch1", "ch2"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("PDF link list cards=%v want %v", ids, want)
	}
}

func TestModel_LinkListResultEscReturnsToFileTree(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 30
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeCmd
	model.input.Focus()
	model.input.SetValue("link list focus")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("link list did not schedule a result")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[len(batch)-1]()
	}
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.graphFocusID != "focus" || model.viewMode != viewBoard {
		t.Fatalf("link list did not open its result view: focus=%q mode=%q", model.graphFocusID, model.viewMode)
	}
	if model.inputVisible || model.inputActive {
		t.Fatalf("result view was stacked under command input: visible=%v active=%v", model.inputVisible, model.inputActive)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.graphFocusID != "" || model.graphCards != nil || model.graphLayout != "" || model.viewMode != viewTree {
		t.Fatalf("esc did not return to the file tree: focus=%q cards=%d layout=%q mode=%q", model.graphFocusID, len(model.graphCards), model.graphLayout, model.viewMode)
	}
}

func TestModel_LinkGraphDrawsStarCanvasAndNavigatesColumns(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 140, 40
	focus := membox.DocumentView{ID: "focus", Title: "Focus", Path: "/tmp/focus.md", Summary: "focus summary"}
	outgoing := membox.DocumentView{ID: "out", Title: "Outgoing", Path: "/tmp/out.md", Summary: "out summary"}
	incoming := membox.DocumentView{ID: "in", Title: "Incoming", Path: "/tmp/in.md", Summary: "in summary"}
	updated, _ := model.Update(graphFocusMsg{
		documentID: focus.ID,
		cards:      []membox.DocumentView{focus, outgoing, incoming},
		incoming:   1,
		layout:     "star",
		topics:     []membox.TopicView{{ID: "t1", Name: "research"}},
	})
	model = updated.(Model)
	if model.graphLayout != "star" || model.viewMode != viewBoard {
		t.Fatalf("star graph did not enter board mode: layout=%s mode=%s", model.graphLayout, model.viewMode)
	}
	if model.graphSelected != 1 {
		t.Fatalf("star selection should start on the focus card: selected=%d", model.graphSelected)
	}
	view := model.View()
	if !strings.Contains(view, "#research") {
		t.Fatalf("star canvas missing topics: %q", view)
	}
	// One right-hop moves from the focus card into the outgoing column.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	model = updated.(Model)
	visIn, visOut := model.visibleStarSegments()
	if len(visOut) != 1 || model.graphCards[visOut[0]].ID != "out" || model.graphSelected != len(visIn)+1 {
		t.Fatalf("star selection did not land on outgoing: selected=%d visIn=%v visOut=%v", model.graphSelected, visIn, visOut)
	}
	// Left hops back through focus into the backlinks column.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = updated.(Model)
	if model.graphSelected != 0 {
		t.Fatalf("star selection did not return to backlinks: selected=%d", model.graphSelected)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = updated.(Model)
	if model.graphFocusID != "" || model.graphLayout != "" || model.viewMode != viewTree {
		t.Fatalf("q did not leave star graph: focus=%s layout=%s mode=%s", model.graphFocusID, model.graphLayout, model.viewMode)
	}
}

func TestModel_FilterNarrowsResults(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	alphaID := "019fbde8-1764-7f17-bdbc-e4359f7d1111"
	betaID := "019fbde8-1764-7f17-bdbc-e4359f7d2222"
	model.items = documentItems([]membox.DocumentView{
		{ID: alphaID, Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: betaID, Title: "Beta", Path: "/tmp/beta.md", RelativePath: "beta.md"},
	})
	model.inputVisible = true
	model.input.SetValue("beta")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != betaID {
		t.Fatalf("unexpected filtered items: %+v", model.filtered)
	}

	// Name filter matches the visible logical id left-to-right, not the physical UUID.
	model.input.SetValue(shortID(alphaID))
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != alphaID {
		t.Fatalf("logical id filter did not match: %+v", model.filtered)
	}
}

func TestModel_DraftDateTagDoesNotParticipateInTextFilter(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha", Title: "Alpha", Path: "/tmp/alpha.md"},
		{ID: "beta", Title: "Beta", Path: "/tmp/beta.md"},
	})
	model.inputVisible, model.inputActive = true, true
	model.input.SetValue("+m:7")
	model.refreshFilter()
	if len(model.filtered) != 2 {
		t.Fatalf("draft date tag hid filename results: %+v", model.filtered)
	}
	model.input.SetValue("alpha +m:7")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "alpha" {
		t.Fatalf("draft tag interfered with preceding text filter: %+v", model.filtered)
	}
	if got := textFilterQuery("alpha +m:7d"); got != "alpha" {
		t.Fatalf("text query=%q, want alpha", got)
	}
	if got := textFilterQuery("+m:7d"); got != "" {
		t.Fatalf("tag-only text query=%q, want empty", got)
	}
	model.searchMode = searchModeFull
	model.input.SetValue("+m:7d")
	model.loading = false
	_ = model.filterChanged(nil)
	if model.loading {
		t.Fatal("draft date tag triggered a full-text search")
	}
}

func TestModel_TextPatternsCommitAsTagsWithANDSemantics(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha-report", Title: "Alpha report", Path: "/tmp/alpha-report.md"},
		{ID: "beta-report", Title: "Beta report", Path: "/tmp/beta-report.md"},
		{ID: "alpha-note", Title: "Alpha note", Path: "/tmp/alpha-note.md"},
	})
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("report")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.inputVisible {
		t.Fatal("Enter should close filter after pinning")
	}
	// Reopen to assert tag chip + add second tag.
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	deliverNameFilter(t, &model, "alpha-report", "beta-report")
	if len(model.textFilters) != 1 || model.textFilters[0].Mode != searchModeName || len(model.filtered) != 2 {
		t.Fatalf("name pattern was not committed: tags=%+v filtered=%+v", model.textFilters, model.filtered)
	}
	if view := model.inputView(); !strings.Contains(view, "N: report") {
		t.Fatalf("name tag is not rendered above input: %q", view)
	}

	model.input.SetValue("alpha")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	deliverNameFilter(t, &model, "alpha-report")
	if len(model.textFilters) != 2 || len(model.filtered) != 1 || model.filtered[0].document.ID != "alpha-report" {
		t.Fatalf("name tags do not use AND semantics: tags=%+v filtered=%+v", model.textFilters, model.filtered)
	}

	// Empty Enter opens the selected document.
	model.input.SetValue("")
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.inputVisible || !model.loading || command == nil {
		t.Fatalf("Enter did not open selected document: visible=%v loading=%v command=%v", model.inputVisible, model.loading, command)
	}
}

func TestModel_EnterWithDraftPinsFilterTagAndReturnsToTree(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha-report", Title: "Alpha report", Path: "/tmp/alpha-report.md"},
		{ID: "beta-report", Title: "Beta report", Path: "/tmp/beta-report.md"},
	})
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	// Enter pins the draft as a filter tag and closes the input (no document open).
	// loading/command may still be set by the async name-search for the tag.
	model.input.SetValue("alpha")
	model.refreshFilter()
	_ = model.filterChanged(nil)
	model.loading = false
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.inputVisible || model.inputActive {
		t.Fatalf("Enter with draft should close filter input: visible=%v active=%v", model.inputVisible, model.inputActive)
	}
	if len(model.textFilters) != 1 || model.textFilters[0].Value != "alpha" {
		t.Fatalf("Enter with draft did not pin filter tag: tags=%+v", model.textFilters)
	}
	// rawDocumentID/loading from name-search is fine; opening a document sets
	// a viewer path — ensure we did not leave the tree for a preview open via enter.
	// (open path also sets loading, so assert tags pinned + input closed is enough.)

	// Reopen filter with empty draft; Enter opens the selected document.
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("")
	model.loading = false
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.inputVisible || !model.loading || command == nil {
		t.Fatalf("empty Enter did not open selected document: visible=%v loading=%v command=%v", model.inputVisible, model.loading, command)
	}
}

func TestModel_NameAndFullTextTagsKeepTheirModesAndIntersect(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha", Title: "Alpha", Path: "/tmp/alpha.md"},
		{ID: "beta", Title: "Beta", Path: "/tmp/beta.md"},
	})
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("alpha")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	model = updated.(Model)
	model.input.SetValue("flash attention")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	if len(model.textFilters) != 2 || model.textFilters[0].Mode != searchModeName || model.textFilters[1].Mode != searchModeFull {
		t.Fatalf("text tag modes were not retained: %+v", model.textFilters)
	}
	if !strings.Contains(model.inputView(), "N: alpha") || !strings.Contains(model.inputView(), "C: flash attention") {
		t.Fatalf("mode labels missing from tags: %q", model.inputView())
	}
	updated, _ = model.Update(searchMsg{query: "flash attention", results: []membox.SearchResult{{DocumentID: "alpha"}, {DocumentID: "beta"}}})
	model = updated.(Model)
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "alpha" {
		t.Fatalf("NAME and FULL tags did not intersect: %+v", model.filtered)
	}
}

func TestModel_DateTagsRenderAndFilterWithANDSemantics(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "july-2025", Title: "July 2025", Path: "/tmp/a.md", CreatedAt: time.Date(2025, 2, 1, 0, 0, 0, 0, time.Local), UpdatedAt: time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)},
		{ID: "july-2024", Title: "July 2024", Path: "/tmp/b.md", CreatedAt: time.Date(2024, 2, 1, 0, 0, 0, 0, time.Local), UpdatedAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)},
		{ID: "aug-2025", Title: "August 2025", Path: "/tmp/c.md", CreatedAt: time.Date(2025, 2, 1, 0, 0, 0, 0, time.Local), UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)},
	})
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("+2026-07")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if len(model.dateFilters) != 1 || model.input.Value() != "" || len(model.filtered) != 2 {
		t.Fatalf("month tag was not committed: tags=%+v input=%q filtered=%d", model.dateFilters, model.input.Value(), len(model.filtered))
	}
	if !strings.Contains(model.inputView(), "+2026-07") {
		t.Fatalf("tag is not rendered above input: %q", model.inputView())
	}

	model.input.SetValue("+c:2025")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.inputVisible {
		t.Fatal("enter should close filter after pinning date tag")
	}
	if len(model.dateFilters) != 2 || len(model.filtered) != 1 || model.filtered[0].document.ID != "july-2025" {
		t.Fatalf("date tags do not use AND semantics: tags=%+v filtered=%+v", model.dateFilters, model.filtered)
	}
}

func TestModel_FullSearchResultsRespectDateTags(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "july", Path: "/tmp/july.md", UpdatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)},
		{ID: "august", Path: "/tmp/august.md", UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)},
	})
	filter, err := parseDateFilter("+2026-07", time.Date(2026, 8, 2, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	model.dateFilters = []dateFilter{filter}
	model.searchMode = searchModeFull
	model.inputVisible, model.inputActive = true, true
	model.input.SetValue("query")
	updated, _ := model.Update(searchMsg{query: "query", results: []membox.SearchResult{{DocumentID: "july"}, {DocumentID: "august"}}})
	model = updated.(Model)
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "july" || len(model.dateFilters) != 1 {
		t.Fatalf("full search did not preserve date filter: filtered=%+v tags=%+v", model.filtered, model.dateFilters)
	}
}

func TestModel_ClearAndBackspaceRemoveAllFilterTags(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{{ID: "one", Path: "/tmp/one.md", UpdatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)}})
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("+2026")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.dateFilters) != 1 {
		t.Fatal("date tag was not added")
	}
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("one")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.textFilters) != 1 {
		t.Fatal("text tag was not added")
	}
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(Model)
	if len(model.textFilters) != 0 || len(model.dateFilters) != 1 {
		t.Fatal("empty backspace did not remove the latest text tag first")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(Model)
	if len(model.dateFilters) != 0 {
		t.Fatal("second empty backspace did not remove date tag")
	}

	model.input.SetValue("+2026")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("one")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("/clear")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.dateFilters) != 0 || len(model.textFilters) != 0 || model.input.Value() != "" {
		t.Fatalf("/clear did not clear all tags: dates=%+v text=%+v input=%q", model.dateFilters, model.textFilters, model.input.Value())
	}
}

func TestModel_InvalidDateTagShowsErrorAndEscPreservesValidTags(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{{ID: "one", Path: "/tmp/one.md", UpdatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)}})
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("+2026-02-30")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.filterErr == nil || len(model.dateFilters) != 0 || !model.inputVisible {
		t.Fatalf("invalid tag was accepted: err=%v tags=%+v", model.filterErr, model.dateFilters)
	}

	model.input.SetValue("+2026-07")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.inputVisible || len(model.dateFilters) != 1 || len(model.filtered) != 1 {
		t.Fatalf("esc did not preserve active tags: visible=%v tags=%+v filtered=%d", model.inputVisible, model.dateFilters, len(model.filtered))
	}
}

func TestModel_FilterMatchesLogicalIDAndUnicodeFilename(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	target := "019fbde8-1764-7f17-bdbc-e4359f7dceed"
	model.items = documentItems([]membox.DocumentView{
		{ID: target, Title: "01-语言大乱斗-ts", Path: "/tmp/01-语言大乱斗-ts.md", RelativePath: "01-语言大乱斗-ts.md"},
		{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
	})
	model.inputVisible = true
	logical := shortID(target)
	model.input.SetValue(logical)
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != target {
		t.Fatalf("logical id did not match: %+v (logical=%s)", model.filtered, logical)
	}
	// Physical time-prefix must not match in the TUI.
	model.input.SetValue("019fbde8")
	model.refreshFilter()
	if len(model.filtered) != 0 {
		t.Fatalf("physical UUID prefix leaked into name filter: %+v", model.filtered)
	}
	model.input.SetValue(logical + " 大乱斗")
	model.refreshFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("logical id plus Unicode filename lost match: %+v", model.filtered)
	}
	model.input.SetValue("大乱斗")
	model.refreshFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("Unicode filename did not match: %+v", model.filtered)
	}
}

func TestModel_UnpinMovesCursorToNextPinnedDocument(t *testing.T) {
	app := &fakeApp{pins: map[string]bool{"p1": true, "p2": true, "p3": true}}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{
		{ID: "p1", Path: "/tmp/p1.md", Pinned: true},
		{ID: "p2", Path: "/tmp/p2.md", Pinned: true},
		{ID: "p3", Path: "/tmp/p3.md", Pinned: true},
		{ID: "u1", Path: "/tmp/u1.md"},
	})
	model.refreshFilter()
	// select p1 (index 0), unpin it → cursor should move to p2 (still pinned).
	model.selected = 0
	message := togglePinCmd(context.Background(), app, "p1")().(pinMsg)
	updated, _ := model.Update(message)
	model = updated.(Model)
	if model.filtered[model.selected].document.ID != "p2" {
		t.Fatalf("unpin should move cursor to next pinned p2, got %q (selected=%d)",
			model.filtered[model.selected].document.ID, model.selected)
	}
	// Unpin p2 too → cursor should move to p3.
	message = togglePinCmd(context.Background(), app, "p2")().(pinMsg)
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.filtered[model.selected].document.ID != "p3" {
		t.Fatalf("unpin p2 should move cursor to p3, got %q", model.filtered[model.selected].document.ID)
	}
	// Unpin the last pinned doc → no pinned left, cursor stays where it is.
	message = togglePinCmd(context.Background(), app, "p3")().(pinMsg)
	updated, _ = model.Update(message)
	model = updated.(Model)
	if model.filtered[model.selected].document.ID != "p3" {
		t.Fatalf("last unpin should keep cursor on p3, got %q", model.filtered[model.selected].document.ID)
	}
}

func TestCtrlNStartsQuickNote(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "doc-1", Title: "Existing", Path: "/tmp/existing.md", RelativePath: "existing.md", Status: "active"},
	})
	model.refreshFilter()
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	model = updated.(Model)
	if !model.loading {
		t.Fatal("ctrl+n should set loading")
	}
	if cmd == nil {
		t.Fatal("ctrl+n should schedule createQuickNoteCmd")
	}
	// Batch(spinner, createQuickNoteCmd) — find the noteCreatedMsg producer.
	var created noteCreatedMsg
	found := false
	msg := cmd()
	switch batch := msg.(type) {
	case noteCreatedMsg:
		created, found = batch, true
	case tea.BatchMsg:
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if c, ok := sub().(noteCreatedMsg); ok {
				created, found = c, true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected noteCreatedMsg in ctrl+n command batch, got %T", msg)
	}
	if created.err != nil {
		t.Fatal(created.err)
	}
	if !created.quickNote {
		t.Fatal("expected quickNote=true")
	}
	if created.document.ID != "quick-note" {
		t.Fatalf("document=%+v", created.document)
	}
	if created.command == nil {
		t.Fatal("expected editor command")
	}
}
