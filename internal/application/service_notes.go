package application

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

func (s *Service) ListTopics(ctx context.Context) ([]port.DocumentRecord, error) {
	return s.store.ListTopics(ctx)
}

func (s *Service) ResolveTopicSelector(ctx context.Context, selector string) (*catalog.Document, string, error) {
	return s.store.ResolveTopic(ctx, strings.TrimSpace(selector))
}

type CreateNoteOptions struct {
	Title string
	Body  string
	// Filename is an internal exact-path override for generated projections.
	// Unlike Title-derived note names, it is already validated by the caller
	// and must not be slugified or collision-suffixed.
	Filename string
	// AlignBodyTitle makes Title authoritative for an existing front matter
	// title and first H1 in Body. Browser-created related documents use this so
	// pasted Markdown cannot replace the title that also generated the filename.
	AlignBodyTitle bool
	FromSelector   string
	Topic          bool
	// Browser clip provenance (stored in document_sources, not inferred later).
	SourceURL string
	ClipMode  string // "selection" | "page" | ""
}

type CreateNoteResult struct {
	Document *catalog.Document
	Path     string
	Link     *catalog.GraphEdge
}

type UpsertMarkdownOptions struct {
	Filename string
	Body     string
}

type UpsertMarkdownResult struct {
	Document *catalog.Document
	Path     string
	Created  bool
}

func validFlatMarkdownFilename(filename string) bool {
	return filename != "" && filename != "." && filename != ".." &&
		filepath.Base(filename) == filename && strings.ToLower(filepath.Ext(filename)) == ".md" &&
		!strings.Contains(filename, "/") && !strings.Contains(filename, "\\")
}

// ResolveDocumentByRelativePath resolves an active document in the configured
// main path by its exact flat filename. It is intentionally narrower than the
// public UUID selector used by ResolveDocument.
func (s *Service) ResolveDocumentByRelativePath(ctx context.Context, filename string) (*catalog.Document, string, error) {
	filename = strings.TrimSpace(filename)
	if !validFlatMarkdownFilename(filename) {
		return nil, "", fmt.Errorf("invalid relative filename %q", filename)
	}
	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return nil, "", err
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return nil, "", err
	}
	for _, document := range documents {
		if filepath.ToSlash(document.Location.RelativePath) == filepath.ToSlash(filename) && document.Status == catalog.DocumentActive {
			return document, filepath.Join(indexedPath.Root, filename), nil
		}
	}
	return nil, "", fmt.Errorf("document path %q not found", filename)
}

// FindMarkdownByFilenameSuffix resolves a generated Markdown projection in the
// configured main path by an identity-bearing filename suffix. It supports
// migrating an exact legacy filename to a readable-prefix convention without
// relying on a mutable PDF title or path.
func (s *Service) FindMarkdownByFilenameSuffix(ctx context.Context, suffix string) (*catalog.Document, string, bool, error) {
	suffix = strings.TrimSpace(suffix)
	if !validFlatMarkdownFilename(suffix) {
		return nil, "", false, fmt.Errorf("invalid generated Markdown suffix %q", suffix)
	}
	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return nil, "", false, err
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return nil, "", false, err
	}
	var match *catalog.Document
	var absolute string
	for _, document := range documents {
		relative := filepath.ToSlash(document.Location.RelativePath)
		// Trashed projections must never shadow a regenerated bundle.
		if strings.HasPrefix(relative, catalog.TrashDir+"/") {
			continue
		}
		filename := filepath.Base(filepath.FromSlash(relative))
		if document.Index.MediaType != "text/markdown" || !strings.HasSuffix(filename, suffix) {
			continue
		}
		if match != nil {
			return nil, "", false, fmt.Errorf("multiple generated Markdown files end in %q", suffix)
		}
		match = document
		absolute = filepath.Join(indexedPath.Root, filepath.FromSlash(document.Location.RelativePath))
	}
	if match == nil {
		return nil, "", false, nil
	}
	return match, absolute, true, nil
}

// UpsertMarkdown writes one generated Markdown projection into the configured
// main path under an exact, stable filename. Existing active documents are
// updated in place so their UUID, links and annotations survive regeneration.
func (s *Service) UpsertMarkdown(ctx context.Context, opts UpsertMarkdownOptions) (UpsertMarkdownResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return UpsertMarkdownResult{}, lockErr
	}
	defer release()
	filename := strings.TrimSpace(opts.Filename)
	if !validFlatMarkdownFilename(filename) {
		return UpsertMarkdownResult{}, fmt.Errorf("invalid generated Markdown filename %q", filename)
	}
	if strings.TrimSpace(opts.Body) == "" {
		return UpsertMarkdownResult{}, errors.New("generated Markdown body is required")
	}
	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return UpsertMarkdownResult{}, err
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return UpsertMarkdownResult{}, err
	}
	for _, document := range documents {
		if filepath.ToSlash(document.Location.RelativePath) != filepath.ToSlash(filename) {
			continue
		}
		if document.Status != catalog.DocumentActive {
			return UpsertMarkdownResult{}, fmt.Errorf("generated document %s is %s; restore it before publishing", document.ID, document.Status)
		}
		updated, err := s.SyncDocument(ctx, string(document.ID), opts.Body)
		if err != nil {
			return UpsertMarkdownResult{}, err
		}
		return UpsertMarkdownResult{Document: document, Path: updated.Path}, nil
	}
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	created, err := s.CreateNote(ctx, CreateNoteOptions{Title: stem, Body: opts.Body, Filename: filename})
	if err != nil {
		return UpsertMarkdownResult{}, err
	}
	if filepath.Base(created.Path) != filename {
		return UpsertMarkdownResult{}, fmt.Errorf("generated filename collision: wanted %s, created %s", filename, filepath.Base(created.Path))
	}
	return UpsertMarkdownResult{Document: created.Document, Path: created.Path, Created: true}, nil
}

func (s *Service) CreateNote(ctx context.Context, opts CreateNoteOptions) (CreateNoteResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return CreateNoteResult{}, lockErr
	}
	defer release()
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return CreateNoteResult{}, errors.New("note title is required")
	}
	var from *catalog.Document
	if strings.TrimSpace(opts.FromSelector) != "" {
		resolved, _, err := s.ResolveDocument(ctx, opts.FromSelector)
		if err != nil {
			return CreateNoteResult{}, err
		}
		from = resolved
	}
	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return CreateNoteResult{}, err
	}
	filename := strings.TrimSpace(opts.Filename)
	exactFilename := filename != ""
	if exactFilename {
		if !validFlatMarkdownFilename(filename) || strings.HasPrefix(filename, ".") {
			return CreateNoteResult{}, fmt.Errorf("invalid exact note filename %q", filename)
		}
		if opts.Topic || strings.EqualFold(strings.TrimSpace(opts.ClipMode), "selection") {
			return CreateNoteResult{}, errors.New("exact note filename cannot be combined with topic or selection-note naming")
		}
	} else {
		slug := noteSlug(title)
		if slug == "" {
			return CreateNoteResult{}, errors.New("note title does not produce a filename")
		}
		filename = slug + ".md"
		if opts.Topic {
			filename = "topic-" + slug + ".md"
			if existing, absolute, err := s.store.ResolveTopic(ctx, title); err == nil {
				return CreateNoteResult{Document: existing, Path: absolute}, nil
			}
		} else if strings.EqualFold(strings.TrimSpace(opts.ClipMode), "selection") {
			// Selection excerpts always use the *-note.md convention. A content
			// hash keeps names unique when two excerpts share the same slug
			// prefix, so the -2 collision suffix effectively never triggers.
			slug = strings.TrimSuffix(slug, "-note")
			if slug == "" {
				slug = "selection"
			}
			filename = slug + "-" + contentNameFragment(opts.Body, 10) + "-note.md"
		}
	}
	// Never reuse a catalog-tracked path: a new note must not clobber an
	// existing document's file (active, missing, or trashed all count).
	tracked := map[string]bool{}
	if docs, docsErr := s.store.DocumentsForPath(ctx, indexedPath.ID); docsErr == nil {
		for _, d := range docs {
			tracked[filepath.ToSlash(d.Location.RelativePath)] = true
		}
	}
	var absolute string
	if exactFilename {
		if tracked[filepath.ToSlash(filename)] {
			return CreateNoteResult{}, fmt.Errorf("exact note filename %q is already cataloged", filename)
		}
		absolute = filepath.Join(indexedPath.Root, filename)
		if _, err := os.Lstat(absolute); err == nil {
			return CreateNoteResult{}, fmt.Errorf("exact note filename %q already exists", filename)
		} else if !errors.Is(err, os.ErrNotExist) {
			return CreateNoteResult{}, err
		}
	} else {
		var err error
		absolute, err = s.availableNotePath(indexedPath.Root, filename, tracked)
		if err != nil {
			return CreateNoteResult{}, err
		}
	}
	body := []byte(opts.Body)
	if len(body) == 0 {
		body = []byte("# " + title + "\n\n")
	} else if opts.AlignBodyTitle {
		body, _ = rewriteMarkdownDisplayTitle(body, title)
	}
	if err := s.writer.WriteNew(ctx, absolute, body); err != nil {
		return CreateNoteResult{}, err
	}
	// The note is verified by path below, not by the scan's add/update split:
	// when the file already existed in the catalog (e.g. deleted externally and
	// recreated, or a rename matched it), the scan reports Updated instead of
	// Added — the note is still correctly indexed.
	relative, err := filepath.Rel(indexedPath.Root, absolute)
	if err != nil {
		return CreateNoteResult{}, err
	}
	// Fast path: index exactly the freshly written file instead of walking the
	// whole directory on every clip (`mm path scan` still does the full walk).
	// Fall back to the full scan when the single-file observation fails or the
	// path turns out to be cataloged (external recreation / rename matched it).
	created, fastErr := s.indexNewFile(ctx, indexedPath, absolute)
	if created == nil {
		if _, scanErr := s.scanOne(ctx, indexedPath); scanErr != nil {
			if fastErr != nil {
				return CreateNoteResult{}, errors.Join(fastErr, scanErr)
			}
			return CreateNoteResult{}, scanErr
		}
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return CreateNoteResult{}, err
	}
	created = nil
	for _, document := range documents {
		if document.Location.RelativePath == filepath.ToSlash(relative) && document.Status == catalog.DocumentActive {
			created = document
			break
		}
	}
	if created == nil {
		return CreateNoteResult{}, fmt.Errorf("created note %q was not indexed", absolute)
	}
	result := CreateNoteResult{Document: created, Path: absolute}
	if from != nil {
		edge, err := catalog.NewGraphEdge(from.ID, created.ID, catalog.EdgeManual, s.clock.Now())
		if err != nil {
			return CreateNoteResult{}, err
		}
		if _, err := s.store.AddEdge(ctx, edge); err != nil {
			return CreateNoteResult{}, err
		}
		result.Link = &edge
	}
	if source := strings.TrimSpace(opts.SourceURL); source != "" {
		mode := strings.TrimSpace(opts.ClipMode)
		if mode == "" && strings.HasSuffix(strings.ToLower(created.Location.RelativePath), "-note.md") {
			mode = "selection"
		}
		if mode == "" {
			mode = "page"
		}
		if err := s.store.UpsertDocumentSource(ctx, created.ID, source, mode, s.clock.Now()); err != nil {
			return CreateNoteResult{}, err
		}
	}
	return result, nil
}

// indexNewFile indexes exactly one freshly written document without walking
// the whole directory (CreateNote fast path — avoids an O(directory) rescan
// on every browser clip).
//
// Callers must hold the mutation lock and guarantee the file is not already
// cataloged (WriteNew uses O_EXCL and path tracking is checked upstream).
// When the observation fails the caller falls back to a full scan so the
// note is still indexed with identical semantics to before.
func (s *Service) indexNewFile(ctx context.Context, indexedPath *catalog.IndexedPath, absolute string) (*catalog.Document, error) {
	relative, err := filepath.Rel(indexedPath.Root, absolute)
	if err != nil {
		return nil, fmt.Errorf("relating note path: %w", err)
	}
	location, err := catalog.NewLocation(indexedPath.ID, filepath.ToSlash(relative))
	if err != nil {
		return nil, err
	}
	observation, err := s.scanner.ObserveFile(ctx, location, absolute)
	if err != nil {
		return nil, err
	}
	id, err := s.ids.NewDocumentID()
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	document, err := catalog.NewDocument(id, observation, now)
	if err != nil {
		return nil, err
	}
	save := port.ScanSave{Document: document, Body: observation.Body, SearchText: observation.SearchText, Reindex: true}
	indexedPath.RecordScan(now, nil)
	if err := s.prepareScanContent(ctx, []port.ScanSave{save}); err != nil {
		return nil, err
	}
	if err := s.store.SaveScan(ctx, indexedPath, []port.ScanSave{save}); err != nil {
		return nil, err
	}
	return document, nil
}

// ListClipsBySourceURL returns documents registered for a normalized web URL.
// When selectionNotesOnly is true, only selection excerpts (*-note.md / clip_mode=selection) are returned.
func (s *Service) ListClipsBySourceURL(ctx context.Context, sourceURLNorm string, selectionNotesOnly bool) ([]port.DocumentSourceRecord, error) {
	return s.store.ListDocumentsBySourceURL(ctx, strings.TrimSpace(sourceURLNorm), selectionNotesOnly)
}

// ListDocumentSources enumerates browser-ingested documents, optionally
// filtered by clip mode.
func (s *Service) ListDocumentSources(ctx context.Context, clipMode string) ([]port.DocumentSourceRecord, error) {
	return s.store.ListDocumentSources(ctx, clipMode)
}

func (s *Service) defaultCreatePath(ctx context.Context) (*catalog.IndexedPath, error) {
	summaries, err := s.store.ListPaths(ctx, false)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, errors.New("no configured paths; add one with mm path add")
	}
	preferred, err := s.store.GetSetting(ctx, SettingMainPath)
	if err != nil {
		return nil, err
	}
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		for i := range summaries {
			if pathRootsEqual(summaries[i].Path.Root, preferred) {
				path := summaries[i].Path
				return &path, nil
			}
		}
	}
	// Default: first configured path (paths[0]).
	path := summaries[0].Path
	return &path, nil
}

func pathRootsEqual(a, b string) bool {
	left, err := filepath.Abs(filepath.Clean(a))
	if err != nil {
		left = filepath.Clean(a)
	}
	right, err := filepath.Abs(filepath.Clean(b))
	if err != nil {
		right = filepath.Clean(b)
	}
	return left == right
}

func (s *Service) availableNotePath(root, filename string, tracked map[string]bool) (string, error) {
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") || filename == "" {
		return "", fmt.Errorf("invalid note filename %q", filename)
	}
	for index := 0; ; index++ {
		candidate := filename
		if index > 0 {
			extension := filepath.Ext(filename)
			candidate = strings.TrimSuffix(filename, extension) + "-" + strconv.Itoa(index+1) + extension
		}
		// A path tracked by the catalog (active, missing, or trashed) must never
		// be reused by a new note: writing there would clobber an existing
		// document's file and re-point it at the new content (this is what
		// replaced the Effective-Go article with a bubble-sort note).
		if tracked[filepath.ToSlash(candidate)] {
			continue
		}
		absolute := filepath.Join(root, candidate)
		if _, err := os.Stat(absolute); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return absolute, nil
	}
}

func noteSlug(title string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

// contentNameFragment derives a short fragment from the note body so
// selection notes sharing a slug prefix still get distinct filenames. It is
// deliberately alphabetic, never hex: document IDs are UUIDv7 hex strings, so
// a hex fragment in filenames would pollute UUID / short-ID searches.
func contentNameFragment(body string, length int) string {
	sum := sha256.Sum256([]byte(body))
	value := binary.BigEndian.Uint64(sum[:8])
	fragment := make([]byte, length)
	for i := length - 1; i >= 0; i-- {
		fragment[i] = "abcdefghijklmnopqrstuvwxyz"[value%26]
		value /= 26
	}
	return string(fragment)
}

func (s *Service) AddDocumentTopic(ctx context.Context, documentSelector, topicSelector string) (bool, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return false, lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, documentSelector)
	if err != nil {
		return false, err
	}
	topic, _, err := s.store.ResolveTopic(ctx, strings.TrimSpace(topicSelector))
	if err != nil {
		return false, err
	}
	edge, err := catalog.NewGraphEdge(document.ID, topic.ID, catalog.EdgeMember, s.clock.Now())
	if err != nil {
		return false, err
	}
	return s.store.AddEdge(ctx, edge)
}

func (s *Service) RemoveDocumentTopic(ctx context.Context, documentSelector, topicSelector string) (bool, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return false, lockErr
	}
	defer release()
	document, _, err := s.ResolveDocument(ctx, documentSelector)
	if err != nil {
		return false, err
	}
	topic, _, err := s.store.ResolveTopic(ctx, strings.TrimSpace(topicSelector))
	if err != nil {
		return false, err
	}
	return s.store.RemoveEdge(ctx, document.ID, topic.ID, catalog.EdgeMember)
}

type DocumentLinkResult struct {
	Created       bool
	AlreadyExists bool
}

func (s *Service) LinkDocuments(ctx context.Context, fromSelector, toSelector string) (DocumentLinkResult, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return DocumentLinkResult{}, lockErr
	}
	defer release()
	fromDocument, _, err := s.ResolveDocument(ctx, fromSelector)
	if err != nil {
		return DocumentLinkResult{}, err
	}
	toDocument, _, err := s.ResolveDocument(ctx, toSelector)
	if err != nil {
		return DocumentLinkResult{}, err
	}
	edge, err := catalog.NewGraphEdge(fromDocument.ID, toDocument.ID, catalog.EdgeManual, s.clock.Now())
	if err != nil {
		return DocumentLinkResult{}, err
	}
	created, err := s.store.AddEdge(ctx, edge)
	if err != nil {
		return DocumentLinkResult{}, err
	}
	return DocumentLinkResult{Created: created, AlreadyExists: !created}, nil
}

func (s *Service) UnlinkDocuments(ctx context.Context, fromSelector, toSelector string) (bool, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return false, lockErr
	}
	defer release()
	fromDocument, _, err := s.ResolveDocument(ctx, fromSelector)
	if err != nil {
		return false, err
	}
	toDocument, _, err := s.ResolveDocument(ctx, toSelector)
	if err != nil {
		return false, err
	}
	return s.store.RemoveEdge(ctx, fromDocument.ID, toDocument.ID, catalog.EdgeManual)
}

func (s *Service) GetDocumentGraph(ctx context.Context, selector string) (*catalog.Document, catalog.DocumentGraph, error) {
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return nil, catalog.DocumentGraph{}, err
	}
	outgoing, incoming, topics, err := s.store.GetDocumentGraph(ctx, document.ID)
	if err != nil {
		return nil, catalog.DocumentGraph{}, err
	}
	graph := catalog.DocumentGraph{
		Outgoing: documentLinks(outgoing),
		Incoming: documentLinks(incoming),
		Topics:   documentLinks(topics),
	}
	return document, graph, nil
}

func documentLinks(records []port.DocumentRecord) []catalog.DocumentLink {
	links := make([]catalog.DocumentLink, 0, len(records))
	for _, record := range records {
		links = append(links, catalog.DocumentLink{Document: record.Document, Path: record.AbsolutePath})
	}
	return links
}

// NeighborhoodNode is one document discovered by the neighborhood walk, with
// the link distance from the focus document.
type NeighborhoodNode struct {
	Document *catalog.Document
	Path     string
	Distance int
}

// NeighborhoodEdge is a directed manual link between two discovered documents.
type NeighborhoodEdge struct {
	From catalog.DocumentID
	To   catalog.DocumentID
}

// Neighborhood is the document subgraph reachable within `depth` link hops.
type Neighborhood struct {
	Focus     *catalog.Document
	FocusPath string
	Depth     int
	Nodes     []NeighborhoodNode
	Edges     []NeighborhoodEdge
}

// GetDocumentNeighborhood walks the manual link graph outward from the focus
// document (links and backlinks alike) up to `depth` hops and returns every
// discovered document plus the edges between them. Depth < 1 is treated as 1.
func (s *Service) GetDocumentNeighborhood(ctx context.Context, selector string, depth int) (Neighborhood, error) {
	if depth < 1 {
		depth = 1
	}
	focus, focusPath, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return Neighborhood{}, err
	}
	nodes := map[catalog.DocumentID]NeighborhoodNode{focus.ID: {Document: focus, Path: focusPath, Distance: 0}}
	edgeSet := map[string]NeighborhoodEdge{}
	addEdge := func(from, to catalog.DocumentID) {
		key := string(from) + "\x00" + string(to)
		edgeSet[key] = NeighborhoodEdge{From: from, To: to}
	}
	frontier := []catalog.DocumentID{focus.ID}
	for distance := 1; distance <= depth && len(frontier) > 0; distance++ {
		var next []catalog.DocumentID
		for _, id := range frontier {
			outgoing, incoming, _, graphErr := s.store.GetDocumentGraph(ctx, id)
			if graphErr != nil {
				return Neighborhood{}, graphErr
			}
			for _, record := range outgoing {
				if record.Document == nil {
					continue
				}
				if _, seen := nodes[record.Document.ID]; !seen {
					nodes[record.Document.ID] = NeighborhoodNode{Document: record.Document, Path: record.AbsolutePath, Distance: distance}
					next = append(next, record.Document.ID)
				}
				addEdge(id, record.Document.ID)
			}
			for _, record := range incoming {
				if record.Document == nil {
					continue
				}
				if _, seen := nodes[record.Document.ID]; !seen {
					nodes[record.Document.ID] = NeighborhoodNode{Document: record.Document, Path: record.AbsolutePath, Distance: distance}
					next = append(next, record.Document.ID)
				}
				addEdge(record.Document.ID, id)
			}
		}
		frontier = next
	}
	result := Neighborhood{Focus: focus, FocusPath: focusPath, Depth: depth}
	for _, node := range nodes {
		result.Nodes = append(result.Nodes, node)
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].Distance != result.Nodes[j].Distance {
			return result.Nodes[i].Distance < result.Nodes[j].Distance
		}
		return result.Nodes[i].Path < result.Nodes[j].Path
	})
	for _, edge := range edgeSet {
		result.Edges = append(result.Edges, edge)
	}
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].From != result.Edges[j].From {
			return result.Edges[i].From < result.Edges[j].From
		}
		return result.Edges[i].To < result.Edges[j].To
	})
	return result, nil
}

func (s *Service) ListTopicDocuments(ctx context.Context, selector string) (*catalog.Document, string, []catalog.DocumentLink, error) {
	topic, absolute, err := s.store.ResolveTopic(ctx, strings.TrimSpace(selector))
	if err != nil {
		return nil, "", nil, err
	}
	records, err := s.store.ListTopicDocuments(ctx, topic.ID)
	if err != nil {
		return nil, "", nil, err
	}
	return topic, absolute, documentLinks(records), nil
}
