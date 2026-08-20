package backend

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"membox/internal/bootstrap"
)

// TestRepairDuplicateNotes is a one-shot repair for two defects caused by the
// old reconcile bug (ref-less saves never matched multi-line excerpts, so every
// save created another identical *-note.md file):
//
//  1. Duplicate notes: identical excerpt+note documents are collapsed to the
//     oldest one; extra files, rows, and edges are deleted.
//  2. Orphaned notes: note files that lost their annotation_notes row are
//     re-anchored to the document whose text still contains their excerpt.
//
// Stop any running `mm` server first, then:
//
//	go test ./internal/web/backend -run TestRepairDuplicateNotes -v
//	MEMBOX_REPAIR_APPLY=1 go test ./internal/web/backend -run TestRepairDuplicateNotes -v
//
// The default is a dry run that only prints the plan.
func TestRepairDuplicateNotes(t *testing.T) {
	dbPath := os.Getenv("MEMBOX_REPAIR_DB")
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("cannot resolve home dir: %v", err)
		}
		dbPath = filepath.Join(home, ".membox", "membox.db")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("no membox database at %s", dbPath)
	}
	apply := os.Getenv("MEMBOX_REPAIR_APPLY") == "1"
	if !apply {
		t.Log("DRY RUN — set MEMBOX_REPAIR_APPLY=1 to execute")
	}

	ctx := context.Background()
	service, err := bootstrap.Open(dbPath)
	if err != nil {
		t.Fatalf("opening %s: %v", dbPath, err)
	}
	defer func() { _ = service.Close() }()
	server := NewServer(service, fstest.MapFS{}, fstest.MapFS{})

	records, err := service.ListDocuments(ctx, 5000, false, "")
	if err != nil {
		t.Fatalf("listing documents: %v", err)
	}
	if len(records) == 5000 {
		t.Fatalf("more than 5000 documents; raise the limit before repairing")
	}
	if err != nil {
		t.Fatalf("listing documents: %v", err)
	}

	type noteDoc struct {
		id        string
		path      string
		dir       string
		excerpt   string
		note      string
		hasRow    bool
		rowTarget string
	}
	var notes []*noteDoc
	type candidateTarget struct {
		id        string
		denseText string
		annotRows int
	}
	var candidates []candidateTarget

	for _, record := range records {
		base := filepath.Base(record.AbsolutePath)
		isNote := strings.HasSuffix(base, "-note.md") || strings.Contains(base, "-note-") && strings.HasSuffix(base, ".md")
		if !isNote {
			body, readErr := service.ReadDocument(ctx, string(record.Document.ID))
			if readErr != nil {
				continue
			}
			dense, _ := denseRunes(markdownToCanonicalText(string(body)))
			rowCount := 0
			if _, rows, listErr := service.ListAnnotationNotes(ctx, string(record.Document.ID)); listErr == nil {
				rowCount = len(rows)
			}
			candidates = append(candidates, candidateTarget{id: string(record.Document.ID), denseText: dense, annotRows: rowCount})
			continue
		}
		row, ok, _ := service.GetAnnotationNote(ctx, string(record.Document.ID))
		body, readErr := service.ReadDocument(ctx, string(record.Document.ID))
		if readErr != nil {
			continue
		}
		excerpt, noteText, _ := parseClipBody(string(body))
		if strings.TrimSpace(excerpt) == "" {
			continue
		}
		nd := &noteDoc{
			id:      string(record.Document.ID),
			path:    record.AbsolutePath,
			dir:     filepath.Dir(record.AbsolutePath),
			excerpt: excerpt,
			note:    strings.TrimSpace(noteText),
			hasRow:  ok,
		}
		if ok {
			nd.rowTarget = string(row.TargetDocumentID)
		}
		notes = append(notes, nd)
	}

	// Group identical notes (dense excerpt + note text).
	groups := map[string][]*noteDoc{}
	for _, nd := range notes {
		key := denseExcerpt(nd.excerpt) + "\x00" + nd.note
		groups[key] = append(groups[key], nd)
	}

	var relinked, deletedWithRow, deletedBare, skipped int
	var db *sql.DB
	ensureDB := func() *sql.DB {
		if db == nil {
			opened, openErr := sql.Open("sqlite", "file:"+dbPath)
			if openErr != nil {
				t.Fatalf("opening raw sqlite handle: %v", openErr)
			}
			// Cascade document deletions into locations/index/sources/edges.
			if _, pragmaErr := opened.ExecContext(ctx, `PRAGMA foreign_keys=ON`); pragmaErr != nil {
				t.Fatalf("enabling foreign keys: %v", pragmaErr)
			}
			db = opened
		}
		return db
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		members := groups[key]
		// Deterministic keeper: UUIDv7 sorts chronologically.
		sort.Slice(members, func(i, j int) bool { return members[i].id < members[j].id })
		keeper := members[0]

		target := keeper.rowTarget
		if target == "" {
			for _, member := range members[1:] {
				if member.rowTarget != "" {
					target = member.rowTarget
					break
				}
			}
		}
		if target == "" {
			// Find the document whose text contains the excerpt; prefer the
			// same directory, then the document with the most annotations.
			want := denseExcerpt(keeper.excerpt)
			var best *candidateTarget
			for i := range candidates {
				c := &candidates[i]
				if !strings.Contains(c.denseText, want) {
					continue
				}
				if best == nil || c.annotRows > best.annotRows {
					best = c
				}
			}
			if best == nil {
				skipped++
				t.Logf("SKIP   %s (excerpt not found in any document)", filepath.Base(keeper.path))
				continue
			}
			target = best.id
		}

		if !keeper.hasRow {
			t.Logf("RELINK %s -> target %s", filepath.Base(keeper.path), target)
			if apply {
				if ok, upsertErr := server.upsertClipAnnotationRelation(ctx, target, keeper.id, keeper.excerpt); upsertErr != nil || !ok {
					t.Errorf("relink failed for %s: ok=%v err=%v", keeper.id, ok, upsertErr)
					skipped++
					continue
				}
			}
			relinked++
		}

		for _, member := range members[1:] {
			if member.hasRow {
				t.Logf("DELETE %s (duplicate; row+edge+file via DeleteAnnotationNote)", filepath.Base(member.path))
				if apply {
					if delErr := service.DeleteAnnotationNote(ctx, member.rowTarget, member.id); delErr != nil {
						t.Errorf("deleting duplicate %s: %v", member.id, delErr)
						continue
					}
				}
				deletedWithRow++
			} else {
				t.Logf("DELETE %s (duplicate; file + orphan document row)", filepath.Base(member.path))
				if apply {
					if rmErr := os.Remove(member.path); rmErr != nil {
						t.Errorf("removing file %s: %v", member.path, rmErr)
						continue
					}
					if _, sqlErr := ensureDB().ExecContext(ctx, `DELETE FROM documents WHERE id=?`, member.id); sqlErr != nil {
						t.Errorf("removing orphan document row %s: %v", member.id, sqlErr)
						continue
					}
				}
				deletedBare++
			}
		}
	}

	if db != nil {
		_ = db.Close()
	}
	t.Logf("SUMMARY: relinked=%d deleted_duplicates_with_row=%d deleted_duplicates_bare=%d skipped=%d (apply=%v)",
		relinked, deletedWithRow, deletedBare, skipped, apply)
}
