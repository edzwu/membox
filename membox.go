package membox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/application"
	"membox/internal/application/port"
	"membox/internal/bootstrap"
	"membox/internal/domain/catalog"
	"membox/internal/web"
	"membox/internal/web/backend"
	"membox/internal/web/companion"
)

type Config struct {
	Home         string
	DatabasePath string
}

func DefaultConfig() (Config, error) {
	home := os.Getenv("MEMBOX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		home = filepath.Join(userHome, ".membox")
	}
	return Config{Home: home, DatabasePath: filepath.Join(home, "membox.db")}, nil
}

type Box struct {
	service   *application.Service
	webServer *web.Server
	home      string
}

func Open(config Config) (*Box, error) {
	if config.DatabasePath == "" {
		if config.Home == "" {
			return nil, errors.New("membox home or database path is required")
		}
		config.DatabasePath = filepath.Join(config.Home, "membox.db")
	}
	if config.Home == "" {
		config.Home = filepath.Dir(config.DatabasePath)
	}
	service, err := bootstrap.Open(config.DatabasePath)
	if err != nil {
		return nil, err
	}
	return &Box{service: service, home: config.Home}, nil
}

func (b *Box) Close() error {
	var shutdownErr error
	if b.webServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		shutdownErr = b.webServer.Shutdown(ctx)
		cancel()
	}
	return errors.Join(shutdownErr, b.service.Close())
}

// WebServer returns a localhost HTTP server that renders indexed Markdown in
// the browser using the embedded Miru reader.
func (b *Box) WebServer() *web.Server { return web.NewServer(b.service) }

// DefaultBridgePort is the preferred fixed port so the browser extension can
// keep a stable pairing URL while the Web Companion is running.
const DefaultBridgePort = companion.DefaultPort

// WebStatusView is the control-plane snapshot of the Web Companion shown in
// the TUI status bar and config panel.
type WebStatusView struct {
	Running   bool      `json:"running"`
	URL       string    `json:"url,omitempty"`
	Port      int       `json:"port,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	PID       int       `json:"pid,omitempty"`
	Tabs      int       `json:"tabs"`
	DirtyTabs int       `json:"dirty_tabs"`
	StartedAt time.Time `json:"started_at,omitempty"`
	// Started reports that the last Ensure call spawned a new companion.
	Started bool `json:"started"`
}

func webStatusView(status companion.Status) WebStatusView {
	return WebStatusView{
		Running:   status.Running,
		URL:       status.BaseURL,
		Port:      status.Port,
		Mode:      status.Mode,
		PID:       status.PID,
		Tabs:      status.Tabs,
		DirtyTabs: status.DirtyTabs,
		StartedAt: status.StartedAt,
		Started:   status.Started,
	}
}

// WebStatus probes the Web Companion without starting it.
func (b *Box) WebStatus(ctx context.Context) (WebStatusView, error) {
	status, err := companion.Probe(ctx, b.home)
	return webStatusView(status), err
}

// webLifecycleFromSetting resolves the stored "web on exit" policy into the
// companion lifecycle used when the caller does not pick one explicitly.
func (b *Box) webLifecycleFromSetting(ctx context.Context) string {
	value, err := b.service.GetSetting(ctx, application.SettingWebOnExit)
	if err == nil && value == application.OnExitKeep {
		return companion.LifecycleKeep
	}
	return companion.LifecycleSession
}

// EnsureWebCompanion connects to the running Web Companion or starts one.
// An empty lifecycle defers to the stored "web on exit" policy. The Box never
// embeds the server itself: one companion process per home owns the port, the
// bridge file, and web-originated writes.
func (b *Box) EnsureWebCompanion(ctx context.Context, lifecycle string) (WebStatusView, error) {
	if strings.TrimSpace(lifecycle) == "" {
		lifecycle = b.webLifecycleFromSetting(ctx)
	}
	status, _, err := companion.Ensure(ctx, b.home, lifecycle, 0, nil)
	return webStatusView(status), err
}

// RestartWebCompanion stops any existing companion for this home (including
// orphans on the preferred extension port) and starts a fresh one. The TUI
// calls this on entry so the browser extension keeps a stable :8787 pairing
// and Agent workers are owned by the current process generation.
func (b *Box) RestartWebCompanion(ctx context.Context, lifecycle string) (WebStatusView, error) {
	if strings.TrimSpace(lifecycle) == "" {
		lifecycle = b.webLifecycleFromSetting(ctx)
	}
	status, err := companion.Restart(ctx, b.home, lifecycle, 0, nil)
	return webStatusView(status), err
}

// StopWeb asks the Web Companion to shut down gracefully and waits for it.
func (b *Box) StopWeb(ctx context.Context) error {
	return companion.Stop(ctx, b.home)
}

// RenewWebLease registers or heartbeats this TUI as one independent session
// controller. Multiple TUIs can safely share a session companion.
func (b *Box) RenewWebLease(ctx context.Context, controller string) error {
	return companion.SetControllerLease(ctx, b.home, controller, false)
}

// ReleaseWebLease releases only this TUI's ownership; it is not a global stop.
func (b *Box) ReleaseWebLease(ctx context.Context, controller string) error {
	return companion.SetControllerLease(ctx, b.home, controller, true)
}

// SetWebLifecycle switches a running companion between session and keep mode.
func (b *Box) SetWebLifecycle(ctx context.Context, mode string) error {
	return companion.SetLifecycle(ctx, b.home, mode)
}

// StartWebServer keeps the historical API: ensure the Web Companion is up and
// return its base URL. The port argument is accepted for compatibility; the
// companion owns port selection.
func (b *Box) StartWebServer(ctx context.Context, _ int) (string, error) {
	status, err := b.EnsureWebCompanion(ctx, "")
	if err != nil {
		return "", err
	}
	return status.URL, nil
}

// BridgeInfo returns the live bridge base URL and token from bridge.json.
func (b *Box) BridgeInfo() (baseURL, token string) {
	bridge, err := backend.ReadBridgeFile(b.home)
	if err != nil {
		return "", ""
	}
	return bridge.BaseURL, bridge.Token
}

// GetViewer returns the configured viewer mode (leaf or web).
func (b *Box) GetViewer(ctx context.Context) (string, error) {
	return b.service.GetViewer(ctx)
}

// SetViewer persists the viewer mode (leaf or web).
func (b *Box) SetViewer(ctx context.Context, viewer string) error {
	return b.service.SetViewer(ctx, viewer)
}

// SettingView describes one configurable option for the config UI.
type SettingView struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Value   string   `json:"value"`
	Options []string `json:"options"`
}

// ListSettings returns every configurable option with its current value.
func (b *Box) ListSettings(ctx context.Context) ([]SettingView, error) {
	settings, err := b.service.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SettingView, 0, len(settings))
	for _, setting := range settings {
		out = append(out, SettingView{Key: setting.Key, Label: setting.Label, Value: setting.Value, Options: setting.Options})
	}
	return out, nil
}

// SetSetting validates and persists one configurable option.
func (b *Box) SetSetting(ctx context.Context, key, value string) error {
	return b.service.SetSetting(ctx, key, value)
}

// OpenDocumentWeb ensures the Web Companion is running and returns the
// browser URL rendering the given document.
func (b *Box) OpenDocumentWeb(ctx context.Context, selector string) (string, error) {
	document, absolute, err := b.service.ResolveDocument(ctx, selector)
	if err != nil {
		return "", err
	}
	if document.Status != catalog.DocumentActive {
		return "", fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	status, err := b.EnsureWebCompanion(ctx, "")
	if err != nil {
		return "", err
	}
	return status.URL + "/?id=" + string(document.ID), nil
}

type PathView struct {
	ID         int64      `json:"id"`
	Path       string     `json:"path"`
	Documents  int        `json:"documents"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	LastScanAt *time.Time `json:"last_scan_at"`
	LastError  string     `json:"last_error,omitempty"`
}

type AddPathCommand struct{ Directory string }
type AddPathResult struct {
	Path          PathView   `json:"path"`
	AlreadyExists bool       `json:"already_exists"`
	Scan          ScanReport `json:"scan"`
}

type RemovePathCommand struct{ Selector string }
type RemovePathResult struct {
	PathID    int64 `json:"path_id"`
	Documents int   `json:"documents"`
}

type ScanPathsCommand struct {
	Selector        string
	TimestampSource string
}
type ScanReport struct {
	Paths               int    `json:"paths"`
	Files               int    `json:"files"`
	Added               int    `json:"added"`
	Updated             int    `json:"updated"`
	Renamed             int    `json:"renamed"`
	Unchanged           int    `json:"unchanged"`
	Missing             int    `json:"missing"`
	PossibleRenames     int    `json:"possible_renames"`
	Errors              int    `json:"errors"`
	TimestampSource     string `json:"timestamp_source,omitempty"`
	GitPaths            int    `json:"git_paths,omitempty"`
	TimestampsUpdated   int    `json:"timestamps_updated,omitempty"`
	TimestampsUnchanged int    `json:"timestamps_unchanged,omitempty"`
	NoGitHistory        int    `json:"no_git_history,omitempty"`
	NonGitPaths         int    `json:"non_git_paths,omitempty"`
}

func (b *Box) AddPath(ctx context.Context, command AddPathCommand) (AddPathResult, error) {
	result, err := b.service.AddPath(ctx, command.Directory)
	return AddPathResult{Path: pathView(result.Path), AlreadyExists: result.AlreadyExists, Scan: scanReport(result.Scan)}, err
}

func (b *Box) ListPaths(ctx context.Context) ([]PathView, error) {
	paths, err := b.service.ListPaths(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PathView, 0, len(paths))
	for _, path := range paths {
		out = append(out, pathView(path))
	}
	return out, nil
}

func (b *Box) RemovePath(ctx context.Context, command RemovePathCommand) (RemovePathResult, error) {
	result, err := b.service.RemovePath(ctx, command.Selector)
	return RemovePathResult{PathID: int64(result.Path.ID), Documents: result.Documents}, err
}

func (b *Box) ScanPaths(ctx context.Context, command ScanPathsCommand) (ScanReport, error) {
	report, err := b.service.ScanPaths(ctx, application.ScanOptions{Selector: command.Selector, TimestampSource: command.TimestampSource})
	return scanReport(report), err
}

type SearchDocumentsQuery struct {
	Query string
	Limit int
}
type SearchResult struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Snippet    string `json:"snippet"`
}

func (b *Box) SearchDocuments(ctx context.Context, query SearchDocumentsQuery) ([]SearchResult, error) {
	hits, err := b.service.Search(ctx, query.Query, query.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(hits))
	for _, hit := range hits {
		out = append(out, SearchResult{DocumentID: string(hit.DocumentID), Title: hit.Title, Path: hit.Path, Snippet: hit.Snippet})
	}
	return out, nil
}

type ListDocumentsQuery struct {
	Limit int
	All   bool
}

type GetDocumentQuery struct{ Selector string }
type DocumentView struct {
	ID           string     `json:"id"`
	Path         string     `json:"path"`
	PathID       int64      `json:"path_id"`
	RelativePath string     `json:"relative_path"`
	Status       string     `json:"status"`
	Pinned       bool       `json:"pinned"`
	Title        string     `json:"title"`
	Summary      string     `json:"summary"`
	MTime        int64      `json:"mtime"`
	Size         int64      `json:"size"`
	SHA256       string     `json:"sha256"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	IndexedAt    *time.Time `json:"indexed_at"`
}

func (b *Box) ListDocuments(ctx context.Context, query ListDocumentsQuery) ([]DocumentView, error) {
	records, err := b.service.ListDocuments(ctx, query.Limit, query.All)
	if err != nil {
		return nil, err
	}
	views := make([]DocumentView, 0, len(records))
	for _, record := range records {
		views = append(views, documentView(record.Document, record.AbsolutePath))
	}
	return views, nil
}

func (b *Box) GetDocument(ctx context.Context, query GetDocumentQuery) (DocumentView, error) {
	document, path, err := b.service.ResolveDocument(ctx, query.Selector)
	if err != nil {
		return DocumentView{}, err
	}
	return documentView(document, path), nil
}

func documentView(document *catalog.Document, path string) DocumentView {
	view := DocumentView{ID: string(document.ID), Path: path, PathID: int64(document.Location.PathID), RelativePath: document.Location.RelativePath,
		Status: string(document.Status), Pinned: document.Pinned, Title: document.Index.Title, Summary: document.Index.Summary, MTime: document.Index.MTime, Size: document.Index.Size,
		SHA256: document.Index.SHA256, CreatedAt: document.Index.SourceCreatedAt, UpdatedAt: document.Index.SourceUpdatedAt}
	if !document.Index.IndexedAt.IsZero() {
		value := document.Index.IndexedAt
		view.IndexedAt = &value
	}
	return view
}

type ResolveLocationQuery struct{ Selector string }
type LocationView struct{ DocumentID, Path, Status string }

func (b *Box) ResolveDocumentLocation(ctx context.Context, query ResolveLocationQuery) (LocationView, error) {
	document, path, err := b.service.ResolveDocument(ctx, query.Selector)
	if err != nil {
		return LocationView{}, err
	}
	return LocationView{DocumentID: string(document.ID), Path: path, Status: string(document.Status)}, nil
}

type CreateNoteCommand struct {
	Title        string
	FromSelector string
}

type LinkView struct {
	FromDocumentID string `json:"from_document_id"`
	ToDocumentID   string `json:"to_document_id"`
	Kind           string `json:"kind"`
}

type CreateNoteResult struct {
	Document DocumentView `json:"document"`
	Link     *LinkView    `json:"link,omitempty"`
}

func (b *Box) CreateNote(ctx context.Context, command CreateNoteCommand) (CreateNoteResult, error) {
	result, err := b.service.CreateNote(ctx, application.CreateNoteOptions{Title: command.Title, FromSelector: command.FromSelector})
	if err != nil {
		return CreateNoteResult{}, err
	}
	view := CreateNoteResult{Document: documentView(result.Document, result.Path)}
	if result.Link != nil {
		view.Link = &LinkView{FromDocumentID: string(result.Link.FromDocumentID), ToDocumentID: string(result.Link.ToDocumentID), Kind: string(result.Link.Kind)}
	}
	return view, nil
}

type TopicView struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	RelativePath string    `json:"relative_path"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func topicView(record port.DocumentRecord) TopicView {
	name, _ := catalog.TopicNameForDocument(record.Document)
	if name == "" {
		name = record.Document.Index.Title
	}
	return TopicView{ID: string(record.Document.ID), Name: name, Path: record.AbsolutePath, RelativePath: record.Document.Location.RelativePath, CreatedAt: record.Document.CreatedAt, UpdatedAt: record.Document.UpdatedAt}
}

type ListTopicsQuery struct{}

func (b *Box) ListTopics(ctx context.Context, _ ListTopicsQuery) ([]TopicView, error) {
	topics, err := b.service.ListTopics(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]TopicView, 0, len(topics))
	for _, record := range topics {
		views = append(views, topicView(record))
	}
	return views, nil
}

type CreateTopicCommand struct{ Name string }

type CreateTopicResult struct {
	Topic         TopicView `json:"topic"`
	AlreadyExists bool      `json:"already_exists"`
}

func (b *Box) CreateTopic(ctx context.Context, command CreateTopicCommand) (CreateTopicResult, error) {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return CreateTopicResult{}, errors.New("topic name is required")
	}
	before, err := b.service.ListTopics(ctx)
	if err != nil {
		return CreateTopicResult{}, err
	}
	for _, existing := range before {
		topicName, _ := catalog.TopicNameForDocument(existing.Document)
		if strings.EqualFold(topicName, name) || strings.EqualFold(existing.Document.Index.Title, name) {
			return CreateTopicResult{Topic: topicView(existing), AlreadyExists: true}, nil
		}
	}
	created, err := b.service.CreateNote(ctx, application.CreateNoteOptions{Title: name, Topic: true})
	if err != nil {
		return CreateTopicResult{}, err
	}
	return CreateTopicResult{Topic: topicView(port.DocumentRecord{Document: created.Document, AbsolutePath: created.Path})}, nil
}

type TopicMembershipCommand struct {
	DocumentSelector string
	TopicSelector    string
}

type TopicMembershipResult struct {
	DocumentID    string    `json:"document_id"`
	Topic         TopicView `json:"topic"`
	Added         bool      `json:"added"`
	Removed       bool      `json:"removed"`
	AlreadyExists bool      `json:"already_exists,omitempty"`
}

func (b *Box) AddDocumentTopic(ctx context.Context, command TopicMembershipCommand) (TopicMembershipResult, error) {
	document, _, err := b.service.ResolveDocument(ctx, command.DocumentSelector)
	if err != nil {
		return TopicMembershipResult{}, err
	}
	topic, _, err := b.service.ResolveTopicSelector(ctx, command.TopicSelector)
	if err != nil {
		return TopicMembershipResult{}, err
	}
	added, err := b.service.AddDocumentTopic(ctx, command.DocumentSelector, command.TopicSelector)
	return TopicMembershipResult{DocumentID: string(document.ID), Topic: topicView(port.DocumentRecord{Document: topic}), Added: added, AlreadyExists: !added}, err
}

func (b *Box) RemoveDocumentTopic(ctx context.Context, command TopicMembershipCommand) (TopicMembershipResult, error) {
	document, _, err := b.service.ResolveDocument(ctx, command.DocumentSelector)
	if err != nil {
		return TopicMembershipResult{}, err
	}
	topic, _, err := b.service.ResolveTopicSelector(ctx, command.TopicSelector)
	if err != nil {
		return TopicMembershipResult{}, err
	}
	removed, err := b.service.RemoveDocumentTopic(ctx, command.DocumentSelector, command.TopicSelector)
	return TopicMembershipResult{DocumentID: string(document.ID), Topic: topicView(port.DocumentRecord{Document: topic}), Removed: removed}, err
}

type ListTopicDocumentsQuery struct{ Selector string }
type TopicDocumentsView struct {
	Topic     TopicView      `json:"topic"`
	Documents []DocumentView `json:"documents"`
}

func (b *Box) ListTopicDocuments(ctx context.Context, query ListTopicDocumentsQuery) (TopicDocumentsView, error) {
	topic, topicPath, links, err := b.service.ListTopicDocuments(ctx, query.Selector)
	if err != nil {
		return TopicDocumentsView{}, err
	}
	result := TopicDocumentsView{Topic: topicView(port.DocumentRecord{Document: topic, AbsolutePath: topicPath}), Documents: make([]DocumentView, 0, len(links))}
	for _, link := range links {
		result.Documents = append(result.Documents, documentView(link.Document, link.Path))
	}
	return result, nil
}

type LinkDocumentsCommand struct {
	FromSelector string
	ToSelector   string
}

type LinkDocumentsResult struct {
	Created       bool `json:"created"`
	AlreadyExists bool `json:"already_exists"`
}

func (b *Box) LinkDocuments(ctx context.Context, command LinkDocumentsCommand) (LinkDocumentsResult, error) {
	result, err := b.service.LinkDocuments(ctx, command.FromSelector, command.ToSelector)
	return LinkDocumentsResult{Created: result.Created, AlreadyExists: result.AlreadyExists}, err
}

type UnlinkDocumentsCommand struct {
	FromSelector string
	ToSelector   string
}

type UnlinkDocumentsResult struct {
	Removed bool `json:"removed"`
}

func (b *Box) UnlinkDocuments(ctx context.Context, command UnlinkDocumentsCommand) (UnlinkDocumentsResult, error) {
	removed, err := b.service.UnlinkDocuments(ctx, command.FromSelector, command.ToSelector)
	return UnlinkDocumentsResult{Removed: removed}, err
}

type GetDocumentGraphQuery struct{ Selector string }
type DocumentGraphView struct {
	Focus    DocumentView   `json:"focus"`
	Outgoing []DocumentView `json:"outgoing"`
	Incoming []DocumentView `json:"incoming"`
	Topics   []TopicView    `json:"topics"`
}

func (b *Box) GetDocumentGraph(ctx context.Context, query GetDocumentGraphQuery) (DocumentGraphView, error) {
	document, graph, err := b.service.GetDocumentGraph(ctx, query.Selector)
	if err != nil {
		return DocumentGraphView{}, err
	}
	focusPath := ""
	if _, path, resolveErr := b.service.ResolveDocument(ctx, string(document.ID)); resolveErr == nil {
		focusPath = path
	}
	result := DocumentGraphView{
		Focus:    documentView(document, focusPath),
		Outgoing: make([]DocumentView, 0, len(graph.Outgoing)),
		Incoming: make([]DocumentView, 0, len(graph.Incoming)),
		Topics:   make([]TopicView, 0, len(graph.Topics)),
	}
	for _, link := range graph.Outgoing {
		result.Outgoing = append(result.Outgoing, documentView(link.Document, link.Path))
	}
	for _, link := range graph.Incoming {
		result.Incoming = append(result.Incoming, documentView(link.Document, link.Path))
	}
	for _, link := range graph.Topics {
		result.Topics = append(result.Topics, topicView(port.DocumentRecord{Document: link.Document, AbsolutePath: link.Path}))
	}
	return result, nil
}

type GetNeighborhoodQuery struct {
	Selector string
	Depth    int
}

type NeighborhoodNodeView struct {
	DocumentView
	Distance int `json:"distance"`
}

type NeighborhoodEdgeView struct {
	FromID string `json:"from"`
	ToID   string `json:"to"`
}

type NeighborhoodView struct {
	Focus DocumentView           `json:"focus"`
	Depth int                    `json:"depth"`
	Nodes []NeighborhoodNodeView `json:"nodes"`
	Edges []NeighborhoodEdgeView `json:"edges"`
}

// GetNeighborhood returns every document within Depth link hops of the
// selector (links and backlinks alike) plus the directed edges between them.
func (b *Box) GetNeighborhood(ctx context.Context, query GetNeighborhoodQuery) (NeighborhoodView, error) {
	neighborhood, err := b.service.GetDocumentNeighborhood(ctx, query.Selector, query.Depth)
	if err != nil {
		return NeighborhoodView{}, err
	}
	view := NeighborhoodView{
		Focus: documentView(neighborhood.Focus, neighborhood.FocusPath),
		Depth: neighborhood.Depth,
		Nodes: make([]NeighborhoodNodeView, 0, len(neighborhood.Nodes)),
		Edges: make([]NeighborhoodEdgeView, 0, len(neighborhood.Edges)),
	}
	for _, node := range neighborhood.Nodes {
		view.Nodes = append(view.Nodes, NeighborhoodNodeView{DocumentView: documentView(node.Document, node.Path), Distance: node.Distance})
	}
	for _, edge := range neighborhood.Edges {
		view.Edges = append(view.Edges, NeighborhoodEdgeView{FromID: string(edge.From), ToID: string(edge.To)})
	}
	return view, nil
}

type ReadDocumentQuery struct{ Selector string }

func (b *Box) ReadDocument(ctx context.Context, query ReadDocumentQuery) ([]byte, error) {
	return b.service.ReadDocument(ctx, query.Selector)
}

type ToggleDocumentPinCommand struct{ Selector string }
type ToggleDocumentPinResult struct {
	DocumentID string `json:"document_id"`
	Pinned     bool   `json:"pinned"`
}

func (b *Box) ToggleDocumentPin(ctx context.Context, command ToggleDocumentPinCommand) (ToggleDocumentPinResult, error) {
	result, err := b.service.ToggleDocumentPin(ctx, command.Selector)
	return ToggleDocumentPinResult{DocumentID: string(result.DocumentID), Pinned: result.Pinned}, err
}

type DeleteDocumentCommand struct{ Selector string }
type DeleteDocumentResult struct {
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
	Trashed    bool   `json:"trashed"`
}

// DeleteDocument soft-deletes: the Markdown file moves to the path's trash
// directory and the document keeps its UUID, links, and annotations until
// purged. Restore with RestoreTrashedDocument.
func (b *Box) DeleteDocument(ctx context.Context, command DeleteDocumentCommand) (DeleteDocumentResult, error) {
	document, path, err := b.service.TrashDocumentFile(ctx, command.Selector)
	return DeleteDocumentResult{DocumentID: string(document.ID), Path: path, Trashed: true}, err
}

type TrashItemView struct {
	DocumentView
	OriginRelativePath string    `json:"origin_relative_path"`
	TrashedAt          time.Time `json:"trashed_at"`
}

func (b *Box) ListTrash(ctx context.Context) ([]TrashItemView, error) {
	records, trashRecords, err := b.service.ListTrashedDocuments(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]port.TrashRecord, len(trashRecords))
	for _, record := range trashRecords {
		byID[string(record.DocumentID)] = record
	}
	views := make([]TrashItemView, 0, len(records))
	for _, record := range records {
		trash := byID[string(record.Document.ID)]
		views = append(views, TrashItemView{
			DocumentView:       documentView(record.Document, record.AbsolutePath),
			OriginRelativePath: trash.OriginRelativePath,
			TrashedAt:          trash.TrashedAt,
		})
	}
	return views, nil
}

type RestoreDocumentCommand struct{ Selector string }
type RestoreDocumentResult struct {
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
}

func (b *Box) RestoreTrashedDocument(ctx context.Context, command RestoreDocumentCommand) (RestoreDocumentResult, error) {
	document, path, err := b.service.RestoreDocument(ctx, command.Selector)
	return RestoreDocumentResult{DocumentID: string(document.ID), Path: path}, err
}

type PurgeTrashCommand struct {
	All           bool
	OlderThanDays int
}
type PurgeTrashResult struct {
	Removed    int   `json:"removed"`
	BytesFreed int64 `json:"bytes_freed"`
}

// PurgeTrash physically deletes trashed documents. Without --all, only items
// older than OlderThanDays (default 30) are removed.
func (b *Box) PurgeTrash(ctx context.Context, command PurgeTrashCommand) (PurgeTrashResult, error) {
	var olderThan time.Time
	if !command.All {
		days := command.OlderThanDays
		if days <= 0 {
			days = 30
		}
		olderThan = time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	}
	removed, freed, err := b.service.PurgeTrashedDocuments(ctx, olderThan)
	return PurgeTrashResult{Removed: removed, BytesFreed: freed}, err
}

type TrashSummaryResult struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

func (b *Box) TrashSummary(ctx context.Context) (TrashSummaryResult, error) {
	count, bytes, err := b.service.TrashSummary(ctx)
	return TrashSummaryResult{Count: count, Bytes: bytes}, err
}

type RenameDocumentCommand struct {
	Selector    string
	NewFilename string
	// Title is the human display name. When set, front matter and the first H1
	// are updated so reopen/list UIs do not snap back to the old body title.
	Title string
}
type RenameDocumentResult struct {
	DocumentID string `json:"document_id"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
}

// RenameDocument moves a document's Markdown file to a new filename in the
// same directory without changing its stable UUID. Links, source URLs, and
// annotation relations are preserved. Display title in the body is updated
// when command.Title is set (or derived from the new filename).
func (b *Box) RenameDocument(ctx context.Context, command RenameDocumentCommand) (RenameDocumentResult, error) {
	result, err := b.service.RenameDocument(ctx, command.Selector, command.NewFilename, command.Title)
	if err != nil {
		return RenameDocumentResult{}, err
	}
	return RenameDocumentResult{DocumentID: string(result.DocumentID), Path: result.Path, Title: result.Title}, nil
}

type ReindexDocumentCommand struct{ Selector string }

func (b *Box) ReindexDocument(ctx context.Context, command ReindexDocumentCommand) error {
	return b.service.ReindexDocument(ctx, command.Selector)
}

type IndexStatusView struct {
	Paths        int        `json:"paths"`
	Active       int        `json:"active"`
	Missing      int        `json:"missing"`
	Untracked    int        `json:"untracked"`
	LastScanAt   *time.Time `json:"last_scan_at"`
	DatabasePath string     `json:"database_path"`
}

func (b *Box) GetIndexStatus(ctx context.Context) (IndexStatusView, error) {
	status, err := b.service.Status(ctx)
	return IndexStatusView{Paths: status.Paths, Active: status.Active, Missing: status.Missing, Untracked: status.Untracked, LastScanAt: status.LastScanAt, DatabasePath: status.DatabasePath}, err
}

func pathView(summary port.PathSummary) PathView {
	return PathView{ID: int64(summary.Path.ID), Path: summary.Path.Root, Documents: summary.DocumentCount,
		Status: string(summary.Path.Status), CreatedAt: summary.Path.CreatedAt, LastScanAt: summary.Path.LastScanAt,
		LastError: summary.Path.LastError}
}

func scanReport(report application.ScanReport) ScanReport {
	return ScanReport{Paths: report.Paths, Files: report.Files, Added: report.Added, Updated: report.Updated,
		Renamed: report.Renamed, Unchanged: report.Unchanged, Missing: report.Missing,
		PossibleRenames: report.PossibleRenames, Errors: report.Errors, TimestampSource: report.TimestampSource,
		GitPaths: report.GitPaths, TimestampsUpdated: report.TimestampsUpdated, TimestampsUnchanged: report.TimestampsUnchanged,
		NoGitHistory: report.NoGitHistory, NonGitPaths: report.NonGitPaths}
}
