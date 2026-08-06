package agent

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// SessionRecord is the SQLite catalog row for one Agent session.
type SessionRecord struct {
	ID            string
	PiSessionID   string
	SessionPath   string
	Title         string
	ModelProvider string
	ModelID       string
	ThinkingLevel string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	LastUsedAt    time.Time
	Archived      bool
}

// SessionCatalog persists membox-facing session metadata.
// Full transcripts live in Pi session JSONL files.
type SessionCatalog interface {
	ListSessions(ctx context.Context, includeArchived bool) ([]SessionRecord, error)
	GetSession(ctx context.Context, id string) (SessionRecord, bool, error)
	InsertSession(ctx context.Context, rec SessionRecord) error
	UpdateSession(ctx context.Context, rec SessionRecord) error
	TouchSession(ctx context.Context, id string, at time.Time) error
	ArchiveSession(ctx context.Context, id string, at time.Time) error
}

// validateSessionPath ensures the path is inside the canonical session root.
func validateSessionPath(sessionRoot, sessionPath string) (string, error) {
	root, err := filepath.Abs(sessionRoot)
	if err != nil {
		return "", wrapError(CodeInternal, "resolve session root", err)
	}
	root = filepath.Clean(root)
	abs, err := filepath.Abs(sessionPath)
	if err != nil {
		return "", wrapError(CodeInternal, "resolve session path", err)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmtError(CodeSessionFileMissing, "session path outside root")
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmtError(CodeSessionFileMissing, "session path outside root")
	}
	if !strings.HasSuffix(strings.ToLower(abs), ".jsonl") {
		return "", fmtError(CodeSessionFileMissing, "session path must be a .jsonl file")
	}
	return abs, nil
}

func recordToView(rec SessionRecord, state string, unavailable bool) SessionView {
	var model *ModelRef
	if rec.ModelProvider != "" || rec.ModelID != "" {
		model = &ModelRef{Provider: rec.ModelProvider, ID: rec.ModelID}
	}
	return SessionView{
		ID:            rec.ID,
		Title:         rec.Title,
		State:         state,
		Model:         model,
		ThinkingLevel: rec.ThinkingLevel,
		CreatedAt:     rec.CreatedAt,
		UpdatedAt:     rec.UpdatedAt,
		LastUsedAt:    rec.LastUsedAt,
		Archived:      rec.Archived,
		Unavailable:   unavailable,
	}
}
