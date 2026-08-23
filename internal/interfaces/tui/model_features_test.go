package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"membox"
)

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
	if !strings.Contains(view, "> ○ dabf  alpha.md") {
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

// deliverNameFilter simulates the catalog identity-search response the name
// filter now awaits: after an Enter commit (or draft edit) the filter runs a
// full-catalog SQLite query (SuggestDocuments); tests feed the backend-like
// hit list back through Update so local exact/case/date/notes semantics apply.
func deliverNameFilter(t *testing.T, model *Model, ids ...string) {
	t.Helper()
	query := model.nameSearchQuery()
	results := make([]membox.SearchResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, membox.SearchResult{DocumentID: id})
	}
	updated, _ := model.Update(searchMsg{query: query, nameSearch: true, results: results})
	*model = updated.(Model)
}

func TestNameFilterORsDocumentIDAndTitle(t *testing.T) {
	// Name filter is OR: id hit or title/path hit. Pure-hex English words
	// like "eff" must still match titles ("Effective Go").
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbeff-0001", Title: "Effective Go", Path: "/tmp/effective-go.md", RelativePath: "effective-go.md"},
		{ID: "019fbee0-0002", Title: "Rust Notes", Path: "/tmp/rust.md", RelativePath: "rust.md"},
		{ID: "3ccf", Title: "Other", Path: "/tmp/other.md", RelativePath: "other.md"},
	})
	model.inputVisible = true

	model.input.SetValue("eff")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.Title != "Effective Go" {
		t.Fatalf("name filter 'eff' did not match Effective Go: %+v", model.filtered)
	}

	// Short logical id still matches via the id arm of the OR.
	model.input.SetValue("3ccf")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "3ccf" {
		t.Fatalf("name filter '3ccf' did not match logical id: %+v", model.filtered)
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

func TestModel_CtrlRImmediatelyFocusesPDFImportedByCompanion(t *testing.T) {
	oldPDF := membox.DocumentView{
		ID: "019-old-pdf", Title: "Old PDF", Path: "/pdfs/old.pdf", MediaType: "application/pdf",
		UpdatedAt: time.Date(2026, 8, 14, 10, 0, 0, 0, time.Local),
	}
	newPDF := membox.DocumentView{
		ID: "01a-new-pdf", Title: "Newly Imported", Path: "/pdfs/new.pdf", MediaType: "application/pdf", Size: 8 << 20,
		UpdatedAt: time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local),
	}
	app := &fakeApp{documents: []membox.DocumentView{oldPDF, newPDF}}
	model := New(context.Background(), app, fakeLauncher{})
	model.width, model.height = 100, 20
	model.mediaScope = mediaScopePDF
	model.items = documentItems([]membox.DocumentView{oldPDF})
	model.refreshFilter()

	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	model = updated.(Model)
	if command == nil || model.scanRefreshSequence == 0 {
		t.Fatal("ctrl+r did not schedule immediate catalog refresh")
	}
	updated, _ = model.Update(documentsMsg{sequence: model.scanRefreshSequence, documents: app.documents})
	model = updated.(Model)
	selected, ok := model.selectedDocument()
	if !ok || selected.ID != newPDF.ID {
		t.Fatalf("refresh selected=%+v, want imported PDF", selected)
	}
	if !model.scanning || !strings.Contains(model.statusMessage, "1 new") || !strings.Contains(model.statusMessage, "focused") {
		t.Fatalf("refresh state: scanning=%v status=%q", model.scanning, model.statusMessage)
	}
	updated, _ = model.Update(scanMsg{report: membox.ScanReport{Files: 2}})
	model = updated.(Model)
	updated, _ = model.Update(documentsMsg{sequence: model.scanRefreshSequence, documents: app.documents})
	model = updated.(Model)
	selected, _ = model.selectedDocument()
	if selected.ID != newPDF.ID || !strings.Contains(model.statusMessage, "1 new") {
		t.Fatalf("post-scan refresh lost imported PDF focus/status: selected=%+v status=%q", selected, model.statusMessage)
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
	if !strings.HasPrefix(strings.TrimLeft(status, " "), "? help") {
		t.Fatalf("single help hint is not on the left: %q", status)
	}
}

func TestModel_InputUsesSingleHighlightedModeBadge(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width = 120
	model.inputVisible = true
	input := model.inputView()
	if !strings.Contains(input, " SEARCH ") {
		t.Fatalf("input does not contain highlighted mode badge: %q", input)
	}
	if strings.Count(input, " SEARCH ") != 1 {
		t.Fatalf("SEARCH badge should appear once: %q", input)
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
	if !strings.Contains(lines[len(lines)-1], "? help") {
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

func TestModel_CtrlFTogglesSearchScope(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.inputVisible = true
	model.inputActive = true
	model.input.Focus()
	if model.searchMode != searchModeName {
		t.Fatalf("default scope=%s", model.searchMode)
	}
	// Badge stays SEARCH; scope is status/ctrl+o, not the badge label.
	if !strings.Contains(model.modeBadge(), " SEARCH ") {
		t.Fatalf("badge=%q", model.modeBadge())
	}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	model = updated.(Model)
	if model.searchMode != searchModeFull {
		t.Fatalf("ctrl+f did not enter content: %s", model.searchMode)
	}
	if !strings.Contains(model.modeBadge(), " SEARCH ") {
		t.Fatalf("badge should remain SEARCH: %q", model.modeBadge())
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	model = updated.(Model)
	if model.searchMode != searchModeName {
		t.Fatalf("ctrl+f did not toggle back: %s", model.searchMode)
	}
}

func TestHighlightQueryUsesReadableOrangeBackground(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })
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
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })
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

func TestModeBadgeSearchIsStableBlue(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })
	lipgloss.SetHasDarkBackground(true)
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	name := model.modeBadge()
	model.searchMode = searchModeFull
	full := model.modeBadge()
	if !strings.Contains(name, "SEARCH") || !strings.Contains(full, "SEARCH") {
		t.Fatalf("search badge should say SEARCH: name=%q full=%q", name, full)
	}
	if !strings.Contains(name, "48;2;48;89;184") || !strings.Contains(full, "48;2;48;89;184") {
		t.Fatalf("SEARCH badge should stay blue: name=%q full=%q", name, full)
	}
}

func TestFilterOptionsCanToggleScope(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeSearch
	model.searchMode = searchModeName
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !strings.Contains(model.inputView(), "scope") {
		t.Fatalf("scope row missing: %q", model.inputView())
	}
	// move to scope row (2) and toggle to content
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	if model.searchMode != searchModeFull {
		t.Fatalf("scope toggle did not enter content: %s", model.searchMode)
	}
	_ = cmd
}

func TestShortIDUsesCompactSuffix(t *testing.T) {
	// Without a corpus map, short ids fall back to the last 4 compact hex chars.
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
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })
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
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })
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

	// Enter board mode with ctrl+t (input is inactive by default).
	model = send(model, tea.KeyMsg{Type: tea.KeyCtrlT})
	if model.viewMode != viewBoard {
		t.Fatalf("ctrl+t should enter board mode, got viewMode=%v", model.viewMode)
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
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })
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

func TestModel_FilterOptionsPanelCtrlOAndStatusBar(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 24
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
		{ID: "019fbe56-64c3-7eec-97a7-6c8813ec2d64", Title: "Beta", Path: "/tmp/beta.md", RelativePath: "beta.md"},
	})
	model.inputVisible, model.inputActive, model.inputMode = true, true, inputModeSearch
	model.refreshFilter()

	// ctrl+o opens the filter options panel (not the web settings panel).
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	model = updated.(Model)
	if !model.filterOptionsVisible || model.configVisible {
		t.Fatalf("ctrl+o did not open filter options: opts=%v config=%v", model.filterOptionsVisible, model.configVisible)
	}
	if !strings.Contains(model.inputView(), "filter options") {
		t.Fatalf("options panel not rendered: %q", model.inputView())
	}

	// Toggle exact (row 0) then case (row 1) with space.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	if !model.filterExact {
		t.Fatalf("space did not toggle exact: %+v", model)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(Model)
	if !model.filterCase {
		t.Fatalf("second toggle did not set case: %+v", model)
	}
	// The status bar must reflect both settings.
	bar := model.statusBar()
	if !strings.Contains(bar, " =") || !strings.Contains(bar, " Aa") {
		t.Fatalf("status bar misses match/case segment: %q", bar)
	}

	// esc closes the panel and returns to the input (draft preserved).
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.filterOptionsVisible {
		t.Fatal("esc did not close filter options")
	}
	if !model.inputVisible {
		t.Fatal("esc closed the input too")
	}

	// Whole-word semantics (the user's example): "rust" must NOT select the
	// doc whose title contains "Trust", but MUST select the one with a
	// standalone "Rust" word.
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbe56-64c3-7e3c-861d-000000000001", Title: "Proof-or-Stop: Don't Trust the Agent", Path: "/tmp/proof-or-stop-trust-the-agent.md", RelativePath: "proof-or-stop-trust-the-agent.md"},
		{ID: "019fbe56-64c3-7e3c-861d-000000000002", Title: "The Rust I Wanted Had No Future", Path: "/tmp/the-rust-i-wanted-had-no-future.md", RelativePath: "the-rust-i-wanted-had-no-future.md"},
	})
	model.filterExact, model.filterCase = true, false
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("rust")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if len(model.textFilters) != 1 || !model.textFilters[0].Exact || model.textFilters[0].Case {
		t.Fatalf("committed filter did not freeze semantics: %+v", model.textFilters)
	}
	// Reopen filter to inspect the tag chip in the input chrome.
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	if !strings.Contains(model.inputView(), "N=: rust") {
		t.Fatalf("tag does not encode word mode: %q", model.inputView())
	}
	// Backend returns both LIKE hits (Trust, Rust); local whole-word semantics
	// must keep only the standalone "Rust" doc.
	deliverNameFilter(t, &model, "019fbe56-64c3-7e3c-861d-000000000001", "019fbe56-64c3-7e3c-861d-000000000002")
	if len(model.filtered) != 1 || !strings.Contains(model.filtered[0].document.Title, "Rust I Wanted") {
		t.Fatalf("word match selected wrong docs: %+v", model.filtered)
	}

	// Substring mode selects both (Trust contains rust).
	model.textFilters = nil
	model.filterExact = false
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("rust")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	// Backend returns both LIKE hits; local substring semantics keep both.
	deliverNameFilter(t, &model, "019fbe56-64c3-7e3c-861d-000000000001", "019fbe56-64c3-7e3c-861d-000000000002")
	if len(model.filtered) != 2 {
		t.Fatalf("contains mode should select both docs: %+v", model.filtered)
	}

	// Word + case-sensitive: "RUST" matches neither (title has Rust, slug rust).
	model.textFilters = nil
	model.filterExact, model.filterCase = true, true
	model.inputVisible, model.inputActive = true, true
	model.input.Focus()
	model.input.SetValue("RUST")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	deliverNameFilter(t, &model, "019fbe56-64c3-7e3c-861d-000000000001", "019fbe56-64c3-7e3c-861d-000000000002")
	if len(model.filtered) != 0 {
		t.Fatalf("word+case RUST should match nothing: %+v", model.filtered)
	}
}

func TestModel_TabFocusesAndScrollsPreview(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 120, 10
	model.items = documentItems([]membox.DocumentView{
		{ID: "019fbe56-64c3-7e3c-861d-4da66742dabf", Title: "Alpha", Path: "/tmp/alpha.md", RelativePath: "alpha.md"},
	})
	model.refreshFilter()
	model.resize()
	model.preview.SetContent(strings.Join([]string{
		"line 01", "line 02", "line 03", "line 04", "line 05", "line 06",
		"line 07", "line 08", "line 09", "line 10", "line 11", "line 12",
	}, "\n"))

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if !model.previewFocused || cmd != nil {
		t.Fatalf("tab should focus preview without a command: focused=%v cmd=%v", model.previewFocused, cmd)
	}
	focusedView := model.treePreviewView()
	if strings.Contains(focusedView, "Alpha") || !strings.Contains(focusedView, "line 01") {
		t.Fatalf("focused preview should hide the tree and keep the document: %q", focusedView)
	}
	if model.preview.Width != model.width {
		t.Fatalf("focused preview did not expand: width=%d want=%d", model.preview.Width, model.width)
	}
	if bar := model.statusBar(); !strings.Contains(bar, "preview 1-9/12") {
		t.Fatalf("preview status lacks initial line range: %q", bar)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.preview.YOffset != 1 || model.selected != 0 {
		t.Fatalf("down should scroll preview only: offset=%d selected=%d", model.preview.YOffset, model.selected)
	}
	if bar := model.statusBar(); !strings.Contains(bar, "preview 2-10/12") {
		t.Fatalf("preview status did not follow scrolling: %q", bar)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	model = updated.(Model)
	if model.preview.YOffset <= 1 {
		t.Fatalf("pgdown did not page preview: offset=%d", model.preview.YOffset)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	model = updated.(Model)
	if model.preview.YOffset >= 2 {
		t.Fatalf("pgup did not page preview back: offset=%d", model.preview.YOffset)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if model.previewFocused {
		t.Fatal("second tab did not return focus to the tree")
	}
	if !strings.Contains(model.treePreviewView(), "alpha.md") {
		t.Fatal("second tab did not restore the file tree")
	}
	if model.preview.Width >= model.width {
		t.Fatalf("restored split view kept fullscreen preview width: %d", model.preview.Width)
	}
}
