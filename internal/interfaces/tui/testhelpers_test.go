package tui

import (
	"context"
	"fmt"
	"os/exec"

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
	resolved         int
	pins             map[string]bool
	viewer           string
	model            string
	mainPath         string
	webOpened        []string
	scanCount        int
	graph            membox.DocumentGraphView
	searchResults    []membox.SearchResult
	renamedTo        string
	previewPath      string
	readBody         []byte
	readCount        int
	pdfTitle         string
	pdfTitleSelector string

	// Web Companion control knobs for tests.
	webRunning      bool
	webDirtyTabs    int
	webTabs         int
	webMode         string
	webOnExit       string
	webEnsureErr    error
	webStopErr      error
	webLifecycleErr error
	webLeaseErr     error
	webEnsureCalls  int
	webStopCalls    int
	webLeaseCalls   int
	webReleaseCalls int
	webLifecycle    string
}

func (f *fakeApp) AddPath(context.Context, membox.AddPathCommand) (membox.AddPathResult, error) {
	return membox.AddPathResult{}, nil
}
func (f *fakeApp) SearchDocuments(context.Context, membox.SearchDocumentsQuery) ([]membox.SearchResult, error) {
	return f.searchResults, nil
}
func (f *fakeApp) ListDocuments(context.Context, membox.ListDocumentsQuery) ([]membox.DocumentView, error) {
	return []membox.DocumentView{
		{ID: "019-alpha", Title: "Alpha", Path: "/tmp/alpha.md", Status: "active"},
		{ID: "019-beta", Title: "Beta", Path: "/tmp/beta.md", Status: "active"},
	}, nil
}
func (f *fakeApp) SetDocumentReadStatus(_ context.Context, _ membox.SetReadStatusCommand) error {
	return nil
}
func (f *fakeApp) SetDocumentSummary(_ context.Context, command membox.SetSummaryCommand) (membox.DocumentView, error) {
	return membox.DocumentView{ID: command.Selector, Summary: command.Summary}, nil
}
func (f *fakeApp) SummarizeDocument(_ context.Context, selector string) (membox.DocumentView, error) {
	return membox.DocumentView{ID: selector, Summary: "fake summary"}, nil
}
func (f *fakeApp) ReadDocument(context.Context, membox.ReadDocumentQuery) ([]byte, error) {
	f.readCount++
	if f.readBody != nil {
		return f.readBody, nil
	}
	return []byte("body"), nil
}
func (f *fakeApp) ResolveDocumentLocation(_ context.Context, query membox.ResolveLocationQuery) (membox.LocationView, error) {
	f.resolved++
	path := f.previewPath
	if path == "" {
		path = "/tmp/alpha.md"
	}
	return membox.LocationView{DocumentID: query.Selector, Path: path, Status: "active"}, nil
}
func (f *fakeApp) ReindexDocument(context.Context, membox.ReindexDocumentCommand) error { return nil }
func (f *fakeApp) DeleteDocument(_ context.Context, command membox.DeleteDocumentCommand) (membox.DeleteDocumentResult, error) {
	return membox.DeleteDocumentResult{DocumentID: command.Selector, Path: "/tmp/deleted.md", Trashed: true}, nil
}
func (f *fakeApp) TrashSummary(_ context.Context) (membox.TrashSummaryResult, error) {
	return membox.TrashSummaryResult{Count: 1, Bytes: 128}, nil
}
func (f *fakeApp) RenameDocument(_ context.Context, command membox.RenameDocumentCommand) (membox.RenameDocumentResult, error) {
	f.renamedTo = command.NewFilename
	return membox.RenameDocumentResult{DocumentID: command.Selector, Path: "/tmp/renamed.md"}, nil
}
func (f *fakeApp) UpdatePDFMetadata(_ context.Context, command membox.UpdatePDFMetadataCommand) (membox.DocumentView, error) {
	f.pdfTitleSelector = command.Selector
	if command.Title != nil {
		f.pdfTitle = *command.Title
	}
	return membox.DocumentView{ID: command.Selector, Path: "/tmp/original.pdf", Title: f.pdfTitle, MediaType: "application/pdf"}, nil
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
func (f *fakeApp) GetDocumentGraph(_ context.Context, query membox.GetDocumentGraphQuery) (membox.DocumentGraphView, error) {
	graph := f.graph
	graph.Focus = membox.DocumentView{ID: query.Selector, Title: "Focus", Path: "/tmp/focus.md"}
	return graph, nil
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
	webOnExit := f.webOnExit
	if webOnExit == "" {
		webOnExit = "ask"
	}
	return []membox.SettingView{
		{Key: "viewer", Label: "viewer", Value: viewer, Options: []string{"leaf", "web"}},
		{Key: "model", Label: "model", Value: model, Options: []string{"k3", "grok-4.5"}},
		{Key: "main_path", Label: "main path", Value: mainPath, Options: []string{"/tmp/notes", "/tmp/other"}},
		{Key: "web_on_exit", Label: "web on exit", Value: webOnExit, Options: []string{"ask", "stop", "keep"}},
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
	case "web_on_exit":
		if value != "ask" && value != "stop" && value != "keep" {
			return fmt.Errorf("invalid web_on_exit %q", value)
		}
		f.webOnExit = value
		return nil
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
}
func (f *fakeApp) OpenDocumentWeb(_ context.Context, selector string) (string, error) {
	f.webOpened = append(f.webOpened, selector)
	return "http://127.0.0.1:9999/?id=" + selector, nil
}
func (f *fakeApp) webStatusView() membox.WebStatusView {
	return membox.WebStatusView{
		Running:   f.webRunning,
		URL:       "http://127.0.0.1:8787",
		Port:      8787,
		Mode:      f.webMode,
		PID:       1234,
		Tabs:      f.webTabs,
		DirtyTabs: f.webDirtyTabs,
	}
}
func (f *fakeApp) WebStatus(context.Context) (membox.WebStatusView, error) {
	return f.webStatusView(), nil
}
func (f *fakeApp) RestartWebCompanion(ctx context.Context, lifecycle string) (membox.WebStatusView, error) {
	return f.EnsureWebCompanion(ctx, lifecycle)
}

func (f *fakeApp) EnsureWebCompanion(_ context.Context, _ string) (membox.WebStatusView, error) {
	f.webEnsureCalls++
	if f.webEnsureErr != nil {
		return membox.WebStatusView{}, f.webEnsureErr
	}
	f.webRunning = true
	view := f.webStatusView()
	view.Started = true
	return view, nil
}
func (f *fakeApp) StopWeb(context.Context) error {
	f.webStopCalls++
	if f.webStopErr != nil {
		return f.webStopErr
	}
	f.webRunning = false
	return nil
}
func (f *fakeApp) RenewWebLease(context.Context, string) error {
	f.webLeaseCalls++
	return f.webLeaseErr
}
func (f *fakeApp) ReleaseWebLease(context.Context, string) error {
	f.webReleaseCalls++
	return f.webLeaseErr
}
func (f *fakeApp) SetWebLifecycle(_ context.Context, mode string) error {
	if f.webLifecycleErr != nil {
		return f.webLifecycleErr
	}
	f.webLifecycle = mode
	f.webMode = mode
	return nil
}
