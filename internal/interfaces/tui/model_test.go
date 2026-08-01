package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	updated := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	model.items = documentItems([]membox.DocumentView{{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", MTime: updated.UnixNano()}})
	model.refreshFilter()
	view := model.treePreviewView()
	if !strings.Contains(view, "2026-08-02  2026-08-02") {
		t.Fatalf("tree does not contain filesystem modified date: %q", view)
	}
}

func TestModel_FocusedDocumentShowsDetailsPanel(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	updated := time.Date(2026, 8, 2, 12, 0, 0, 0, time.Local)
	model.items = documentItems([]membox.DocumentView{{
		ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md",
		MTime: updated.UnixNano(),
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
		t.Fatalf("details missing filesystem modified date: %q", view)
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
		t.Fatal("single space did not show details pane")
	}
	model.lastKeyAt = time.Now().Add(-time.Second)
	updated, _ = model.Update(space)
	model = updated.(Model)
	if model.detailsVisible {
		t.Fatal("single space did not hide details pane")
	}
}

func TestModel_DoubleSpaceTogglesInput(t *testing.T) {
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
	if model.inputVisible || model.inputActive {
		t.Fatalf("double space did not hide input")
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
	if !strings.HasPrefix(strings.TrimLeft(status, " "), "space×2 filter") {
		t.Fatalf("shortcuts are not on the left: %q", status)
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
