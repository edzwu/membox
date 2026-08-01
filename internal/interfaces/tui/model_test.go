package tui

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"membox"
)

type fakeLauncher struct{}

func (fakeLauncher) EditorCommand(context.Context, string) (*exec.Cmd, error) {
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
	return []membox.SearchResult{{DocumentID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", Snippet: "alpha"}}, nil
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

func TestModel_TUI002_StaleSearchResultIsIgnored(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.searchSequence = 2
	model.items = []item{{kind: documentItem, document: membox.DocumentView{ID: "current"}, match: "current"}}
	model.filtered = append([]item(nil), model.items...)
	updated, _ := model.Update(searchMsg{sequence: 1, results: []membox.SearchResult{{DocumentID: "stale"}}})
	got := updated.(Model)
	if len(got.filtered) != 1 || got.filtered[0].document.ID != "current" {
		t.Fatalf("stale result replaced current state: %+v", got.filtered)
	}
}

func TestModel_TUI_DoubleSpaceTimeoutOpensFilter(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.spaceSequence = 1
	updated, _ := model.Update(spaceTimeoutMsg{sequence: 1})
	got := updated.(Model)
	if !got.filterActive {
		t.Fatal("double-space timeout did not open filter")
	}
	if !got.loading {
		t.Fatal("filter did not load full document list")
	}
}

func TestModel_TUI_FilterMatchesWordsLikeFZF(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.filterActive = true
	model.items = documentItems([]membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/repo/docs/alpha.md", Status: "active"},
		{ID: "019-beta", Title: "Beta", Path: "/repo/docs/beta.md", Status: "active"},
	})
	model.input.SetValue("docs beta")
	model.refreshFilter()
	if len(model.filtered) != 1 || model.filtered[0].document.ID != "019-beta" {
		t.Fatalf("unexpected filter: %+v", model.filtered)
	}
}

func TestResolveEditorCmd_TUI002_PerformsIOInsideCommand(t *testing.T) {
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

func TestModel_TUI_InputIsAtBottomWithPiLikeBorder(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 100, 24
	model.resize()
	view := model.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("view too short: %d", len(lines))
	}
	border := lines[len(lines)-3]
	input := lines[len(lines)-2]
	footer := lines[len(lines)-1]
	if !strings.Contains(border, "╭") {
		t.Fatalf("missing bottom input border: %q", border)
	}
	if !strings.Contains(input, "search") || !strings.Contains(input, "❯") {
		t.Fatalf("input is not at bottom: %q", input)
	}
	if !strings.Contains(footer, "space×2 filter") {
		t.Fatalf("missing filter hint: %q", footer)
	}
}

func TestModel_TUI002_ResponsivePreviewWidth(t *testing.T) {
	model := New(context.Background(), &fakeApp{}, fakeLauncher{})
	model.width, model.height = 80, 24
	model.resize()
	narrow := model.preview.Width
	model.width = 160
	model.resize()
	if model.preview.Width <= narrow {
		t.Fatalf("wide preview=%d narrow=%d", model.preview.Width, narrow)
	}
}
