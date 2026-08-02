package tui

import (
	"context"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"membox"
)

type fakeLauncher struct{}

func (fakeLauncher) EditorCommand(context.Context, string) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}
func (fakeLauncher) ViewerCommand(context.Context, string) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}
func (fakeLauncher) OpenCommand(context.Context, string) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}

type fakeApp struct{ resolved int }

func (f *fakeApp) AddPath(context.Context, membox.AddPathCommand) (membox.AddPathResult, error) {
	return membox.AddPathResult{}, nil
}
func (f *fakeApp) SearchDocuments(context.Context, membox.SearchDocumentsQuery) ([]membox.SearchResult, error) {
	return nil, nil
}
func (f *fakeApp) ListDocuments(context.Context, membox.ListDocumentsQuery) ([]membox.DocumentView, error) {
	return []membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", Status: "active"},
		{ID: "019-beta", Title: "Beta", Path: "/tmp/beta.md", Status: "active"},
	}, nil
}
func (f *fakeApp) ReadDocument(context.Context, membox.ReadDocumentQuery) ([]byte, error) {
	return []byte("body"), nil
}
func (f *fakeApp) ResolveDocumentLocation(context.Context, membox.ResolveLocationQuery) (membox.LocationView, error) {
	f.resolved++
	return membox.LocationView{DocumentID: "019-alpha", Path: "/tmp/alpha.md", Status: "active"}, nil
}
func (f *fakeApp) ReindexDocument(context.Context, membox.ReindexDocumentCommand) error { return nil }
func (f *fakeApp) ScanPaths(context.Context, membox.ScanPathsCommand) (membox.ScanReport, error) {
	return membox.ScanReport{}, nil
}
func (f *fakeApp) ListPaths(context.Context) ([]membox.PathView, error) { return nil, nil }
func (f *fakeApp) GetIndexStatus(context.Context) (membox.IndexStatusView, error) {
	return membox.IndexStatusView{}, nil
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
	if !strings.Contains(view, "Online_normalize") || !strings.Contains(view, "2083") {
		t.Fatalf("tree does not contain recognizable original filename: %q", view)
	}
	firstLine := strings.Split(view, "\n")[0]
	if strings.Contains(firstLine, "# title in document") {
		t.Fatalf("tree replaced filename with Markdown title: %q", firstLine)
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
	if model.detailsVisible {
		t.Fatal("single space should not toggle immediately")
	}
	// simulate timeout expiry (single space confirmed)
	updated, _ = model.Update(spaceTimeoutMsg{sequence: model.spaceSequence})
	model = updated.(Model)
	if !model.detailsVisible {
		t.Fatal("single space timeout did not show details pane")
	}
	// press space again and let timeout fire -> hide
	model.lastKeyAt = time.Now().Add(-time.Second)
	updated, _ = model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(spaceTimeoutMsg{sequence: model.spaceSequence})
	model = updated.(Model)
	if model.detailsVisible {
		t.Fatal("second single space did not hide details pane")
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

func TestModel_DoubleSpaceDoesNotToggleDetails(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space) // double space -> toggle input
	model = updated.(Model)
	if model.detailsVisible {
		t.Fatal("double space should not toggle details")
	}
	if !model.inputVisible {
		t.Fatal("double space did not open input")
	}
}

func TestModel_DoubleSpaceOpensInputAndSpacesRemainAvailableForText(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	if !model.inputVisible || !model.inputActive {
		t.Fatalf("double space did not show input")
	}
	updated, _ = model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	if !model.inputVisible || !model.inputActive || model.input.Value() != "  " {
		t.Fatalf("spaces in active filter changed input state: visible=%v active=%v value=%q", model.inputVisible, model.inputActive, model.input.Value())
	}
}

func TestModel_SortToggleOrdersNewestFirstAndRestoresNameOrder(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "alpha", Path: "/tmp/alpha.md", UpdatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)},
		{ID: "charlie", Path: "/tmp/charlie.md", UpdatedAt: time.Time{}},
		{ID: "bravo", Path: "/tmp/bravo.md", UpdatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)},
	})
	model.refreshFilter()
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"alpha", "bravo", "charlie"}) {
		t.Fatalf("default order is not by name: %v", got)
	}
	model.selected = 0
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"bravo", "alpha", "charlie"}) {
		t.Fatalf("time order is not newest first: %v", got)
	}
	if model.filtered[model.selected].document.ID != "alpha" {
		t.Fatalf("sort toggle did not preserve selection: selected=%s", model.filtered[model.selected].document.ID)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"alpha", "bravo", "charlie"}) {
		t.Fatalf("second toggle did not restore name order: %v", got)
	}
}

func TestModel_FilterNarrowsResults(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019-beta", Title: "Beta", Path: "/tmp/beta.md", RelativePath: "beta.md"},
	})
	model.inputVisible = true
	model.input.SetValue("beta")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "019-beta" {
		t.Fatalf("unexpected filtered items: %+v", model.filtered)
	}

	model.input.SetValue("019-alpha")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "019-alpha" {
		t.Fatalf("UUID filter did not match: %+v", model.filtered)
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
	if !model.inputVisible || len(model.dateFilters) != 2 || len(model.filtered) != 1 || model.filtered[0].document.ID != "july-2025" {
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

func TestModel_ClearAndBackspaceRemoveDateTags(t *testing.T) {
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
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	model = updated.(Model)
	if len(model.dateFilters) != 0 {
		t.Fatal("empty backspace did not remove last date tag")
	}

	model.input.SetValue("+2026")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	model.input.SetValue("/clear")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.dateFilters) != 0 || model.input.Value() != "" {
		t.Fatalf("/clear did not clear tags: tags=%+v input=%q", model.dateFilters, model.input.Value())
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

func TestModel_FilterMatchesUUIDSuffixAndUnicodeFilename(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbde8-1764-7f17-bdbc-e4359f7dceed", Title: "01-语言大乱斗-ts", Path: "/tmp/01-语言大乱斗-ts.md", RelativePath: "01-语言大乱斗-ts.md"},
		{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
	})
	model.inputVisible = true
	model.input.SetValue("ceed")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "019fbde8-1764-7f17-bdbc-e4359f7dceed" {
		t.Fatalf("UUID suffix did not match: %+v", model.filtered)
	}
	model.input.SetValue("ceed 大乱斗")
	model.refreshFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("UUID suffix plus Unicode filename lost match: %+v", model.filtered)
	}
	model.input.SetValue("大乱斗")
	model.refreshFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("Unicode filename did not match: %+v", model.filtered)
	}
}

func TestModel_DocumentsLoadStartsAtFirstItem(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.scrollTop = 40
	updated, _ := model.Update(documentsMsg{sequence: model.listSequence, documents: []membox.DocumentView{
		{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019fbe56-64c3-7eec-97a7-6c8813ec2d64", Title: "Beta", Path: "/tmp/beta.md", RelativePath: "beta.md"},
	}})
	model = updated.(Model)
	if model.selected != 0 || model.scrollTop != 0 {
		t.Fatalf("document load did not reset view: selected=%d top=%d", model.selected, model.scrollTop)
	}
	view := model.treePreviewView()
	if !strings.Contains(view, "dabf  alpha.md") {
		t.Fatalf("first document is not visible after load: %q", view)
	}
}

func TestModel_UpAtFirstKeepsFocusVisible(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019fbe56-64c3-7eec-97a7-6c8813ec2d64", Title: "Beta", Path: "/tmp/beta.md", RelativePath: "beta.md"},
	})
	model.refreshFilter()
	model.rawDocumentID = model.filtered[0].document.ID
	model.rawContent = "alpha body"
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.selected != 0 || model.scrollTop != 0 {
		t.Fatalf("focus moved at top: selected=%d top=%d", model.selected, model.scrollTop)
	}
	if command != nil {
		t.Fatal("up at top scheduled another preview load")
	}
	view := model.treePreviewView()
	if !strings.Contains(view, "> dabf  alpha.md") {
		t.Fatalf("focus is not visible at top: %q", view)
	}
}

func TestModel_NarrowingFilterKeepsResultVisible(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	documents := []membox.DocumentView{{ID: "019fbde8-1764-7f17-bdbc-e4359f7dceed", Title: "01-语言大乱斗-ts", Path: "/tmp/01-语言大乱斗-ts.md", RelativePath: "01-语言大乱斗-ts.md"}}
	for index := 0; index < 50; index++ {
		documents = append(documents, membox.DocumentView{ID: fmt.Sprintf("019fbe56-64c3-7e3c-861d-4da66742%04d", index), Title: fmt.Sprintf("Alpha %02d", index), Path: fmt.Sprintf("/tmp/alpha-%02d.md", index), RelativePath: fmt.Sprintf("alpha-%02d.md", index)})
	}
	model.items = documentItems(documents)
	model.width, model.height = 120, 24
	model.inputVisible = true
	model.input.SetValue("ce")
	model.refreshFilter()
	if len(model.filtered) == 0 {
		t.Fatal("expected broad match")
	}
	model.selected = 30
	model.scrollTop = 30
	model.input.SetValue("ceed")
	model.refreshFilter()
	if len(model.filtered) != 1 {
		t.Fatalf("expected one narrow result: %+v", model.filtered)
	}
	if model.scrollTop != 0 {
		t.Fatalf("stale scrollTop hid narrow result: %d", model.scrollTop)
	}
	view := model.treePreviewView()
	if !strings.Contains(view, "ceed") || !strings.Contains(view, "01-语言大乱斗-ts.md") {
		t.Fatalf("narrow result is not rendered: %q", view)
	}
}

func TestModel_EnterOpensViewerSubprocess(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md"}})
	model.refreshFilter()
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.fullscreen {
		t.Fatal("enter used internal fullscreen instead of viewer subprocess")
	}
	if !model.loading {
		t.Fatal("enter did not start viewer resolution")
	}
	if command == nil {
		t.Fatal("enter did not schedule viewer command")
	}
}

func TestModel_ResultsHaveUUIDInStatus(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md"}})
	model.refreshFilter()
	status := model.statusBar()
	if !strings.Contains(status, "dabf") {
		t.Fatalf("status bar does not contain UUID: %q", status)
	}
	if !strings.HasPrefix(strings.TrimLeft(status, " "), "s sort:name") {
		t.Fatalf("sort mode and shortcuts are not on the left: %q", status)
	}
}

func TestModel_InputUsesSingleHighlightedModeBadge(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width = 120
	model.inputVisible = true
	input := model.inputView()
	if !strings.Contains(input, " NAME ") {
		t.Fatalf("input does not contain highlighted mode badge: %q", input)
	}
	if strings.Contains(input, " NAME ") && strings.Contains(input, "name  filter documents") {
		t.Fatalf("mode appears redundantly: %q", input)
	}
}

func TestModel_ViewUsesTerminalHeightExactly(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.inputVisible = true
	model.detailsVisible = true
	view := model.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	t.Logf("input lines=%d status=%d visible=%d content=%d", len(strings.Split(model.inputView(), "\n")), len(strings.Split(model.statusBar(), "\n")), model.visibleRows(), len(strings.Split(model.treePreviewView(), "\n")))
	if len(lines) != model.height {
		t.Fatalf("view height=%d terminal height=%d lines=%q", len(lines), model.height, lines)
	}
	if !strings.Contains(lines[len(lines)-1], "tab mode") {
		t.Fatalf("status bar is not bottom: %q", lines[len(lines)-1])
	}
	filter, err := parseDateFilter("+2026-07", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model.dateFilters = []dateFilter{filter}
	view = model.View()
	lines = strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) != model.height || !strings.Contains(view, "+2026-07") {
		t.Fatalf("tag row broke terminal height: height=%d want=%d view=%q", len(lines), model.height, view)
	}
}

func TestModel_TabTogglesHighlightedMode(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.inputVisible = true
	model.inputActive = true
	model.input.Focus()
	badge := model.modeBadge()
	if !strings.Contains(badge, " NAME ") {
		t.Fatalf("default mode badge=%q", badge)
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	badge = model.modeBadge()
	if !strings.Contains(badge, " FULL ") {
		t.Fatalf("full mode badge=%q", badge)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.searchMode != searchModeName {
		t.Fatalf("tab did not toggle back: %s", model.searchMode)
	}
}

func TestHighlightQueryUsesReadableOrangeBackground(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	highlighted := highlightQuery("alpha beta alpha", "beta")
	if !strings.Contains(highlighted, "48;2;217;154;43") {
		t.Fatalf("expected orange-yellow background: %q", highlighted)
	}
	if !strings.Contains(highlighted, "beta") {
		t.Fatalf("highlight lost query: %q", highlighted)
	}
}

func TestModel_FullModePreviewKeepsHighlightAfterResults(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.searchMode = searchModeFull
	model.inputVisible = true
	model.input.SetValue("softmax")
	model.rawContent = "alpha\nsoftmax content\nomega"
	model.preview.Height = 5
	model.applyPreviewContent()
	view := model.preview.View()
	if !strings.Contains(view, "48;2;217;154;43") {
		t.Fatalf("full mode preview lost highlight: %q", view)
	}
	if !strings.Contains(view, "softmax") {
		t.Fatalf("full mode preview lost content: %q", view)
	}
}

func TestModeBadgeUsesBlueAndGreen(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	name := model.modeBadge()
	model.searchMode = searchModeFull
	full := model.modeBadge()
	if !strings.Contains(name, "48;2;48;89;184") {
		t.Fatalf("NAME badge is not blue: %q", name)
	}
	if !strings.Contains(full, "48;2;47;125;73") {
		t.Fatalf("FULL badge is not green: %q", full)
	}
}

func TestShortIDUsesLastFourUUIDCharacters(t *testing.T) {
	if got := shortID("019fbde8-1765-7f72-a52f-0e0606d10000"); got != "0000" {
		t.Fatalf("unexpected short UUID %q", got)
	}
	if shortID("019fbde8-1765-7f72-a52f-0e0606d10000") == shortID("019fbde8-1765-7f72-a52f-0e0606d11111") {
		t.Fatal("distinct UUIDs collapsed")
	}
}

func TestResolveViewerCmd_PerformsIOInsideCommand(t *testing.T) {
	app := &fakeApp{}
	command := resolveViewerCmd(context.Background(), app, "id")
	if app.resolved != 0 {
		t.Fatal("facade was called while constructing tea.Cmd")
	}
	message := command()
	if app.resolved != 1 {
		t.Fatal("facade was not called when tea.Cmd executed")
	}
	ready, ok := message.(editReadyMsg)
	if !ok || ready.err != nil || ready.path != "/tmp/alpha.md" || !ready.viewer {
		t.Fatalf("unexpected message: %#v", message)
	}
}

func TestResolveEditorCmd_PerformsIOInsideCommand(t *testing.T) {
	app := &fakeApp{}
	command := resolveEditorCmd(context.Background(), app, "id")
	if app.resolved != 0 {
		t.Fatal("facade was called while constructing tea.Cmd")
	}
	message := command()
	if app.resolved != 1 {
		t.Fatal("facade was not called when tea.Cmd executed")
	}
	ready, ok := message.(editReadyMsg)
	if !ok || ready.err != nil || ready.path != "/tmp/alpha.md" {
		t.Fatalf("unexpected message: %#v", message)
	}
}

func TestRenderCard_LinesHaveEqualDisplayWidth(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New(context.Background(), nil, nil)

	cardWidth := 34
	summaries := []string{
		"short",
		"个人职业目标清单：深入掌握 C++ 等硬核技术，成长为合格的 full-stack engineer 与网络工程师。",
		"",
		"a much longer summary that should wrap across several lines because it contains many words and keeps going and going",
	}
	for i, summary := range summaries {
		candidate := item{
			document: membox.DocumentView{ID: "019aaaaa-bbbb-cccc-dddd-eeeeffff0000", Title: "标题", Summary: summary, Path: "/notes/文档.md"},
			filename: "文档.md",
		}
		for _, selected := range []bool{false, true} {
			lines := m.renderCard(candidate, cardWidth, selected)
			for li, line := range lines {
				if w := ansi.StringWidth(line); w != cardWidth {
					t.Fatalf("case %d selected=%v line %d width=%d want %d: %q", i, selected, li, w, cardWidth, line)
				}
			}
		}
	}
}

func TestBoardView_OnlyShowsDocumentsWithSummary(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New(context.Background(), nil, nil)
	m.width = 100
	m.height = 40
	m.viewMode = viewBoard
	m.filtered = []item{
		{document: membox.DocumentView{ID: "019aaaaa-0000-0000-0000-000000000001", Title: "有摘要", Summary: "这是摘要内容", Path: "/a.md"}, filename: "a.md"},
		{document: membox.DocumentView{ID: "019aaaaa-0000-0000-0000-000000000002", Title: "无摘要", Summary: "", Path: "/b.md"}, filename: "b.md"},
	}
	out := m.boardView()
	// The doc WITH a summary must be rendered (its summary + filename appear).
	if !strings.Contains(out, "这是摘要内容") {
		t.Fatalf("board should contain the summarized doc's summary: %q", out)
	}
	if !strings.Contains(out, "a.md") {
		t.Fatalf("board should contain the summarized doc's filename: %q", out)
	}
	// The doc WITHOUT a summary must be filtered out (its filename/ID absent).
	if strings.Contains(out, "b.md") {
		t.Fatalf("board should NOT contain the doc without summary: %q", out)
	}
	if strings.Contains(out, "0002") {
		t.Fatalf("board should NOT contain the filtered doc's ID: %q", out)
	}
}

func TestModel_BoardNavigationOnlyMovesAmongVisibleCards(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 40
	docs := []membox.DocumentView{
		{ID: "id-a", Title: "A", Path: "/tmp/a.md", RelativePath: "a.md"},                       // 0: no summary
		{ID: "id-b", Title: "B", Path: "/tmp/b.md", RelativePath: "b.md", Summary: "summary b"}, // 1: visible
		{ID: "id-c", Title: "C", Path: "/tmp/c.md", RelativePath: "c.md"},                       // 2: no summary
		{ID: "id-d", Title: "D", Path: "/tmp/d.md", RelativePath: "d.md", Summary: "summary d"}, // 3: visible
	}
	model.items = documentItems(docs)
	model.filtered = documentItems(docs)
	model.viewMode = viewBoard

	step := func(key string) {
		updated, _ := model.moveSelection(key)
		model = updated.(Model)
	}

	// Entering board mode: a non-visible selection snaps to the first visible card.
	model.selected = 0
	model.snapToBoardSelection()
	if model.selected != 1 {
		t.Fatalf("snap should land on first visible card (index 1), got %d", model.selected)
	}

	// Down moves to the next visible card (index 3), skipping the summary-less index 2.
	step("down")
	if model.selected != 3 {
		t.Fatalf("down should move to next visible card (index 3), got %d", model.selected)
	}

	// Down at the last visible card stays put.
	step("down")
	if model.selected != 3 {
		t.Fatalf("down at last visible card should stay at 3, got %d", model.selected)
	}

	// Up moves back to index 1, skipping index 2.
	step("up")
	if model.selected != 1 {
		t.Fatalf("up should move to previous visible card (index 1), got %d", model.selected)
	}

	// Up at the first visible card stays put.
	step("up")
	if model.selected != 1 {
		t.Fatalf("up at first visible card should stay at 1, got %d", model.selected)
	}

	// The selection must always correspond to a card that is actually drawn.
	if model.filtered[model.selected].document.Summary == "" {
		t.Fatalf("selection landed on a non-displayed card: %+v", model.filtered[model.selected].document)
	}
}

// TestModel_BoardArrowKeysThroughUpdate drives real tea.KeyMsg events through
// Update() (the exact path a user's keypress takes) and asserts the highlight
// never lands on a card that is not drawn on the board.
func TestModel_BoardArrowKeysThroughUpdate(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 40
	docs := []membox.DocumentView{
		{ID: "id-a", Title: "A", Path: "/tmp/a.md", RelativePath: "a.md"},                       // no summary
		{ID: "id-b", Title: "B", Path: "/tmp/b.md", RelativePath: "b.md", Summary: "summary b"}, // visible
		{ID: "id-c", Title: "C", Path: "/tmp/c.md", RelativePath: "c.md"},                       // no summary
		{ID: "id-d", Title: "D", Path: "/tmp/d.md", RelativePath: "d.md", Summary: "summary d"}, // visible
		{ID: "id-e", Title: "E", Path: "/tmp/e.md", RelativePath: "e.md"},                       // no summary
	}
	model.items = documentItems(docs)
	model.refreshFilter()
	model.resize()

	send := func(m Model, key tea.KeyMsg) Model {
		nm, _ := m.Update(key)
		return nm.(Model)
	}

	// Enter board mode with Tab (input is inactive by default).
	model = send(model, tea.KeyMsg{Type: tea.KeyTab})
	if model.viewMode != viewBoard {
		t.Fatalf("Tab should enter board mode, got viewMode=%v", model.viewMode)
	}
	if model.filtered[model.selected].document.Summary == "" {
		t.Fatalf("after entering board, selection is on a non-displayed card idx %d", model.selected)
	}

	// Drive arrow keys through Update; the selection must only ever be on a
	// displayed (summary) card.
	for i := 0; i < 12; i++ {
		model = send(model, tea.KeyMsg{Type: tea.KeyDown})
		if model.filtered[model.selected].document.Summary == "" {
			t.Fatalf("down #%d landed on non-displayed card idx %d (ID %s)", i, model.selected, model.filtered[model.selected].document.ID)
		}
	}
	for i := 0; i < 12; i++ {
		model = send(model, tea.KeyMsg{Type: tea.KeyUp})
		if model.filtered[model.selected].document.Summary == "" {
			t.Fatalf("up #%d landed on non-displayed card idx %d (ID %s)", i, model.selected, model.filtered[model.selected].document.ID)
		}
	}
}

// TestWrapText_PreservesCJKContent verifies wrapText breaks long CJK runs
// (which have no spaces) at character boundaries, keeping every line within the
// width and preserving all content (nothing dropped, nothing truncated).
func TestWrapText_PreservesCJKContent(t *testing.T) {
	s := "个人预算与待购清单，记录想购买的电子设备、家居与车载安全用品，以及信用卡、房贷等各类账单的到期日与金额。"
	lines := wrapText(s, 28)
	if len(lines) <= 1 {
		t.Fatalf("expected the long CJK summary to wrap into multiple lines, got %d: %q", len(lines), lines)
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 28 {
			t.Fatalf("line exceeds width: %d > 28: %q", w, l)
		}
	}
	joined := strings.Join(lines, "")
	for _, r := range s {
		if r == ' ' {
			continue
		}
		if !strings.ContainsRune(joined, r) {
			t.Fatalf("rune %q lost during wrapping", r)
		}
	}
}

// TestRenderCard_FullSummaryNoTruncation verifies a card renders the entire
// summary across wrapped lines with no ellipsis truncation.
func TestRenderCard_FullSummaryNoTruncation(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := New(context.Background(), nil, nil)
	longSummary := "个人预算与待购清单，记录想购买的电子设备、家居与车载安全用品，以及信用卡、房贷等各类账单的到期日与金额。"
	candidate := item{
		document: membox.DocumentView{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "budget", Summary: longSummary, Path: "/tmp/budget.md"},
		filename: "budget.md",
	}
	cardLines := m.renderCard(candidate, 32, false)
	joined := strings.Join(cardLines, "\n")
	if strings.Contains(joined, "…") {
		t.Fatalf("card truncated content with ellipsis:\n%s", joined)
	}
	for _, r := range longSummary {
		if r == ' ' {
			continue
		}
		if !strings.ContainsRune(joined, r) {
			t.Fatalf("summary rune %q missing from rendered card:\n%s", r, joined)
		}
	}
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// TestModel_QIsContextualBackNotQuit verifies q steps back within the TUI
// (board -> tree, then closes the details pane) instead of quitting.
func TestModel_QIsContextualBackNotQuit(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 40
	model.items = documentItems([]membox.DocumentView{
		{ID: "id-b", Title: "B", Path: "/tmp/b.md", RelativePath: "b.md", Summary: "summary b"},
	})
	model.refreshFilter()
	model.resize()

	q := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}

	// q in board view returns to tree view and does NOT quit.
	model.viewMode = viewBoard
	updated, cmd := model.Update(q)
	model = updated.(Model)
	if model.viewMode != viewTree {
		t.Fatalf("q in board view should return to tree, got viewMode=%v", model.viewMode)
	}
	if isQuitCmd(cmd) {
		t.Fatal("q must not quit the program")
	}

	// q in tree view with the details pane open closes it and does NOT quit.
	model.detailsVisible = true
	updated, cmd = model.Update(q)
	model = updated.(Model)
	if model.detailsVisible {
		t.Fatal("q in tree view should close the details pane")
	}
	if isQuitCmd(cmd) {
		t.Fatal("q must not quit the program")
	}
}

// TestModel_CtrlDQuits verifies Ctrl+D quits the program from any state.
func TestModel_CtrlDQuits(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 40

	ctrlD := tea.KeyMsg{Type: tea.KeyCtrlD}

	// Quits from the base navigation state.
	_, cmd := model.Update(ctrlD)
	if !isQuitCmd(cmd) {
		t.Fatal("ctrl+d should quit from navigation state")
	}

	// Quits from board view too.
	model.viewMode = viewBoard
	_, cmd = model.Update(ctrlD)
	if !isQuitCmd(cmd) {
		t.Fatal("ctrl+d should quit from board view")
	}
}

// TestModel_CtrlCDeletesInputChar verifies Ctrl+C deletes a character in the
// filter input box and does NOT quit the program.
func TestModel_CtrlCDeletesInputChar(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 40
	model.inputVisible = true
	model.inputActive = true
	model.input.Focus()
	model.input.SetValue("abc")

	ctrlC := tea.KeyMsg{Type: tea.KeyCtrlC}
	updated, cmd := model.Update(ctrlC)
	model = updated.(Model)

	if isQuitCmd(cmd) {
		t.Fatal("ctrl+c must not quit the program")
	}
	if got := model.input.Value(); got != "ab" {
		t.Fatalf("ctrl+c should delete one char from input: got %q, want %q", got, "ab")
	}
}
