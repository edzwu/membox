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
	if !strings.Contains(status, "? help") {
		t.Fatalf("status does not show the single help hint: %q", status)
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
	if model.inputMode != inputModeAgent || !strings.Contains(model.modeBadge(), "AGENT") {
		t.Fatalf("ctrl+p did not cycle to agent mode: mode=%s badge=%q", model.inputMode, model.modeBadge())
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
