package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"membox"
)

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

// Walking the thread tree: arrows move between linked documents, enter opens
// the focused one, and when the viewer exits the graph re-focuses on the
// opened document so the user can keep walking the thread.
func TestModel_ThreadTreeWalkKeepsRootAfterOpen(t *testing.T) {
	app := &fakeApp{graph: membox.DocumentGraphView{
		Outgoing: []membox.DocumentView{{ID: "019-out", Title: "Out", Path: "/tmp/out.md"}},
		Incoming: []membox.DocumentView{{ID: "019-in", Title: "In", Path: "/tmp/in.md"}},
	}}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 120, 30

	cards := []membox.DocumentView{
		{ID: "019-focus", Title: "Focus", Path: "/tmp/focus.md"},
		{ID: "019-out", Title: "Out", Path: "/tmp/out.md"},
		{ID: "019-in", Title: "In", Path: "/tmp/in.md"},
	}
	updated, _ := model.Update(graphFocusMsg{documentID: "019-focus", cards: cards, incoming: 1})
	model = updated.(Model)
	if model.graphSelected != 0 || model.viewMode != viewBoard {
		t.Fatalf("graph focus not initialized: sel=%d mode=%s", model.graphSelected, model.viewMode)
	}

	// Arrow keys walk the tree and highlight the selection.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.graphSelected != 1 {
		t.Fatalf("down did not move selection: %d", model.graphSelected)
	}
	if !strings.Contains(model.View(), "> ") {
		t.Fatal("selected thread card is not highlighted")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.graphSelected != 0 {
		t.Fatalf("up did not move selection back: %d", model.graphSelected)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnd})
	model = updated.(Model)
	if model.graphSelected != len(cards)-1 {
		t.Fatalf("end did not jump to last card: %d", model.graphSelected)
	}

	// Enter opens the focused card through the viewer path.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyHome})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown}) // select 019-out
	model = updated.(Model)
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if command == nil {
		t.Fatal("enter did not schedule viewer command")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	ready, ok := message.(editReadyMsg)
	if !ok {
		t.Fatalf("expected editReadyMsg (ExecProcess path), got %T", message)
	}
	updated, _ = model.Update(ready) // schedules tea.ExecProcess

	// Viewer exits -> the tree keeps its original root; opening a linked
	// document never re-roots the graph, and the selection is preserved.
	updated, command = model.Update(editorDoneMsg{selector: "019-out", viewer: true})
	model = updated.(Model)
	if model.graphFocusID != "019-focus" {
		t.Fatalf("thread tree root changed after opening a linked doc: %q", model.graphFocusID)
	}
	if model.graphSelected != 1 {
		t.Fatalf("selection was lost after viewer exit: %d", model.graphSelected)
	}
	if command != nil {
		t.Fatal("viewer exit should not schedule a graph re-focus")
	}
}

func TestModel_ReindexAfterEditRefreshesFileList(t *testing.T) {
	// After vim wq (note new / e-edit), reindex success must refresh both the
	// preview and the document tree so the new/edited file shows up directly.
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 24

	updated, command := model.Update(reindexMsg{err: nil})
	model = updated.(Model)
	if command == nil {
		t.Fatal("reindex success should schedule preview + list reload")
	}
	var scheduled []tea.Cmd
	if message := command(); message != nil {
		if batch, ok := message.(tea.BatchMsg); ok {
			scheduled = batch
		}
	}
	if len(scheduled) == 0 {
		scheduled = []tea.Cmd{command}
	}
	var docs documentsMsg
	for _, cmd := range scheduled {
		if cmd == nil {
			continue
		}
		msg := cmd()
		if d, ok := msg.(documentsMsg); ok {
			docs = d
		}
	}
	if docs.err != nil {
		t.Fatal(docs.err)
	}
	updated, _ = model.Update(docs)
	model = updated.(Model)
	if len(model.items) != 2 {
		t.Fatalf("tree not refreshed after edit: %d items", len(model.items))
	}
	if model.items[0].document.ID != "019-alpha" {
		t.Fatalf("unexpected tree contents after refresh: %q", model.items[0].document.ID)
	}
}

func TestModel_HideNotesFiltersClippedNotes(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "019-doc", Title: "Article", Path: "/tmp/article.md"},
		{ID: "019-note", Title: "a note", Path: "/tmp/article-note.md"},
		{ID: "019-doc2", Title: "Other", Path: "/tmp/other.md"},
	})

	model.hideNotes = true
	model.refreshFilter()
	if len(model.filtered) != 2 {
		t.Fatalf("hide_notes=on kept %d items, want 2", len(model.filtered))
	}
	for _, it := range model.filtered {
		if strings.HasSuffix(it.filename, "-note.md") {
			t.Fatalf("note file not hidden: %s", it.filename)
		}
	}

	model.hideNotes = false
	model.refreshFilter()
	if len(model.filtered) != 3 {
		t.Fatalf("hide_notes=off kept %d items, want 3", len(model.filtered))
	}

	// Search results honor the same setting.
	results := []membox.SearchResult{{DocumentID: "019-note"}, {DocumentID: "019-doc"}}
	model.hideNotes = true
	got := searchResultItems(model.items, results, nil, nil, model.hideNotes)
	if len(got) != 1 || got[0].document.ID != "019-doc" {
		t.Fatalf("search with hide_notes leaked notes: %+v", got)
	}
}

func TestModel_CtrlHTogglesHiddenNotesAndPersists(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "019-doc", Title: "Article", Path: "/tmp/article.md"},
		{ID: "019-note", Title: "a note", Path: "/tmp/article-note.md"},
	})
	model.hideNotes = false
	model.refreshFilter()

	// Plain h remains available for Vim-style graph navigation and must not
	// toggle the global note visibility setting.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	model = updated.(Model)
	if model.hideNotes || len(model.filtered) != 2 {
		t.Fatalf("plain h toggled notes: hideNotes=%v filtered=%d", model.hideNotes, len(model.filtered))
	}

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	model = updated.(Model)
	if !model.hideNotes || len(model.filtered) != 1 {
		t.Fatalf("ctrl+h did not hide notes: hideNotes=%v filtered=%d", model.hideNotes, len(model.filtered))
	}
	if command == nil {
		t.Fatal("ctrl+h did not persist the setting")
	}
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[0]()
	}
	if saved, ok := message.(settingSavedMsg); !ok || saved.key != "hide_notes" || saved.value != "on" {
		t.Fatalf("unexpected setting save: %+v", message)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	model = updated.(Model)
	if model.hideNotes || len(model.filtered) != 2 {
		t.Fatalf("second ctrl+h did not unhide notes: hideNotes=%v filtered=%d", model.hideNotes, len(model.filtered))
	}
}

func TestModel_HelpModalTogglesWithQuestionMark(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 70
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(Model)
	if !model.helpVisible {
		t.Fatal("? did not open the help modal")
	}
	view := model.View()
	if !strings.Contains(view, "Keyboard Help") {
		t.Fatalf("help modal missing title")
	}
	for _, category := range []string{"Browse", "Filter & commands", "General"} {
		if !strings.Contains(view, category) {
			t.Fatalf("help modal missing category %q", category)
		}
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.helpVisible {
		t.Fatal("esc did not close the help modal")
	}
}

func TestModel_HelpDoesNotOpenWhileTyping(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 30
	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	updated, _ := model.Update(space)
	model = updated.(Model)
	updated, _ = model.Update(space)
	model = updated.(Model)
	if !model.inputVisible {
		t.Fatal("double space did not open the filter input")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(Model)
	if model.helpVisible {
		t.Fatal("? opened help while the filter input was active")
	}
	if model.input.Value() != "?" {
		t.Fatalf("? was not typed into the filter input: %q", model.input.Value())
	}
}

func TestModel_HelpModalKeepsUnderlyingContent(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 40
	model.items = documentItems([]membox.DocumentView{{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()

	base := model.View()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	model = updated.(Model)
	withHelp := model.View()

	if !strings.Contains(withHelp, "Keyboard Help") {
		t.Fatal("help modal missing")
	}
	// Rows outside the modal rect are untouched: first content row and the
	// status bar stay exactly as rendered without the panel.
	baseLines := strings.Split(base, "\n")
	helpLines := strings.Split(withHelp, "\n")
	if len(helpLines) != len(baseLines) {
		t.Fatalf("line count changed: %d vs %d", len(helpLines), len(baseLines))
	}
	if helpLines[0] != baseLines[0] {
		t.Fatalf("top row modified by modal:\nbefore %q\nafter  %q", baseLines[0], helpLines[0])
	}
	if !strings.Contains(helpLines[0], "alpha.md") {
		t.Fatalf("underlying tree content lost: %q", helpLines[0])
	}
	last := len(baseLines) - 1
	if helpLines[last] != baseLines[last] {
		t.Fatalf("status bar modified by modal:\nbefore %q\nafter  %q", baseLines[last], helpLines[last])
	}
}

func threadTestModel() Model {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 40
	model.graphFocusID = "019-alpha"
	model.graphCards = []membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019-note-one", Title: "Raft note", Path: "/tmp/raft-note.md", RelativePath: "raft-note.md"},
		{ID: "019-note-two", Title: "Quorum note", Path: "/tmp/quorum-note.md", RelativePath: "quorum-note.md"},
	}
	model.graphIncoming = 1
	return model
}

func TestModel_ThreadViewFiltersByString(t *testing.T) {
	model := threadTestModel()

	rows, _ := model.graphRows()
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "raft-note.md") || !strings.Contains(joined, "quorum-note.md") {
		t.Fatalf("unfiltered thread missing cards: %q", joined)
	}

	// A committed string filter narrows the thread; the focus card always stays.
	model.textFilters = []textFilter{{Value: "raft", Mode: searchModeName, Sequence: 1}}
	model.refreshFilter()
	rows, _ = model.graphRows()
	joined = strings.Join(rows, "\n")
	if !strings.Contains(joined, "raft-note.md") || strings.Contains(joined, "quorum-note.md") {
		t.Fatalf("filter did not narrow the thread: %q", joined)
	}
	if !strings.Contains(joined, "● focus") || !strings.Contains(joined, "alpha.md") {
		t.Fatalf("focus card lost while filtering: %q", joined)
	}

	// Selection is clamped into the visible cards.
	model.graphSelected = 5
	model.refreshFilter()
	if model.graphSelected != 1 {
		t.Fatalf("graphSelected not clamped to visible cards: %d", model.graphSelected)
	}

	// Navigation stays inside the filtered set.
	model.moveGraphSelection("down")
	model.moveGraphSelection("down")
	if model.graphSelected != 1 {
		t.Fatalf("navigation escaped the filtered thread: %d", model.graphSelected)
	}
}

func TestModel_ThreadViewFiltersLiveWhileTyping(t *testing.T) {
	model := threadTestModel()
	model.inputVisible = true
	model.searchMode = searchModeName
	model.input.SetValue("quorum")

	indices := model.visibleGraphIndices()
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 2 {
		t.Fatalf("draft filter visible indices = %v, want [0 2]", indices)
	}
	rows, _ := model.graphRows()
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "raft-note.md") || !strings.Contains(joined, "quorum-note.md") {
		t.Fatalf("draft filter not applied live: %q", joined)
	}
	// Incoming direction survives filtering (quorum is the incoming card).
	if !strings.Contains(joined, "←") {
		t.Fatalf("incoming direction lost after filtering: %q", joined)
	}
}

func TestModel_ThreadViewFullModeFiltersByBody(t *testing.T) {
	// FTS says only the quorum note's body matches the query.
	app := &fakeApp{searchResults: []membox.SearchResult{{DocumentID: "019-note-two"}}}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 40
	model.graphFocusID = "019-alpha"
	model.graphCards = []membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019-note-one", Title: "Raft note", Path: "/tmp/raft-note.md", RelativePath: "raft-note.md"},
		{ID: "019-note-two", Title: "Quorum note", Path: "/tmp/quorum-note.md", RelativePath: "quorum-note.md"},
	}
	model.graphIncoming = 1
	model.textFilters = []textFilter{{Value: "quorum leader election", Mode: searchModeFull, Sequence: 1}}

	// Before the async FTS result arrives, full mode must not fall back to
	// filename matching.
	if indices := model.visibleGraphIndices(); len(indices) != 3 {
		t.Fatalf("pending full search should keep all cards visible: %v", indices)
	}

	message := model.threadSearchCmd(model.fullTextFilterQuery(), model.filterExact)()
	updated, _ := model.Update(message)
	model = updated.(Model)

	// "raft-note.md" substring-matches nothing here; only the body FTS hit
	// survives.
	rows, _ := model.graphRows()
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "raft-note.md") || !strings.Contains(joined, "quorum-note.md") {
		t.Fatalf("full mode did not filter by body search results: %q", joined)
	}
	if model.graphSelected > len(model.visibleGraphIndices())-1 {
		t.Fatalf("selection escaped the filtered thread: %d", model.graphSelected)
	}
}

func TestModel_RenameCommandKeepsUUIDAndRefreshes(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 30
	model.items = documentItems([]membox.DocumentView{{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"}})
	model.refreshFilter()
	model.inputVisible = true
	model.inputActive = true
	model.inputMode = inputModeCmd
	model.input.SetValue("rename better-name.md")

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	renamed, ok := message.(renamedMsg)
	if !ok {
		t.Fatalf("expected renamedMsg, got %T", message)
	}
	if renamed.documentID != "019-alpha" {
		t.Fatalf("rename ran against %q, want the selected document", renamed.documentID)
	}
	if app.renamedTo != "better-name.md" {
		t.Fatalf("rename did not pass the new filename: %q", app.renamedTo)
	}
	updated, _ = model.Update(renamed)
	model = updated.(Model)
	if model.statusMessage != "Renamed renamed.md" {
		t.Fatalf("status after rename = %q", model.statusMessage)
	}

	// Usage errors: no selection and no filename.
	model.inputVisible = true
	model.inputActive = true
	model.inputMode = inputModeCmd
	model.selected = 99
	model.input.SetValue("rename other.md")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.filterErr == nil {
		t.Fatal("rename without a selectable document should report a usage error")
	}
}

func TestModel_RKeyOpensPrefilledRenameInput(t *testing.T) {
	app := &fakeApp{}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 30
	model.items = documentItems([]membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
	})
	model.refreshFilter()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model = updated.(Model)
	if !model.inputVisible || model.inputMode != inputModeCmd {
		t.Fatalf("r should open the command input: visible=%v mode=%q", model.inputVisible, model.inputMode)
	}
	want := "rename alpha.md"
	if model.input.Value() != want {
		t.Fatalf("r should prefill %q, got %q", want, model.input.Value())
	}
	// Enter executes the prefilled rename against the selected document.
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	message := command()
	if batch, ok := message.(tea.BatchMsg); ok {
		message = batch[1]()
	}
	if _, ok := message.(renamedMsg); !ok {
		t.Fatalf("expected renamedMsg from prefilled r, got %T", message)
	}
	if app.renamedTo != "alpha.md" {
		t.Fatalf("prefilled rename passed %q, want alpha.md", app.renamedTo)
	}
}

func TestModel_ColonOpensCommandPaletteDirectly(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	model = updated.(Model)
	if !model.inputVisible || !model.inputActive || model.inputMode != inputModeCmd {
		t.Fatalf(": did not open the command palette: visible=%v active=%v mode=%s", model.inputVisible, model.inputActive, model.inputMode)
	}
	if !strings.Contains(model.modeBadge(), " CMD ") {
		t.Fatalf("badge does not show CMD mode: %q", model.modeBadge())
	}
	if model.input.Value() != "" {
		t.Fatalf("command input should start empty: %q", model.input.Value())
	}
}

func TestModel_CtrlKOpensAgentInputDirectly(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	model = updated.(Model)
	if !model.inputVisible || !model.inputActive || model.inputMode != inputModeAgent {
		t.Fatalf("ctrl+k did not open the agent input: visible=%v active=%v mode=%s", model.inputVisible, model.inputActive, model.inputMode)
	}
	if !strings.Contains(model.modeBadge(), " AGENT ") {
		t.Fatalf("badge does not show AGENT mode: %q", model.modeBadge())
	}
}
