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

// TestRepairMisassignedNotes is a one-shot repair for notes whose
// annotation_notes target and anchors point at the wrong document.
//
// Symptom: Go/Effective-Go selection notes (created 08-04..08-06) have
// target_document_id + anchor offsets (76k..84k) that belong to a ~100KB
// article body, but the target document's file was later replaced by an
// unrelated note (bubble-sort.md), so the notes can no longer be viewed as
// annotations on their article.
//
// The repair:
//  1. Re-anchors each *-note.md to the live document whose canonical text
//     still contains its excerpt (preferring clipped page documents).
//  2. Removes graph edges that point at the misassigned target.
//  3. Recovers missing *-note.md files from the FTS index body, reindexes
//     them, and re-anchors them the same way.
//
// Stop any running `mm` server first, then:
//
//	go test ./internal/web/backend -run TestRepairMisassignedNotes -v
//	MEMBOX_REPAIR_APPLY=1 go test ./internal/web/backend -run TestRepairMisassignedNotes -v
//
// The default is a dry run that only prints the plan.
func TestRepairMisassignedNotes(t *testing.T) {
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

	records, err := service.ListDocuments(ctx, 1000, true, "")
	if err != nil {
		t.Fatalf("listing documents: %v", err)
	}
	if len(records) == 1000 {
		t.Fatalf("more than 1000 documents; raise the repository limit before repairing")
	}

	// Candidate targets: live, non-note documents, dense canonical text.
	type target struct {
		id     string
		dense  string
		page   bool // has a clipped source_url
		length int
	}
	// Pages with clip_mode=page, resolved via raw sqlite (no service helper needed).
	pageIDs := map[string]bool{}
	if pageDB, pageErr := sql.Open("sqlite", "file:"+dbPath); pageErr == nil {
		rows, qErr := pageDB.QueryContext(ctx, `SELECT document_id FROM document_sources WHERE clip_mode='page'`)
		if qErr == nil {
			for rows.Next() {
				var pid string
				if rows.Scan(&pid) == nil {
					pageIDs[pid] = true
				}
			}
			rows.Close()
		}
		_ = pageDB.Close()
	}
	var targets []*target
	for _, rec := range records {
		base := filepath.Base(rec.AbsolutePath)
		if isNoteFile(base) {
			continue
		}
		body, readErr := service.ReadDocument(ctx, string(rec.Document.ID))
		if readErr != nil || len(body) == 0 {
			continue
		}
		dense, _ := denseRunes(markdownToCanonicalText(string(body)))
		if len(dense) < 50 {
			continue
		}
		targets = append(targets, &target{
			id:     string(rec.Document.ID),
			dense:  dense,
			page:   pageIDs[string(rec.Document.ID)],
			length: len(dense),
		})
	}

	// Prefer clipped pages, then larger documents.
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].page != targets[j].page {
			return targets[i].page
		}
		return targets[i].length > targets[j].length
	})

	// Note documents: read body (file, or FTS when missing).
	type noteDoc struct {
		id       string
		absPath  string
		missing  bool
		body     string
		excerpt  string
		curTarget string
	}
	var notes []*noteDoc
	for _, rec := range records {
		base := filepath.Base(rec.AbsolutePath)
		if !isNoteFile(base) {
			continue
		}
		missing := rec.Document.Status != "active"
		body := []byte{}
		if !missing {
			body, err = service.ReadDocument(ctx, string(rec.Document.ID))
			if err != nil {
				continue
			}
		} else {
			// Recover from the FTS index (the file is gone).
			if ftsBody, ok := ftsBodyFor(ctx, dbPath, string(rec.Document.ID)); ok {
				body = []byte(ftsBody)
			}
		}
		if len(body) == 0 {
			continue
		}
		excerpt, _, _ := parseClipBody(string(body))
		if strings.TrimSpace(excerpt) == "" {
			continue
		}
		curTarget := ""
		if row, ok, rowErr := service.GetAnnotationNote(ctx, string(rec.Document.ID)); rowErr == nil && ok {
			curTarget = string(row.TargetDocumentID)
		}
		notes = append(notes, &noteDoc{
			id:        string(rec.Document.ID),
			absPath:   rec.AbsolutePath,
			missing:   missing,
			body:      string(body),
			excerpt:   excerpt,
			curTarget: curTarget,
		})
	}

	ensureDB := func() *sql.DB {
		opened, openErr := sql.Open("sqlite", "file:"+dbPath)
		if openErr != nil {
			t.Fatalf("opening raw sqlite handle: %v", openErr)
		}
		if _, pragmaErr := opened.ExecContext(ctx, `PRAGMA foreign_keys=ON`); pragmaErr != nil {
			t.Fatalf("enabling foreign keys: %v", pragmaErr)
		}
		return opened
	}

	var recovered, relinked, edgesCleaned, skipped int
	for _, nd := range notes {
		want := denseExcerpt(nd.excerpt)
		var best *target
		for _, c := range targets {
			if strings.Contains(c.dense, want) {
				best = c
				break
			}
		}

		// 1. Recover missing file from FTS body.
		if nd.missing {
			t.Logf("RECOVER %s (%s) -> %d bytes", filepath.Base(nd.absPath), nd.id[:8], len(nd.body))
			if apply {
				if err := os.MkdirAll(filepath.Dir(nd.absPath), 0o700); err != nil {
					t.Errorf("mkdir %s: %v", filepath.Dir(nd.absPath), err)
					continue
				}
				if err := os.WriteFile(nd.absPath, []byte(nd.body), 0o600); err != nil {
					t.Errorf("write %s: %v", nd.absPath, err)
					continue
				}
				db := ensureDB()
				if _, upErr := db.ExecContext(ctx, `UPDATE document_locations SET status='active' WHERE document_id=?`, nd.id); upErr != nil {
					t.Errorf("mark active %s: %v", nd.id, upErr)
					continue
				}
				if idxErr := service.ReindexDocument(ctx, nd.id); idxErr != nil {
					t.Errorf("reindex %s: %v", nd.id, idxErr)
				}
			}
			recovered++
		}

		if best == nil {
			// No live page contains this excerpt. Detach from any wrong target so
			// Miru does not try to project the note onto an unrelated document
			// (e.g. bubble-sort) with anchors beyond its body; the note file
			// stays fully viewable as a standalone document.
			if nd.curTarget != "" {
				t.Logf("DETACH %s (%s) from %s (excerpt not in any live document)", filepath.Base(nd.absPath), nd.id[:8], shortTarget(nd.curTarget))
				if apply {
					db := ensureDB()
					if _, delErr := db.ExecContext(ctx, `DELETE FROM annotation_notes WHERE note_document_id=? AND target_document_id=?`, nd.id, nd.curTarget); delErr != nil {
						t.Errorf("detach row %s: %v", nd.id, delErr)
					} else {
						recovered++
					}
					if _, unlinkErr := db.ExecContext(ctx, `DELETE FROM graph_edges WHERE from_document_id=? AND to_document_id=?`, nd.curTarget, nd.id); unlinkErr == nil {
						edgesCleaned++
					}
				}
			} else {
				skipped++
				t.Logf("SKIP   %s (excerpt not found in any live document)", filepath.Base(nd.absPath))
			}
			continue
		}

		// 2. Re-anchor to the correct target.
		if best.id != nd.curTarget {
			t.Logf("RELINK %s (%s) target %s -> %s", filepath.Base(nd.absPath), nd.id[:8], shortTarget(nd.curTarget), best.id[:8])
			if apply {
				if ok, relinkErr := server.upsertClipAnnotationRelation(ctx, best.id, nd.id, nd.excerpt); relinkErr != nil || !ok {
					t.Errorf("relink %s: ok=%v err=%v", nd.id, ok, relinkErr)
					continue
				}
				// Fix graph edges: drop the old target link, add the new one.
				if nd.curTarget != "" && nd.curTarget != best.id {
					if _, unlinkErr := service.UnlinkDocuments(ctx, nd.curTarget, nd.id); unlinkErr == nil {
						edgesCleaned++
					}
				}
				if _, linkErr := service.LinkDocuments(ctx, best.id, nd.id); linkErr != nil {
					t.Errorf("link %s -> %s: %v", best.id, nd.id, linkErr)
				}
			}
			relinked++
		} else if nd.curTarget == "" && best.id != "" {
			t.Logf("RELINK %s (orphan) -> %s", filepath.Base(nd.absPath), best.id[:8])
			if apply {
				if ok, relinkErr := server.upsertClipAnnotationRelation(ctx, best.id, nd.id, nd.excerpt); relinkErr != nil || !ok {
					t.Errorf("relink %s: ok=%v err=%v", nd.id, ok, relinkErr)
					continue
				}
				if _, linkErr := service.LinkDocuments(ctx, best.id, nd.id); linkErr != nil {
					t.Errorf("link %s -> %s: %v", best.id, nd.id, linkErr)
				}
			}
			relinked++
		}
	}

	t.Logf("summary: recovered=%d relinked=%d edges_cleaned=%d skipped=%d", recovered, relinked, edgesCleaned, skipped)
}

func isNoteFile(base string) bool {
	return strings.HasSuffix(base, "-note.md") || (strings.Contains(base, "-note-") && strings.HasSuffix(base, ".md"))
}

func shortTarget(id string) string {
	if id == "" {
		return "(none)"
	}
	return id[:8]
}

// ftsBodyFor reads a document's indexed body from the FTS virtual table via a
// separate sqlite handle (the file may be missing, so ReadDocument would fail).
func ftsBodyFor(ctx context.Context, dbPath, id string) (string, bool) {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return "", false
	}
	defer db.Close()
	var body string
	if err := db.QueryRowContext(ctx, `SELECT body FROM document_fts WHERE document_id=?`, id).Scan(&body); err != nil {
		return "", false
	}
	return body, true
}
