// Package filesystem implements repository.NoteStore using markdown files.
package filesystem

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/earendil-works/membox/internal/domain"
	"github.com/earendil-works/membox/internal/repository"
)

const frontmatterSep = "---"

// NoteStore stores notes as markdown files with minimal YAML frontmatter.
// It can span multiple notes directories (a workspace).
type NoteStore struct {
	notesDirs []string
}

// NewNoteStore creates a filesystem-backed note store over one or more notes
// directories. The first directory is the default write target for new notes.
func NewNoteStore(notesDirs []string) *NoteStore {
	return &NoteStore{notesDirs: notesDirs}
}

// Path returns the filesystem path for a note based on its title.
// New notes are written to the first notes directory.
func (s *NoteStore) Path(n *domain.Note) string {
	return filepath.Join(s.notesDirs[0], sanitize(n.Title)+".md")
}

// Save writes a note to <notesDir>/<title>.md.
// If the note already exists at a different path (e.g. title changed), it is
// renamed. The note remains in its original notes directory if it already
// exists; otherwise it is written to the first directory.
func (s *NoteStore) Save(n *domain.Note) error {
	existing, _ := s.FindByUUID(n.UUID)

	path := s.Path(n)
	if existing != "" {
		// Keep the note in its existing workspace directory even if the title
		// changes; only the filename derived from the title changes.
		path = filepath.Join(filepath.Dir(existing), sanitize(n.Title)+".md")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create note dir: %w", err)
	}

	if existing != "" && existing != path {
		if err := os.Rename(existing, path); err != nil {
			return fmt.Errorf("rename note file: %w", err)
		}
	}

	content := frontmatterSep + "\n" +
		"uuid: " + n.UUID + "\n" +
		frontmatterSep + "\n\n" +
		n.Content

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write note file: %w", err)
	}
	return nil
}

// Load reads a note by UUID, scanning all notes directories.
func (s *NoteStore) Load(uuid string) (*domain.Note, error) {
	matches, err := s.allMarkdownFiles()
	if err != nil {
		return nil, err
	}
	for _, p := range matches {
		n, err := parseFile(p)
		if err != nil {
			continue
		}
		if n.UUID == uuid {
			return n, nil
		}
	}
	return nil, repository.ErrNotFound
}

// Delete removes a note file by UUID from any notes directory.
func (s *NoteStore) Delete(uuid string) error {
	matches, err := s.allMarkdownFiles()
	if err != nil {
		return err
	}
	for _, p := range matches {
		n, err := parseFile(p)
		if err != nil {
			continue
		}
		if n.UUID == uuid {
			return os.Remove(p)
		}
	}
	return repository.ErrNotFound
}

// Scan returns metadata for all notes found across all notes directories.
func (s *NoteStore) Scan() ([]*domain.Note, error) {
	matches, err := s.allMarkdownFiles()
	if err != nil {
		return nil, err
	}

	var out []*domain.Note
	for _, p := range matches {
		n, err := parseFile(p)
		if err != nil {
			continue
		}
		// Infer title from the filename since the frontmatter intentionally
		// does not store it.
		n.Title = strings.TrimSuffix(filepath.Base(p), ".md")
		// File modification time is the only source of truth for updated_at.
		if info, err := os.Stat(p); err == nil {
			n.UpdatedAt = info.ModTime().UTC()
		}
		out = append(out, n)
	}
	return out, nil
}

// FindByUUID returns the filesystem path of the note with the given UUID.
func (s *NoteStore) FindByUUID(uuid string) (string, error) {
	matches, err := s.allMarkdownFiles()
	if err != nil {
		return "", err
	}
	for _, p := range matches {
		n, err := parseFile(p)
		if err != nil {
			continue
		}
		if n.UUID == uuid {
			return p, nil
		}
	}
	return "", repository.ErrNotFound
}

// FindByTitle checks whether a note with the given title already exists in
// any notes directory.
func (s *NoteStore) FindByTitle(title string) (*domain.Note, error) {
	for _, dir := range s.notesDirs {
		path := filepath.Join(dir, sanitize(title)+".md")
		if _, err := os.Stat(path); err == nil {
			return parseFile(path)
		}
	}
	return nil, repository.ErrNotFound
}

// allMarkdownFiles returns all *.md files across all notes directories.
func (s *NoteStore) allMarkdownFiles() ([]string, error) {
	var out []string
	for _, dir := range s.notesDirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			return nil, fmt.Errorf("scan notes dir %q: %w", dir, err)
		}
		out = append(out, matches...)
	}
	return out, nil
}

func sanitize(s string) string {
	return strings.ReplaceAll(s, "/", "_")
}

func parseFile(path string) (*domain.Note, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open note file: %w", err)
	}
	defer f.Close()

	n := &domain.Note{}
	scanner := bufio.NewScanner(f)

	// First line must be "---"
	if !scanner.Scan() || scanner.Text() != frontmatterSep {
		return nil, fmt.Errorf("missing frontmatter separator in %s", path)
	}

	inFrontmatter := true
	var contentLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if inFrontmatter {
			if line == frontmatterSep {
				inFrontmatter = false
				continue
			}
			if err := parseFrontmatterLine(n, line); err != nil {
				return nil, fmt.Errorf("parse frontmatter in %s: %w", path, err)
			}
			continue
		}
		contentLines = append(contentLines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read note file: %w", err)
	}

	// Trim leading blank lines from content.
	start := 0
	for start < len(contentLines) && strings.TrimSpace(contentLines[start]) == "" {
		start++
	}
	n.Content = strings.Join(contentLines[start:], "\n")

	if n.UUID == "" {
		return nil, fmt.Errorf("missing uuid in %s", path)
	}

	// The title is intentionally not stored in frontmatter. Infer it from the
	// flat markdown filename so callers that load by UUID can still resolve the
	// canonical path later.
	n.Title = strings.TrimSuffix(filepath.Base(path), ".md")

	return n, nil
}

func parseFrontmatterLine(n *domain.Note, line string) error {
	key, val, ok := strings.Cut(line, ":")
	if !ok {
		return nil
	}
	key = strings.TrimSpace(key)
	val = strings.TrimSpace(val)

	switch key {
	case "uuid":
		n.UUID = val
	}
	return nil
}
