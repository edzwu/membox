// Package service implements application use cases for membox.
package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/earendil-works/membox/internal/domain"
	"github.com/earendil-works/membox/internal/perkeep"
	"github.com/earendil-works/membox/internal/repository"
	sqliterepo "github.com/earendil-works/membox/internal/repository/sqlite"
	"github.com/google/uuid"
)

// CreateNoteRequest contains user-provided fields for a new note.
type CreateNoteRequest struct {
	Title   string
	Tags    []string
	Content string
}

// UpdateNoteRequest contains mutable fields for an existing note.
// Pointer fields indicate that the caller wants to update them; nil means
// leave the current value unchanged.
type UpdateNoteRequest struct {
	Title   *string
	Tags    []string
	Content *string
}

// NoteService orchestrates note operations.
// Markdown files are the ground truth; SQLite holds only metadata.
type NoteService struct {
	repo   repository.NoteMetaRepository
	store  repository.NoteStore
	editor string
}

// NewNoteService creates a NoteService backed by the given metadata repo and store.
func NewNoteService(repo repository.NoteMetaRepository, store repository.NoteStore, editor string) *NoteService {
	if editor == "" {
		editor = "vim"
	}
	return &NoteService{repo: repo, store: store, editor: editor}
}

// SetStore replaces the note store used by the service.
// Callers must ensure the new store sees the same logical workspace; this is
// useful when workspace directories change at runtime.
func (s *NoteService) SetStore(store repository.NoteStore) {
	s.store = store
}

// CreateNote creates a new note, writing the markdown file first and then indexing metadata.
// If req.Content is empty and stdin is a TTY, it opens the configured editor.
func (s *NoteService) CreateNote(ctx context.Context, req CreateNoteRequest, interactive bool) (*domain.Note, error) {
	req = normalizeCreate(req)

	// Check for duplicate title before creating anything.
	if existing, err := s.store.FindByTitle(req.Title); err == nil {
		return nil, fmt.Errorf("note with title %q already exists (uuid: %s)", req.Title, existing.UUID)
	}

	now := time.Now().UTC()
	n := &domain.Note{
		UUID:      uuid.NewString(),
		Title:     req.Title,
		Tags:      dedupeStrings(req.Tags),
		Content:   req.Content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if n.Content == "" && interactive {
		if err := s.openEditor(n); err != nil {
			return nil, err
		}
	}

	if err := s.store.Save(n); err != nil {
		return nil, fmt.Errorf("save note file: %w", err)
	}
	if err := s.repo.CreateMeta(ctx, n); err != nil {
		return nil, fmt.Errorf("index note metadata: %w", err)
	}
	return n, nil
}

// GetNote returns a single note by UUID, loading content from the filesystem.
func (s *NoteService) GetNote(ctx context.Context, uuid string) (*domain.Note, error) {
	n, err := s.store.Load(uuid)
	if err != nil {
		return nil, err
	}
	// Reconcile metadata from SQLite.
	meta, err := s.repo.GetMetaByUUID(ctx, uuid)
	if err == nil {
		n.Title = meta.Title
		n.Tags = meta.Tags
		n.CreatedAt = meta.CreatedAt
		n.UpdatedAt = meta.UpdatedAt
	}
	return n, nil
}

// UpdateNote updates a note. The markdown file is rewritten and metadata is re-indexed.
func (s *NoteService) UpdateNote(ctx context.Context, uuid string, req UpdateNoteRequest) (*domain.Note, error) {
	n, err := s.GetNote(ctx, uuid)
	if err != nil {
		return nil, err
	}

	oldTitle := n.Title
	if req.Title != nil {
		n.Title = *req.Title
	}
	if req.Tags != nil {
		n.Tags = dedupeStrings(req.Tags)
	}
	if req.Content != nil {
		n.Content = *req.Content
	}

	// If title changed, ensure the new title does not collide with another note.
	if n.Title != oldTitle {
		if existing, err := s.store.FindByTitle(n.Title); err == nil && existing.UUID != uuid {
			return nil, fmt.Errorf("note with title %q already exists (uuid: %s)", n.Title, existing.UUID)
		}
	}

	n.UpdatedAt = time.Now().UTC()
	if err := s.store.Save(n); err != nil {
		return nil, fmt.Errorf("save note file: %w", err)
	}
	if err := s.repo.UpdateMeta(ctx, n); err != nil {
		return nil, fmt.Errorf("update note metadata: %w", err)
	}
	return n, nil
}

// OpenNote opens the markdown file for the note matching the given UUID prefix.
func (s *NoteService) OpenNote(ctx context.Context, uuidPrefix string) error {
	meta, err := s.repo.FindByUUIDPrefix(ctx, uuidPrefix)
	if err != nil {
		return err
	}

	n, err := s.store.Load(meta.UUID)
	if err != nil {
		return err
	}
	n.Title = meta.Title
	n.Tags = meta.Tags
	n.CreatedAt = meta.CreatedAt
	n.UpdatedAt = meta.UpdatedAt

	path := s.store.Path(n)
	cmd := exec.Command(s.editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}
	return nil
}

// RenameNote changes a note's title. The markdown file is moved and metadata is updated.
func (s *NoteService) RenameNote(ctx context.Context, uuidPrefix, newTitle string) (*domain.Note, error) {
	meta, err := s.repo.FindByUUIDPrefix(ctx, uuidPrefix)
	if err != nil {
		return nil, err
	}

	if existing, err := s.store.FindByTitle(newTitle); err == nil && existing.UUID != meta.UUID {
		return nil, fmt.Errorf("note with title %q already exists (uuid: %s)", newTitle, existing.UUID)
	}

	n, err := s.store.Load(meta.UUID)
	if err != nil {
		return nil, err
	}

	n.Title = newTitle
	n.Tags = meta.Tags
	n.CreatedAt = meta.CreatedAt
	n.UpdatedAt = time.Now().UTC()

	if err := s.store.Save(n); err != nil {
		return nil, fmt.Errorf("save note file: %w", err)
	}
	if err := s.repo.UpdateMeta(ctx, n); err != nil {
		return nil, fmt.Errorf("update note metadata: %w", err)
	}
	return n, nil
}

// DeleteNote removes a note from both the filesystem and the metadata index.
func (s *NoteService) DeleteNote(ctx context.Context, uuid string) error {
	if err := s.store.Delete(uuid); err != nil {
		return fmt.Errorf("delete note file: %w", err)
	}
	if err := s.repo.DeleteMeta(ctx, uuid); err != nil {
		return fmt.Errorf("delete note metadata: %w", err)
	}
	return nil
}

// ListNotes returns recent notes, loading content from the filesystem.
// If includeTags/excludeTags are non-empty, only notes matching the tag rules are returned.
func (s *NoteService) ListNotes(ctx context.Context, includeTags, excludeTags []string, limit int) ([]*domain.Note, error) {
	metas, err := s.repo.ListMeta(ctx, includeTags, excludeTags, limit)
	if err != nil {
		return nil, err
	}

	out := make([]*domain.Note, 0, len(metas))
	for _, meta := range metas {
		n, err := s.store.Load(meta.UUID)
		if err != nil {
			// Metadata exists but file is missing: skip silently for now.
			continue
		}
		// Prefer metadata from SQLite for title/tags/timestamps.
		n.Title = meta.Title
		n.Tags = meta.Tags
		n.CreatedAt = meta.CreatedAt
		n.UpdatedAt = meta.UpdatedAt
		out = append(out, n)
	}
	return out, nil
}

// SearchNotes performs full-text search over note titles.
func (s *NoteService) SearchNotes(ctx context.Context, query string, limit int) ([]*repository.NoteSearchResult, error) {
	results, err := s.repo.SearchMeta(ctx, query, limit)
	if err != nil {
		return nil, err
	}

	for _, r := range results {
		n, err := s.store.Load(r.Note.UUID)
		if err == nil {
			r.Note.Content = n.Content
		}
	}
	return results, nil
}

// Sync reconciles the SQLite metadata index with the filesystem.
//
// It does the following:
//   - Create metadata for new markdown files.
//   - Update updated_at if the file modification time has changed.
//   - Update title if a file was renamed on disk.
//   - Delete metadata for files that no longer exist.
func (s *NoteService) Sync(ctx context.Context) (*SyncResult, error) {
	files, err := s.store.Scan()
	if err != nil {
		return nil, fmt.Errorf("scan notes: %w", err)
	}

	res := &SyncResult{Total: len(files)}
	fileUUIDs := make(map[string]*domain.Note, len(files))
	for _, n := range files {
		fileUUIDs[n.UUID] = n
	}

	// 1. Reconcile existing files.
	for _, fileNote := range files {
		meta, err := s.repo.GetMetaByUUID(ctx, fileNote.UUID)
		if err == repository.ErrNotFound {
			// New file discovered: create metadata, preserving created_at from the file
			// mtime when no better source exists.
			fileNote.CreatedAt = fileNote.UpdatedAt
			if err := s.repo.CreateMeta(ctx, fileNote); err != nil {
				res.Errors++
				res.Errs = append(res.Errs, fmt.Errorf("create %s: %w", fileNote.UUID, err))
				continue
			}
			res.Created++
			continue
		}
		if err != nil {
			res.Errors++
			res.Errs = append(res.Errs, fmt.Errorf("read %s: %w", fileNote.UUID, err))
			continue
		}

		needsUpdate := false
		merged := *meta

		// Update title if the file was renamed.
		if fileNote.Title != meta.Title {
			merged.Title = fileNote.Title
			needsUpdate = true
		}

		// Update updated_at if the file mtime changed.
		if !fileNote.UpdatedAt.Equal(meta.UpdatedAt) {
			merged.UpdatedAt = fileNote.UpdatedAt
			needsUpdate = true
		}

		if needsUpdate {
			if err := s.repo.UpdateMeta(ctx, &merged); err != nil {
				res.Errors++
				res.Errs = append(res.Errs, fmt.Errorf("update %s: %w", fileNote.UUID, err))
				continue
			}
			res.Updated++
		} else {
			res.Unchanged++
		}
	}

	// 2. Remove metadata for missing files.
	allMeta, err := s.repo.ListMeta(ctx, nil, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("list metadata for cleanup: %w", err)
	}
	for _, meta := range allMeta {
		if _, ok := fileUUIDs[meta.UUID]; !ok {
			if err := s.repo.DeleteMeta(ctx, meta.UUID); err != nil {
				res.Errors++
				res.Errs = append(res.Errs, fmt.Errorf("delete %s: %w", meta.UUID, err))
				continue
			}
			res.Deleted++
		}
	}

	return res, nil
}

// SyncResult reports the outcome of a sync operation.
type SyncResult struct {
	Total     int
	Created   int
	Updated   int
	Unchanged int
	Deleted   int
	Errors    int
	Errs      []error
}

// SyncToPerkeep uploads any changed local notes to Perkeep and records the
// resulting permanode blobrefs in the perkeep_sync table.
//
// If server is non-empty, it is used as the Perkeep server prefix (e.g.
// "http://127.0.0.1:3179"). Otherwise the MM_PERKEEP_SERVER environment
// variable is consulted; if neither is set, the perkeep binaries use their
// default client configuration.
func (s *NoteService) SyncToPerkeep(ctx context.Context, syncRepo *sqliterepo.PerkeepSyncRepo, cacheRoot, server string) (*perkeep.SyncResult, error) {
	if server == "" {
		server = os.Getenv("MM_PERKEEP_SERVER")
	}
	var client *perkeep.Client
	var err error
	if server != "" {
		client, err = perkeep.NewClientWithServer(server)
	} else {
		client, err = perkeep.NewClient()
	}
	if err != nil {
		return nil, fmt.Errorf("create perkeep client: %w", err)
	}
	defer client.Close()
	syncer := perkeep.NewSyncer(client, s.store, s.repo, syncRepo, cacheRoot)
	return syncer.Push(ctx)
}

// openEditor creates a markdown file with minimal frontmatter and opens it in the user's editor.
// After the editor exits, it reads the content back into n.Content.
func (s *NoteService) openEditor(n *domain.Note) error {
	path := s.store.Path(n)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create note dir: %w", err)
	}

	initial := "---\n" +
		"uuid: " + n.UUID + "\n" +
		"---\n\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		return fmt.Errorf("write draft note: %w", err)
	}

	cmd := exec.Command(s.editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("editor failed: %w", err)
	}

	loaded, err := s.store.Load(n.UUID)
	if err != nil {
		return fmt.Errorf("read note after editing: %w", err)
	}
	n.Content = loaded.Content
	return nil
}

// normalizeCreate applies defaults and trims whitespace.
func normalizeCreate(req CreateNoteRequest) CreateNoteRequest {
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "Untitled"
	}
	req.Content = strings.TrimSpace(req.Content)
	return req
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
