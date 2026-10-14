package postgres

import (
	"github.com/google/uuid"
)

// newUUID mints a UUIDv7 — the same identity semantics as the local node
// (ADR-001), so a document keeps one stable ID across nodes.
func newUUID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// UUIDv7 generation only fails when the RNG is broken; fall back to v4
		// rather than crash a request path.
		return uuid.NewString()
	}
	return id.String()
}

// NewDocumentID adapts newUUID to remotedocs.IDGenerator.
func NewDocumentID() (string, error) { return newUUID(), nil }
