// Package repository defines the persistence interfaces used by services.
package repository

import (
	"context"
	"errors"

	"github.com/earendil-works/membox/internal/domain"
)

// Common errors returned by repository implementations.
var (
	ErrNotFound = errors.New("note not found")
	ErrExists   = errors.New("note already exists")
)

// NoteSearchResult is a lightweight projection returned by full-text search.
type NoteSearchResult struct {
	Note    *domain.Note
	Snippet string
}

// NoteMetaRepository abstracts metadata storage for notes.
// The markdown content itself lives in a NoteStore implementation.
type NoteMetaRepository interface {
	// CreateMeta persists metadata for a new note.
	CreateMeta(ctx context.Context, n *domain.Note) error

	// GetMetaByUUID loads metadata for a single note by UUID.
	GetMetaByUUID(ctx context.Context, uuid string) (*domain.Note, error)

	// UpdateMeta replaces the mutable metadata fields of an existing note.
	UpdateMeta(ctx context.Context, n *domain.Note) error

	// DeleteMeta removes metadata for a note by UUID.
	DeleteMeta(ctx context.Context, uuid string) error

	// ListMeta returns recent note metadata, optionally filtered by included/excluded tags.
	ListMeta(ctx context.Context, includeTags, excludeTags []string, limit int) ([]*domain.Note, error)

	// SearchMeta performs full-text search over note titles.
	SearchMeta(ctx context.Context, query string, limit int) ([]*NoteSearchResult, error)

	// FindByUUIDPrefix returns metadata for the note whose UUID starts with the given prefix.
	// If no note or more than one note matches, an error is returned.
	FindByUUIDPrefix(ctx context.Context, prefix string) (*domain.Note, error)
}

// NoteStore abstracts markdown content storage for notes.
// Implementations may use the local filesystem or a remote daemon.
type NoteStore interface {
	// Save writes or overwrites the markdown file for a note.
	Save(n *domain.Note) error

	// Load reads the markdown file for a note and returns the full note including content.
	Load(uuid string) (*domain.Note, error)

	// Delete removes the markdown file for a note.
	Delete(uuid string) error

	// Scan returns the metadata for all notes found in the store.
	// It is used by the sync command to rebuild the metadata index.
	Scan() ([]*domain.Note, error)

	// Path returns the filesystem path that would be used for the given note.
	Path(n *domain.Note) string

	// FindByTitle returns the note at the canonical path for title.
	FindByTitle(title string) (*domain.Note, error)
}
