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
	Title        string
	Body         string
	FromSelector string
	Topic        bool
	// Browser clip provenance (stored in document_sources, not inferred later).
	SourceURL string
	ClipMode  string // "selection" | "page" | ""
}

type CreateNoteResult struct {
	Document *catalog.Document
	Path     string
	Link     *catalog.GraphEdge
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
	slug := noteSlug(title)
	if slug == "" {
		return CreateNoteResult{}, errors.New("note title does not produce a filename")
	}
	filename := slug + ".md"
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
	absolute, err := s.availableNotePath(indexedPath.Root, filename)
	if err != nil {
		return CreateNoteResult{}, err
	}
	body := []byte(opts.Body)
	if len(body) == 0 {
		body = []byte("# " + title + "\n\n")
	}
	if err := s.writer.WriteNew(ctx, absolute, body); err != nil {
		return CreateNoteResult{}, err
	}
	report, err := s.scanOne(ctx, indexedPath)
	if err != nil {
		return CreateNoteResult{}, err
	}
	if report.Added == 0 {
		return CreateNoteResult{}, fmt.Errorf("created note %q was not indexed", absolute)
	}
	relative, err := filepath.Rel(indexedPath.Root, absolute)
	if err != nil {
		return CreateNoteResult{}, err
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return CreateNoteResult{}, err
	}
	var created *catalog.Document
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

func (s *Service) availableNotePath(root, filename string) (string, error) {
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") || filename == "" {
		return "", fmt.Errorf("invalid note filename %q", filename)
	}
	for index := 0; ; index++ {
		candidate := filename
		if index > 0 {
			extension := filepath.Ext(filename)
			candidate = strings.TrimSuffix(filename, extension) + "-" + strconv.Itoa(index+1) + extension
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
