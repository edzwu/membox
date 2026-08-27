package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"membox/internal/domain/catalog"
)

// QuickNoteDraft is an unindexed scratch file. Ctrl+N opens this path
// immediately; indexing, graph linking, and final naming happen after the
// editor exits so durable catalog work is never on the editor-launch path.
type QuickNoteDraft struct {
	Path string
}

// CreateQuickNoteDraft reserves a collision-resistant Markdown file in the
// default notes path without indexing it. If the process dies while editing,
// the ordinary path scanner can still recover the visible untitled-* file.
func (s *Service) CreateQuickNoteDraft(ctx context.Context) (QuickNoteDraft, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return QuickNoteDraft{}, lockErr
	}
	defer release()

	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return QuickNoteDraft{}, err
	}
	id, err := s.ids.NewDocumentID()
	if err != nil {
		return QuickNoteDraft{}, err
	}
	token := strings.ToLower(strings.ReplaceAll(string(id), "-", ""))
	if len(token) > 12 {
		token = token[len(token)-12:]
	}
	if token == "" {
		return QuickNoteDraft{}, errors.New("quick note draft id is empty")
	}
	absolute := filepath.Join(indexedPath.Root, "untitled-"+token+".md")
	if err := s.writer.WriteNew(ctx, absolute, []byte("\n")); err != nil {
		return QuickNoteDraft{}, err
	}
	return QuickNoteDraft{Path: absolute}, nil
}

// FinalizeQuickNoteDraft publishes one draft after its editor exits. H1 notes
// receive their final title immediately. Prose-only notes get a timestamp title
// and NeedsNaming=true so the TUI can ask mmd to improve it in the background.
func (s *Service) FinalizeQuickNoteDraft(ctx context.Context, draftPath, fromSelector string) (FinalizeQuickNoteResult, error) {
	indexedPath, absolute, err := s.resolveQuickNoteDraftPath(ctx, draftPath)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	bodyBytes, err := s.reader.Read(ctx, absolute)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	body := string(bodyBytes)
	title := strings.TrimSpace(ExtractFirstMarkdownH1(body))
	needsNaming := title == "" && strings.TrimSpace(stripMarkdownFrontMatter(body)) != ""
	if needsNaming {
		title = fallbackQuickNoteTitle(s.clock.Now())
	}

	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return FinalizeQuickNoteResult{}, lockErr
	}
	defer release()

	// Re-read after taking the lock. The editor has exited, but this also makes
	// an external last-millisecond write authoritative.
	bodyBytes, err = s.reader.Read(ctx, absolute)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	body = string(bodyBytes)
	if strings.TrimSpace(stripMarkdownFrontMatter(body)) == "" {
		return s.discardQuickNoteDraft(ctx, indexedPath, absolute)
	}
	if h1 := strings.TrimSpace(ExtractFirstMarkdownH1(body)); h1 != "" {
		title, needsNaming = h1, false
	} else if !needsNaming || title == "" {
		title, needsNaming = fallbackQuickNoteTitle(s.clock.Now()), true
	}

	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	relative, err := filepath.Rel(indexedPath.Root, absolute)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	relative = filepath.ToSlash(relative)
	var existing *catalog.Document
	tracked := make(map[string]bool, len(documents))
	for _, document := range documents {
		if filepath.ToSlash(document.Location.RelativePath) == relative && document.Status == catalog.DocumentActive {
			existing = document
			continue
		}
		tracked[filepath.ToSlash(document.Location.RelativePath)] = true
	}

	var from *catalog.Document
	if strings.TrimSpace(fromSelector) != "" {
		from, _, err = s.ResolveDocument(ctx, fromSelector)
		if err != nil {
			return FinalizeQuickNoteResult{}, err
		}
	}

	slug := noteSlug(title)
	if slug == "" {
		slug = "note"
	}
	desired := slug + ".md"
	target, err := s.availableNotePath(indexedPath.Root, desired, tracked)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	newFilename := filepath.Base(target)

	var document *catalog.Document
	if existing != nil {
		renamed, renameErr := s.RenameDocument(ctx, string(existing.ID), newFilename, title)
		if renameErr != nil {
			return FinalizeQuickNoteResult{}, renameErr
		}
		document, target, err = s.ResolveDocument(ctx, string(renamed.DocumentID))
		if err != nil {
			return FinalizeQuickNoteResult{}, err
		}
	} else {
		if err := s.writer.Move(ctx, absolute, target); err != nil {
			return FinalizeQuickNoteResult{}, err
		}
		document, err = s.indexNewFileWithTitle(ctx, indexedPath, target, title)
		if err != nil {
			// Keep the user's editor path retryable when publication fails.
			rollbackErr := s.writer.Move(ctx, target, absolute)
			return FinalizeQuickNoteResult{}, errors.Join(err, rollbackErr)
		}
	}

	if from != nil {
		edge, edgeErr := catalog.NewGraphEdge(from.ID, document.ID, catalog.EdgeManual, s.clock.Now())
		if edgeErr != nil {
			return FinalizeQuickNoteResult{}, edgeErr
		}
		if _, edgeErr = s.store.AddEdge(ctx, edge); edgeErr != nil {
			return FinalizeQuickNoteResult{}, edgeErr
		}
	}

	return FinalizeQuickNoteResult{
		Document:    document,
		Path:        target,
		Title:       title,
		Filename:    newFilename,
		NeedsNaming: needsNaming,
	}, nil
}

func (s *Service) resolveQuickNoteDraftPath(ctx context.Context, draftPath string) (*catalog.IndexedPath, string, error) {
	indexedPath, err := s.defaultCreatePath(ctx)
	if err != nil {
		return nil, "", err
	}
	root, err := filepath.Abs(indexedPath.Root)
	if err != nil {
		return nil, "", err
	}
	absolute, err := filepath.Abs(strings.TrimSpace(draftPath))
	if err != nil {
		return nil, "", err
	}
	base := filepath.Base(absolute)
	if filepath.Dir(absolute) != root || !strings.HasPrefix(strings.ToLower(base), "untitled-") || !validFlatMarkdownFilename(base) {
		return nil, "", fmt.Errorf("invalid quick note draft path %q", draftPath)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("quick note draft is not a regular file: %s", absolute)
	}
	return indexedPath, absolute, nil
}

func (s *Service) discardQuickNoteDraft(ctx context.Context, indexedPath *catalog.IndexedPath, absolute string) (FinalizeQuickNoteResult, error) {
	relative, err := filepath.Rel(indexedPath.Root, absolute)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	documents, err := s.store.DocumentsForPath(ctx, indexedPath.ID)
	if err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	for _, document := range documents {
		if filepath.ToSlash(document.Location.RelativePath) != filepath.ToSlash(relative) || document.Status != catalog.DocumentActive {
			continue
		}
		if _, _, err := s.TrashDocumentFile(ctx, string(document.ID)); err != nil {
			return FinalizeQuickNoteResult{}, err
		}
		return FinalizeQuickNoteResult{Document: document, Path: absolute, Filename: filepath.Base(absolute), Deleted: true}, nil
	}
	if err := s.writer.Remove(ctx, absolute); err != nil {
		return FinalizeQuickNoteResult{}, err
	}
	return FinalizeQuickNoteResult{Path: absolute, Filename: filepath.Base(absolute), Deleted: true}, nil
}
