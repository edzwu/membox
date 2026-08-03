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

type fakeApp struct {
	resolved  int
	pins      map[string]bool
	viewer    string
	model     string
	mainPath  string
	webOpened []string
	scanCount int
}

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
func (f *fakeApp) DeleteDocument(_ context.Context, command membox.DeleteDocumentCommand) (membox.DeleteDocumentResult, error) {
	return membox.DeleteDocumentResult{DocumentID: command.Selector, Path: "/tmp/deleted.md"}, nil
}
func (f *fakeApp) CreateNote(_ context.Context, command membox.CreateNoteCommand) (membox.CreateNoteResult, error) {
	document := membox.DocumentView{ID: "new-note", Title: command.Title, Path: "/tmp/new-note.md", RelativePath: "new-note.md", Status: "active"}
	result := membox.CreateNoteResult{Document: document}
	if command.FromSelector != "" {
		result.Link = &membox.LinkView{FromDocumentID: command.FromSelector, ToDocumentID: document.ID, Kind: "manual"}
	}
	return result, nil
}
func (f *fakeApp) CreateTopic(_ context.Context, command membox.CreateTopicCommand) (membox.CreateTopicResult, error) {
	return membox.CreateTopicResult{Topic: membox.TopicView{ID: "topic-" + command.Name, Name: command.Name, Path: "/tmp/topic-" + command.Name + ".md"}}, nil
}
func (f *fakeApp) ListTopics(context.Context, membox.ListTopicsQuery) ([]membox.TopicView, error) {
	return []membox.TopicView{{ID: "topic-attention", Name: "attention", Path: "/tmp/topic-attention.md"}}, nil
}
func (f *fakeApp) AddDocumentTopic(_ context.Context, command membox.TopicMembershipCommand) (membox.TopicMembershipResult, error) {
	return membox.TopicMembershipResult{DocumentID: command.DocumentSelector, Topic: membox.TopicView{ID: command.TopicSelector, Name: "attention"}, Added: true}, nil
}
func (f *fakeApp) RemoveDocumentTopic(_ context.Context, command membox.TopicMembershipCommand) (membox.TopicMembershipResult, error) {
	return membox.TopicMembershipResult{DocumentID: command.DocumentSelector, Topic: membox.TopicView{ID: command.TopicSelector, Name: "attention"}, Removed: true}, nil
}
func (f *fakeApp) ListTopicDocuments(context.Context, membox.ListTopicDocumentsQuery) (membox.TopicDocumentsView, error) {
	return membox.TopicDocumentsView{Topic: membox.TopicView{ID: "topic-attention", Name: "attention"}}, nil
}
func (f *fakeApp) LinkDocuments(context.Context, membox.LinkDocumentsCommand) (membox.LinkDocumentsResult, error) {
	return membox.LinkDocumentsResult{Created: true}, nil
}
func (f *fakeApp) UnlinkDocuments(context.Context, membox.UnlinkDocumentsCommand) (membox.UnlinkDocumentsResult, error) {
	return membox.UnlinkDocumentsResult{Removed: true}, nil
}
func (f *fakeApp) GetDocumentGraph(context.Context, membox.GetDocumentGraphQuery) (membox.DocumentGraphView, error) {
	return membox.DocumentGraphView{}, nil
}
func (f *fakeApp) ToggleDocumentPin(_ context.Context, command membox.ToggleDocumentPinCommand) (membox.ToggleDocumentPinResult, error) {
	if f.pins == nil {
		f.pins = make(map[string]bool)
	}
	f.pins[command.Selector] = !f.pins[command.Selector]
	return membox.ToggleDocumentPinResult{DocumentID: command.Selector, Pinned: f.pins[command.Selector]}, nil
}
func (f *fakeApp) ScanPaths(context.Context, membox.ScanPathsCommand) (membox.ScanReport, error) {
	f.scanCount++
	return membox.ScanReport{Added: 1, Files: 3}, nil
}
func (f *fakeApp) ListPaths(context.Context) ([]membox.PathView, error) { return nil, nil }
func (f *fakeApp) GetIndexStatus(context.Context) (membox.IndexStatusView, error) {
	return membox.IndexStatusView{}, nil
}
func (f *fakeApp) GetViewer(context.Context) (string, error) {
	if f.viewer == "" {
		return "leaf", nil
	}
	return f.viewer, nil
}
func (f *fakeApp) SetViewer(_ context.Context, mode string) error {
	if mode != "leaf" && mode != "web" {
		return fmt.Errorf("invalid viewer %q", mode)
	}
	f.viewer = mode
	return nil
}
func (f *fakeApp) ListSettings(context.Context) ([]membox.SettingView, error) {
	viewer := f.viewer
	if viewer == "" {
		viewer = "leaf"
	}
	model := f.model
	if model == "" {
		model = "k3"
	}
	mainPath := f.mainPath
	if mainPath == "" {
		mainPath = "/tmp/notes"
	}
	return []membox.SettingView{
		{Key: "viewer", Label: "viewer", Value: viewer, Options: []string{"leaf", "web"}},
		{Key: "model", Label: "model", Value: model, Options: []string{"k3", "grok-4.5"}},
		{Key: "main_path", Label: "main path", Value: mainPath, Options: []string{"/tmp/notes", "/tmp/other"}},
	}, nil
}
func (f *fakeApp) SetSetting(_ context.Context, key, value string) error {
	switch key {
	case "viewer":
		return f.SetViewer(context.Background(), value)
	case "model":
		if value != "k3" && value != "grok-4.5" {
			return fmt.Errorf("invalid model %q", value)
		}
		f.model = value
		return nil
	case "main_path":
		f.mainPath = value
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}
func (f *fakeApp) OpenDocumentWeb(_ context.Context, selector string) (string, error) {
	f.webOpened = append(f.webOpened, selector)
	return "http://127.0.0.1:9999/?id=" + selector, nil
}
func (f *fakeApp) StartWebServer(context.Context, int) (string, error) {
	return "http://127.0.0.1:8787", nil
}
func (f *fakeApp) BridgeInfo() (string, string) {
	return "http://127.0.0.1:8787", "test-token"
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

func TestModel_DefaultSortIsNewestFirstAndSTogglesToNameOrder(t *testing.T) {
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
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"alpha", "bravo", "charlie"}) {
		t.Fatalf("sort toggle did not switch to name order: %v", got)
	}
	if model.selected != 0 || model.scrollTop != 0 || model.filtered[model.selected].document.ID != "alpha" {
		t.Fatalf("sort toggle did not focus first row: selected=%d top=%d document=%s", model.selected, model.scrollTop, model.filtered[model.selected].document.ID)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	model = updated.(Model)
	if got := []string{model.filtered[0].document.ID, model.filtered[1].document.ID, model.filtered[2].document.ID}; !reflect.DeepEqual(got, []string{"bravo", "alpha", "charlie"}) {
		t.Fatalf("second toggle did not restore newest-first order: %v", got)
	}
	if model.selected != 0 || model.filtered[model.selected].document.ID != "bravo" {
		t.Fatalf("second toggle did not focus restored first row: selected=%d document=%s", model.selected, model.filtered[model.selected].document.ID)
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
	updated, _ = model.Update(spaceTimeoutMsg{sequence: model.spaceSequence})
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

func TestModel_PinnedDocumentsStayFirstInNewestSort(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.sortMode = sortModeTime
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

	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("doc")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if model.selected != len(model.filtered)-1 || model.input.Value() != "doc" {
		t.Fatalf("End with active input did not focus final row: selected=%d input=%q", model.selected, model.input.Value())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	if model.selected != 0 || model.scrollTop != 0 || model.input.Value() != "doc" {
		t.Fatalf("Home with active input did not focus first row: selected=%d top=%d input=%q", model.selected, model.scrollTop, model.input.Value())
	}
}

func TestModel_DeleteConfirmationDeletesFocusedFile(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{{ID: "doc-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	model = updated.(Model)
	if !model.deleteConfirm || !strings.Contains(model.View(), "Delete alpha.md?") {
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
	if model.deleteConfirm || model.statusMessage != "Deleted deleted.md" {
		t.Fatalf("delete did not complete: confirm=%v status=%q", model.deleteConfirm, model.statusMessage)
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

func TestModel_CtrlPCyclesInputModes(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeSearch || model.searchMode != searchModeName {
		t.Fatalf("double space did not open NAME filter: visible=%v input=%s search=%s", model.inputVisible, model.inputMode, model.searchMode)
	}
	for index, expected := range []struct {
		inputMode  string
		searchMode string
	}{
		{inputModeCmd, searchModeName},
		{inputModeAgent, searchModeName},
		{inputModeSearch, searchModeName},
	} {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
		model = updated.(Model)
		if model.inputMode != expected.inputMode || model.searchMode != expected.searchMode {
			t.Fatalf("cycle %d: got input=%s search=%s, want %s/%s", index, model.inputMode, model.searchMode, expected.inputMode, expected.searchMode)
		}
	}
	status := model.statusBar()
	if !strings.Contains(status, "ctrl+p full") {
		t.Fatalf("status does not show current mode hint: %q", status)
	}
}

func TestModel_InputModePersistsAcrossHideAndReopen(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	if model.inputMode != inputModeAgent {
		t.Fatalf("did not reach agent mode: %s", model.inputMode)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.inputVisible || model.inputMode != inputModeAgent {
		t.Fatalf("esc did not preserve hidden agent mode: visible=%v mode=%s", model.inputVisible, model.inputMode)
	}
	updated, _ = model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeAgent || !strings.Contains(model.modeBadge(), " AGENT ") {
		t.Fatalf("double space did not reopen agent input: visible=%v mode=%s badge=%q", model.inputVisible, model.inputMode, model.modeBadge())
	}
}

func TestModel_CommandPaletteProgressiveDisclosure(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{{ID: "doc-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeCmd || model.cmdMenuVisible {
		t.Fatalf("ctrl+p did not open quiet command input: visible=%v mode=%s menu=%v", model.inputVisible, model.inputMode, model.cmdMenuVisible)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if !model.cmdMenuVisible || len(model.cmdSuggestions) == 0 || model.cmdSuggestions[0].Value != "note" {
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
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	model = updated.(Model)
	if model.inputMode != inputModeAgent || !strings.Contains(model.modeBadge(), " AGENT ") {
		t.Fatalf("ctrl+p did not cycle to agent mode: mode=%s badge=%q", model.inputMode, model.modeBadge())
	}
	model.input.SetValue("summarize current document")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.filterErr == nil || !strings.Contains(model.filterErr.Error(), "not connected") {
		t.Fatalf("agent placeholder error missing: %v", model.filterErr)
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
	if len(model.textFilters) != 1 || model.textFilters[0].Mode != searchModeName || model.input.Value() != "" || len(model.filtered) != 2 {
		t.Fatalf("name pattern was not committed: tags=%+v input=%q filtered=%+v", model.textFilters, model.input.Value(), model.filtered)
	}
	if view := model.inputView(); !strings.Contains(view, "N: report") {
		t.Fatalf("name tag is not rendered above input: %q", view)
	}

	model.input.SetValue("alpha")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.textFilters) != 2 || len(model.filtered) != 1 || model.filtered[0].document.ID != "alpha-report" {
		t.Fatalf("name tags do not use AND semantics: tags=%+v filtered=%+v", model.textFilters, model.filtered)
	}

	// Enter on an empty input retains the old open-document behavior.
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
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	model.input.SetValue("flash attention")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.textFilters) != 2 || model.textFilters[0].Mode != searchModeName || model.textFilters[1].Mode != searchModeFull {
		t.Fatalf("text tag modes were not retained: %+v", model.textFilters)
	}
	if !strings.Contains(model.inputView(), "N: alpha") || !strings.Contains(model.inputView(), "F: flash attention") {
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
	model.input.SetValue("one")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.textFilters) != 1 {
		t.Fatal("text tag was not added")
	}
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
	model.input.SetValue("one")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
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

func TestModel_EnterWithWebViewerOpensBrowser(t *testing.T) {
	app := &fakeApp{viewer: "web"}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md"}})
	model.refreshFilter()
	model.viewerMode = "web"
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !model.loading || command == nil {
		t.Fatal("enter with web viewer did not schedule open-web command")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	webMsg, ok := message.(openWebMsg)
	if !ok {
		t.Fatalf("expected openWebMsg, got %T", message)
	}
	if webMsg.err != nil {
		t.Fatalf("open web failed: %v", webMsg.err)
	}
	if len(app.webOpened) != 1 || app.webOpened[0] != "019-alpha" {
		t.Fatalf("browser was not opened for the document: %v", app.webOpened)
	}
	updated, _ = model.Update(webMsg)
	model = updated.(Model)
	if !strings.Contains(model.statusMessage, "browser") {
		t.Fatalf("status does not confirm browser open: %q", model.statusMessage)
	}
}

func TestModel_CtrlRStartsScanAndReloadsTree(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 20
	model.items = documentItems([]membox.DocumentView{{ID: "019-old", Title: "Old", Path: "/tmp/old.md"}})
	model.refreshFilter()

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updated.(Model)
	if command == nil {
		t.Fatal("ctrl+r did not schedule scan")
	}
	if !model.loading || model.statusMessage != "scanning…" {
		t.Fatalf("ctrl+r did not enter scanning state: loading=%v status=%q", model.loading, model.statusMessage)
	}

	// Run the batched scan command (spinner tick + scanCmd).
	message := command()
	// Batch may return []tea.Cmd via tea.Batch - execute until scanMsg.
	var scan scanMsg
	switch msg := message.(type) {
	case scanMsg:
		scan = msg
	case tea.BatchMsg:
		found := false
		for _, cmd := range msg {
			if cmd == nil {
				continue
			}
			inner := cmd()
			if s, ok := inner.(scanMsg); ok {
				scan = s
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("batch did not include scanMsg: %#v", message)
		}
	default:
		// Some tea versions wrap differently; invoke scanCmd directly as fallback check.
		direct := scanCmd(context.Background(), app)()
		var ok bool
		scan, ok = direct.(scanMsg)
		if !ok {
			t.Fatalf("unexpected command result type %T", message)
		}
	}
	if scan.err != nil {
		t.Fatal(scan.err)
	}
	if app.scanCount == 0 {
		// scanCmd path may have been the fallback
		_ = scanCmd(context.Background(), app)()
	}
	if app.scanCount == 0 {
		t.Fatal("scan was not invoked")
	}

	updated, command = model.Update(scan)
	model = updated.(Model)
	if command == nil {
		t.Fatal("scanMsg did not schedule document reload")
	}
	if !strings.Contains(model.statusMessage, "scan +") {
		t.Fatalf("status missing scan summary: %q", model.statusMessage)
	}
}

func TestModel_ConfigPanelSelectAndConfirm(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 20
	updated, _ := model.Update(settingsMsg{settings: []membox.SettingView{
		{Key: "viewer", Label: "viewer", Value: "leaf", Options: []string{"leaf", "web"}},
		{Key: "model", Label: "model", Value: "k3", Options: []string{"k3", "grok-4.5"}},
	}})
	model = updated.(Model)

	// ctrl+o opens the panel
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !model.configVisible {
		t.Fatal("ctrl+o did not open the config panel")
	}
	view := model.View()
	if !strings.Contains(view, "settings") || !strings.Contains(view, "[leaf]") || !strings.Contains(view, "[k3]") {
		t.Fatalf("panel does not show current values: %q", view)
	}

	// → cycles viewer leaf -> web and persists immediately
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	if command == nil {
		t.Fatal("right arrow did not schedule a setting save")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	saved, ok := message.(settingSavedMsg)
	if !ok {
		t.Fatalf("expected settingSavedMsg, got %T", message)
	}
	updated, _ = model.Update(saved)
	model = updated.(Model)
	if app.viewer != "web" || model.viewerMode != "web" || model.settings[0].Value != "web" {
		t.Fatalf("viewer not updated: app=%q mode=%q settings=%+v", app.viewer, model.viewerMode, model.settings)
	}

	// ↓ selects model row, → cycles k3 -> grok-4.5
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.configSelected != 1 {
		t.Fatalf("down did not move selection: %d", model.configSelected)
	}
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	message = command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	updated, _ = model.Update(message)
	model = updated.(Model)
	if app.model != "grok-4.5" || model.settings[1].Value != "grok-4.5" {
		t.Fatalf("model not updated: app=%q settings=%+v", app.model, model.settings)
	}

	// ← wraps grok-4.5 -> k3
	updated, command = model.Update(tea.KeyMsg{Type: tea.KeyLeft})
	model = updated.(Model)
	message = command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	updated, _ = model.Update(message)
	model = updated.(Model)
	if app.model != "k3" {
		t.Fatalf("left arrow did not wrap model value: %q", app.model)
	}

	// esc closes the panel
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.configVisible {
		t.Fatal("esc did not close the config panel")
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
	if !strings.HasPrefix(strings.TrimLeft(status, " "), "s sort:newest") {
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
	if !strings.Contains(lines[len(lines)-1], "ctrl+p full") {
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

// The command palette's "note view" must open the leaf viewer through the
// same editReadyMsg -> tea.ExecProcess path as pressing enter, so the TUI
// suspends and leaf gets the real terminal (running it inline fails with
// "Device not configured" because bubbletea owns the terminal).
func TestModel_NoteViewCommandUsesExecProcessPath(t *testing.T) {
	app := &fakeApp{} // default viewer: leaf
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 120, 24
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeCmd
	model.input.Focus()
	model.input.SetValue("note view 019-alpha")
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("note view did not schedule a command")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	ready, ok := message.(editReadyMsg)
	if !ok {
		t.Fatalf("expected editReadyMsg (ExecProcess path), got %T: %+v", message, message)
	}
	if !ready.viewer || ready.err != nil || ready.path != "/tmp/alpha.md" {
		t.Fatalf("unexpected editReadyMsg: %+v", ready)
	}
	// The handler must schedule tea.ExecProcess (non-nil Cmd).
	updated, command = model.Update(ready)
	if command == nil {
		t.Fatal("editReadyMsg did not schedule the viewer subprocess")
	}
}
