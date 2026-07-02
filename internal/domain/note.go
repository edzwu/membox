// Package domain contains the core business entities for membox.
package domain

import "time"

// Note represents a markdown document stored in membox.
// It is storage-agnostic and holds the UUID metadata that links the SQLite
// record to a file on disk.
type Note struct {
	UUID      string
	Title     string
	URL       string
	Tags      []string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// HasTag reports whether the note has the given tag.
func (n *Note) HasTag(tag string) bool {
	for _, t := range n.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
