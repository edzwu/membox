package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

// notTrashedClause hides soft-deleted documents from listings while keeping
// their rows intact for restore.
const notTrashedClause = `d.id NOT IN (SELECT document_id FROM document_trash)`

// documentSelect is the shared read projection for documents. The reported
// source_updated_at is the document's own source time raised to the latest
// annotation-note activity targeting it: taking a note counts as working on
// the annotated document, so newest-sort and date filters see it as modified.
// Both annotation_notes.updated_at (app saves) and the note file's own source
// time (external edits picked up by scans) participate.
const documentSelect = `SELECT d.id,d.created_at,d.updated_at,d.pinned,l.path_id,l.relative_path,l.file_key,l.status,
COALESCE(i.title,''),COALESCE(i.summary,''),COALESCE(i.media_type,'text/markdown'),COALESCE(i.metadata_overrides,0),COALESCE(i.authors,''),
COALESCE(i.publication_year,0),COALESCE(i.keywords,''),COALESCE(i.page_count,0),
COALESCE(i.mtime,0),COALESCE(i.size,0),COALESCE(i.sha256,''),i.indexed_at,
COALESCE(i.source_created_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.created_at END),
MAX(COALESCE(i.source_updated_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.updated_at END),
    COALESCE((SELECT MAX(MAX(an.updated_at,COALESCE(ni.source_updated_at,0)))
              FROM annotation_notes an
              LEFT JOIN document_index ni ON ni.document_id=an.note_document_id
              WHERE an.target_document_id=d.id
                AND an.note_document_id NOT IN (SELECT document_id FROM document_trash)),0)),p.root_path
FROM documents d JOIN document_locations l ON l.document_id=d.id
JOIN paths p ON p.id=l.path_id LEFT JOIN document_index i ON i.document_id=d.id`

func scanDocument(scanner interface{ Scan(...any) error }) (*catalog.Document, string, error) {
	var id, relative, fileKey, status, title, summary, mediaType, authors, keywords, hash, root string
	var created, updated, pinned, pathID, metadataOverrides, year, pageCount, mtime, size, sourceCreated, sourceUpdated int64
	var indexed sql.NullInt64
	if err := scanner.Scan(&id, &created, &updated, &pinned, &pathID, &relative, &fileKey, &status, &title, &summary,
		&mediaType, &metadataOverrides, &authors, &year, &keywords, &pageCount, &mtime, &size, &hash, &indexed, &sourceCreated, &sourceUpdated, &root); err != nil {
		return nil, "", err
	}
	location, err := catalog.NewLocation(catalog.IndexedPathID(pathID), relative)
	if err != nil {
		return nil, "", err
	}
	var indexedAt time.Time
	if indexed.Valid {
		indexedAt = fromMillis(indexed.Int64)
	}
	doc, err := catalog.RehydrateDocument(catalog.DocumentID(id), location, catalog.FileKey(fileKey), catalog.DocumentStatus(status), catalog.IndexState{
		Title: title, Summary: summary, MediaType: mediaType, MetadataOverrides: int(metadataOverrides),
		Authors: authors, Year: int(year), Keywords: keywords, PageCount: int(pageCount), MTime: mtime, Size: size, SHA256: hash, IndexedAt: indexedAt,
		SourceCreatedAt: fromMillis(sourceCreated), SourceUpdatedAt: fromMillis(sourceUpdated),
	}, fromMillis(created), fromMillis(updated), pinned != 0)
	if err != nil {
		return nil, "", fmt.Errorf("rehydrating document %q: %w", id, err)
	}
	return doc, filepath.Join(root, filepath.FromSlash(relative)), nil
}

func (s *Store) SaveScan(ctx context.Context, indexedPath *catalog.IndexedPath, saves []port.ScanSave) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := savePath(ctx, tx, indexedPath); err != nil {
		return err
	}
	for _, save := range saves {
		if err := saveDocument(ctx, tx, save); err != nil {
			return err
		}
		if err := saveContentVersion(ctx, tx, save); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing scan: %w", err)
	}
	return nil
}

func (s *Store) SaveDocument(ctx context.Context, save port.ScanSave) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveDocument(ctx, tx, save); err != nil {
		return err
	}
	if err := saveContentVersion(ctx, tx, save); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveSourceTimes(ctx context.Context, documents []*catalog.Document) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, document := range documents {
		if document == nil {
			return errors.New("cannot save source times for nil document")
		}
		result, err := tx.ExecContext(ctx, `UPDATE document_index SET source_created_at=?,source_updated_at=? WHERE document_id=?`,
			millis(document.Index.SourceCreatedAt), millis(document.Index.SourceUpdatedAt), document.ID)
		if err != nil {
			return fmt.Errorf("saving source times for document %s: %w", document.ID, err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("document %s has no index metadata", document.ID)
		}
	}
	return tx.Commit()
}

func (s *Store) SavePinned(ctx context.Context, documentID catalog.DocumentID, pinned bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE documents SET pinned=? WHERE id=?`, pinned, documentID)
	if err != nil {
		return fmt.Errorf("saving pin for document %s: %w", documentID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("document %s not found while saving pin", documentID)
	}
	return nil
}

// SetDocumentSummary stores a user- or agent-authored summary in the index
// metadata. Scans preserve the value (the scan upsert keeps the existing
// summary), so the write is stable across reindexes.
func (s *Store) SetDocumentSummary(ctx context.Context, documentID catalog.DocumentID, summary string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE document_index SET summary=? WHERE document_id=?`, summary, documentID)
	if err != nil {
		return fmt.Errorf("saving summary for document %s: %w", documentID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("document %s has no index metadata", documentID)
	}
	return nil
}

// SaveAnnotations retains legacy Miru sidecars during migration. New note
// content is stored in Markdown and annotation_notes; empty clears the blob.
func (s *Store) SaveAnnotations(ctx context.Context, documentID catalog.DocumentID, sidecar string) error {
	if strings.TrimSpace(sidecar) == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM document_annotations WHERE document_id=?`, documentID); err != nil {
			return fmt.Errorf("clearing annotations for document %s: %w", documentID, err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_annotations(document_id,sidecar,updated_at) VALUES(?,?,?)
ON CONFLICT(document_id) DO UPDATE SET sidecar=excluded.sidecar,updated_at=excluded.updated_at`,
		documentID, sidecar, time.Now().UTC().UnixMilli())
	if err != nil {
		return fmt.Errorf("saving annotations for document %s: %w", documentID, err)
	}
	return nil
}

// GetAnnotations returns a legacy annotation sidecar, or "" if none.
func (s *Store) GetAnnotations(ctx context.Context, documentID catalog.DocumentID) (string, error) {
	var sidecar string
	err := s.db.QueryRowContext(ctx, `SELECT sidecar FROM document_annotations WHERE document_id=?`, documentID).Scan(&sidecar)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading annotations for document %s: %w", documentID, err)
	}
	return sidecar, nil
}

func (s *Store) UpsertAnnotationNote(ctx context.Context, record port.AnnotationNoteRecord) error {
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO annotation_notes(
note_document_id,target_document_id,anchor_start,anchor_prefix,anchor_suffix,
highlight,underline,strikethrough,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(note_document_id) DO UPDATE SET
 target_document_id=excluded.target_document_id,
 anchor_start=excluded.anchor_start,
 anchor_prefix=excluded.anchor_prefix,
 anchor_suffix=excluded.anchor_suffix,
 highlight=excluded.highlight,
 underline=excluded.underline,
 strikethrough=excluded.strikethrough,
 updated_at=excluded.updated_at`,
		record.NoteDocumentID, record.TargetDocumentID, record.Start, record.Prefix, record.Suffix,
		record.Highlight, record.Underline, record.Strikethrough, millis(createdAt), millis(updatedAt))
	if err != nil {
		return fmt.Errorf("saving annotation note %s: %w", record.NoteDocumentID, err)
	}
	return nil
}

func (s *Store) ListAnnotationNotes(ctx context.Context, targetDocumentID catalog.DocumentID) ([]port.AnnotationNoteRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.note_document_id,a.target_document_id,a.anchor_start,a.anchor_prefix,a.anchor_suffix,
a.highlight,a.underline,a.strikethrough,a.created_at,a.updated_at
FROM annotation_notes a
JOIN document_locations l ON l.document_id=a.note_document_id
WHERE a.target_document_id=? AND l.status='active'
  AND a.note_document_id NOT IN (SELECT document_id FROM document_trash)
ORDER BY a.anchor_start,a.created_at,a.note_document_id`, targetDocumentID)
	if err != nil {
		return nil, fmt.Errorf("listing annotation notes for %s: %w", targetDocumentID, err)
	}
	defer rows.Close()
	var records []port.AnnotationNoteRecord
	for rows.Next() {
		var record port.AnnotationNoteRecord
		var highlight, underline, strikethrough int
		var createdAt, updatedAt int64
		if err := rows.Scan(&record.NoteDocumentID, &record.TargetDocumentID, &record.Start, &record.Prefix, &record.Suffix,
			&highlight, &underline, &strikethrough, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		record.Highlight = highlight != 0
		record.Underline = underline != 0
		record.Strikethrough = strikethrough != 0
		record.CreatedAt = time.UnixMilli(createdAt).UTC()
		record.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) GetAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) (port.AnnotationNoteRecord, bool, error) {
	var record port.AnnotationNoteRecord
	var highlight, underline, strikethrough int
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `SELECT note_document_id,target_document_id,anchor_start,anchor_prefix,anchor_suffix,
highlight,underline,strikethrough,created_at,updated_at FROM annotation_notes WHERE note_document_id=?`, noteDocumentID).
		Scan(&record.NoteDocumentID, &record.TargetDocumentID, &record.Start, &record.Prefix, &record.Suffix,
			&highlight, &underline, &strikethrough, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return port.AnnotationNoteRecord{}, false, nil
	}
	if err != nil {
		return port.AnnotationNoteRecord{}, false, fmt.Errorf("reading annotation note %s: %w", noteDocumentID, err)
	}
	record.Highlight = highlight != 0
	record.Underline = underline != 0
	record.Strikethrough = strikethrough != 0
	record.CreatedAt = time.UnixMilli(createdAt).UTC()
	record.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	return record, true, nil
}

func (s *Store) DeleteAnnotationNote(ctx context.Context, noteDocumentID catalog.DocumentID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM annotation_notes WHERE note_document_id=?`, noteDocumentID); err != nil {
		return fmt.Errorf("deleting annotation note %s: %w", noteDocumentID, err)
	}
	return nil
}

func (s *Store) SaveDocumentReadState(ctx context.Context, state port.DocumentReadState) error {
	if state.ProgressY <= 0 && strings.TrimSpace(state.ProgressAt) == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM document_read_state WHERE document_id=?`, state.DocumentID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_read_state(document_id,progress_y,progress_at) VALUES(?,?,?)
ON CONFLICT(document_id) DO UPDATE SET progress_y=excluded.progress_y,progress_at=excluded.progress_at`,
		state.DocumentID, state.ProgressY, state.ProgressAt)
	if err != nil {
		return fmt.Errorf("saving read state for document %s: %w", state.DocumentID, err)
	}
	return nil
}

func (s *Store) GetDocumentReadState(ctx context.Context, documentID catalog.DocumentID) (port.DocumentReadState, bool, error) {
	state := port.DocumentReadState{DocumentID: documentID}
	var finishedMillis int64
	err := s.db.QueryRowContext(ctx, `SELECT progress_y,progress_at,read_status,COALESCE(finished_at,0) FROM document_read_state WHERE document_id=?`, documentID).
		Scan(&state.ProgressY, &state.ProgressAt, &state.ReadStatus, &finishedMillis)
	if errors.Is(err, sql.ErrNoRows) {
		return port.DocumentReadState{}, false, nil
	}
	if err != nil {
		return port.DocumentReadState{}, false, fmt.Errorf("reading read state for document %s: %w", documentID, err)
	}
	if state.ReadStatus == "" {
		state.ReadStatus = "unread"
	}
	if finishedMillis != 0 {
		state.FinishedAt = fromMillis(finishedMillis)
	}
	return state, true, nil
}

// SetDocumentReadStatus records the semantic reading state. A reading/finished
// document keeps its scroll progress; finished also stamps FinishedAt.
func (s *Store) SetDocumentReadStatus(ctx context.Context, documentID catalog.DocumentID, status string, finishedAt time.Time) error {
	if status == "" {
		status = "unread"
	}
	var finishedMillis *int64
	if status == "finished" && !finishedAt.IsZero() {
		v := millis(finishedAt)
		finishedMillis = &v
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO document_read_state(document_id,progress_y,progress_at,read_status,finished_at)
VALUES(?,0,'',?,?)
ON CONFLICT(document_id) DO UPDATE SET
  read_status=excluded.read_status,
  finished_at=CASE WHEN excluded.finished_at IS NOT NULL THEN excluded.finished_at ELSE finished_at END`,
		documentID, status, finishedMillis)
	if err != nil {
		return fmt.Errorf("setting read status for document %s: %w", documentID, err)
	}
	return nil
}

func (s *Store) ListRecentDocuments(ctx context.Context, limit int) ([]port.RecentDocument, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,COALESCE(i.title,''),l.relative_path,r.progress_at
FROM document_read_state r
JOIN documents d ON d.id=r.document_id
JOIN document_locations l ON l.document_id=d.id
LEFT JOIN document_index i ON i.document_id=d.id
WHERE l.status='active' AND `+notTrashedClause+` AND trim(r.progress_at)!=''
ORDER BY r.progress_at DESC,d.id
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recent documents: %w", err)
	}
	defer rows.Close()
	var documents []port.RecentDocument
	for rows.Next() {
		var document port.RecentDocument
		if err := rows.Scan(&document.DocumentID, &document.Title, &document.Path, &document.OpenedAt); err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (s *Store) ListRecentlyModifiedDocuments(ctx context.Context, limit int) ([]port.ModifiedDocument, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,COALESCE(i.title,''),l.relative_path,
MAX(COALESCE(i.source_updated_at,CASE WHEN i.mtime>0 THEN i.mtime/1000000 ELSE d.updated_at END),
    COALESCE((SELECT MAX(MAX(an.updated_at,COALESCE(ni.source_updated_at,0)))
              FROM annotation_notes an
              LEFT JOIN document_index ni ON ni.document_id=an.note_document_id
              WHERE an.target_document_id=d.id
                AND an.note_document_id NOT IN (SELECT document_id FROM document_trash)),0)) AS modified_at
FROM documents d
JOIN document_locations l ON l.document_id=d.id
LEFT JOIN document_index i ON i.document_id=d.id
WHERE l.status='active' AND `+notTrashedClause+`
ORDER BY modified_at DESC,d.id
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recently modified documents: %w", err)
	}
	defer rows.Close()
	var documents []port.ModifiedDocument
	for rows.Next() {
		var document port.ModifiedDocument
		var modifiedAt int64
		if err := rows.Scan(&document.DocumentID, &document.Title, &document.Path, &modifiedAt); err != nil {
			return nil, err
		}
		document.ModifiedAt = fromMillis(modifiedAt)
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func savePath(ctx context.Context, tx *sql.Tx, path *catalog.IndexedPath) error {
	var last any
	if path.LastScanAt != nil {
		last = millis(*path.LastScanAt)
	}
	_, err := tx.ExecContext(ctx, `UPDATE paths SET status=?,last_scan_at=?,last_error=? WHERE id=?`, path.Status, last, path.LastError, path.ID)
	return err
}

func saveDocument(ctx context.Context, tx *sql.Tx, save port.ScanSave) error {
	d := save.Document
	if d == nil {
		return errors.New("cannot save nil document")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents(id,created_at,updated_at,pinned) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at,pinned=excluded.pinned`, d.ID, millis(d.CreatedAt), millis(d.UpdatedAt), d.Pinned); err != nil {
		return fmt.Errorf("saving document: %w", err)
	}
	lastSeen := any(nil)
	if d.Status == catalog.DocumentActive {
		lastSeen = millis(d.UpdatedAt)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_locations(document_id,path_id,relative_path,file_key,status,last_seen_at)
VALUES(?,?,?,?,?,?) ON CONFLICT(document_id) DO UPDATE SET path_id=excluded.path_id,relative_path=excluded.relative_path,
file_key=excluded.file_key,status=excluded.status,last_seen_at=excluded.last_seen_at`,
		d.ID, d.Location.PathID, d.Location.RelativePath, d.FileKey, d.Status, lastSeen); err != nil {
		return fmt.Errorf("saving document location: %w", err)
	}
	if !save.Reindex {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_index(document_id,title,summary,media_type,metadata_overrides,authors,publication_year,keywords,page_count,mtime,size,sha256,indexed_at,source_created_at,source_updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(document_id) DO UPDATE SET title=excluded.title,summary=excluded.summary,media_type=excluded.media_type,metadata_overrides=excluded.metadata_overrides,
authors=excluded.authors,publication_year=excluded.publication_year,keywords=excluded.keywords,page_count=excluded.page_count,mtime=excluded.mtime,size=excluded.size,
sha256=excluded.sha256,indexed_at=excluded.indexed_at,source_created_at=excluded.source_created_at,source_updated_at=excluded.source_updated_at`,
		d.ID, d.Index.Title, d.Index.Summary, d.Index.MediaType, d.Index.MetadataOverrides, d.Index.Authors, d.Index.Year, d.Index.Keywords, d.Index.PageCount,
		d.Index.MTime, d.Index.Size, d.Index.SHA256, millis(d.Index.IndexedAt), millis(d.Index.SourceCreatedAt), millis(d.Index.SourceUpdatedAt)); err != nil {
		return fmt.Errorf("saving document index: %w", err)
	}
	var root string
	if err := tx.QueryRowContext(ctx, `SELECT root_path FROM paths WHERE id=?`, d.Location.PathID).Scan(&root); err != nil {
		return err
	}
	fullPath := filepath.Join(root, filepath.FromSlash(d.Location.RelativePath))
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_fts WHERE document_id=?`, d.ID); err != nil {
		return fmt.Errorf("removing old search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_fts(document_id,title,path,body) VALUES(?,?,?,?)`, d.ID, d.Index.Title, fullPath, searchableBody(d.Index, save)); err != nil {
		return fmt.Errorf("updating search index: %w", err)
	}
	// Persist clip provenance when front matter carries source_url (ingest + scan backfill).
	if sourceURL, clipMode := sourceMetaFromBody(string(save.Body), d.Location.RelativePath); sourceURL != "" {
		norm := normalizeSourceURLLite(sourceURL)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO document_sources(document_id, source_url, source_url_norm, clip_mode, created_at)
VALUES(?,?,?,?,?)
ON CONFLICT(document_id) DO UPDATE SET
  source_url=excluded.source_url,
  source_url_norm=excluded.source_url_norm,
  clip_mode=excluded.clip_mode
`, d.ID, norm, norm, clipMode, millis(d.UpdatedAt)); err != nil {
			return fmt.Errorf("saving document source: %w", err)
		}
	}
	return nil
}

func searchableBody(index catalog.IndexState, save port.ScanSave) string {
	text := save.SearchText
	if text == nil && index.MediaType == "text/markdown" {
		text = save.Body
	}
	metadata := []string{index.Authors, index.Keywords}
	if index.Year > 0 {
		metadata = append(metadata, fmt.Sprintf("%d", index.Year))
	}
	if index.PageCount > 0 {
		metadata = append(metadata, fmt.Sprintf("%d pages", index.PageCount))
	}
	metadata = append(metadata, string(text))
	return strings.Join(metadata, "\n")
}

func saveContentVersion(ctx context.Context, tx *sql.Tx, save port.ScanSave) error {
	if save.Content == nil {
		return nil
	}
	if save.Document == nil {
		return errors.New("cannot version nil document")
	}
	content := save.Content
	if content.SHA256 == "" || content.ObjectHash == "" || content.Size < 0 {
		return fmt.Errorf("invalid content object for document %s", save.Document.ID)
	}
	if content.SHA256 != save.Document.Index.SHA256 || content.Size != save.Document.Index.Size {
		return fmt.Errorf("content object does not match document %s index", save.Document.ID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO contents(sha256,size,representation,object_hash,created_at)
VALUES(?,?,'direct',?,?) ON CONFLICT(sha256) DO NOTHING`,
		content.SHA256, content.Size, content.ObjectHash, millis(save.Document.UpdatedAt)); err != nil {
		return fmt.Errorf("saving content %s: %w", content.SHA256, err)
	}
	var storedSize int64
	var representation, objectHash string
	if err := tx.QueryRowContext(ctx, `SELECT size,representation,object_hash FROM contents WHERE sha256=?`, content.SHA256).
		Scan(&storedSize, &representation, &objectHash); err != nil {
		return fmt.Errorf("checking content %s: %w", content.SHA256, err)
	}
	if storedSize != content.Size || representation != "direct" || objectHash != content.ObjectHash {
		return fmt.Errorf("content %s conflicts with its stored representation", content.SHA256)
	}

	var parentID int64
	var currentHash string
	err := tx.QueryRowContext(ctx, `SELECT h.version_id,v.content_sha256
FROM document_heads h JOIN document_versions v ON v.id=h.version_id
WHERE h.document_id=?`, save.Document.ID).Scan(&parentID, &currentHash)
	if err == nil && currentHash == content.SHA256 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("loading document %s head: %w", save.Document.ID, err)
	}

	reason := "external_edit"
	var parent any
	if errors.Is(err, sql.ErrNoRows) {
		reason = "initial_import"
		parent = nil
	} else {
		parent = parentID
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO document_versions(document_id,parent_version_id,content_sha256,reason,created_at)
VALUES(?,?,?,?,?)`, save.Document.ID, parent, content.SHA256, reason, millis(save.Document.UpdatedAt))
	if err != nil {
		return fmt.Errorf("creating version for document %s: %w", save.Document.ID, err)
	}
	versionID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("reading new version id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO document_heads(document_id,version_id) VALUES(?,?)
ON CONFLICT(document_id) DO UPDATE SET version_id=excluded.version_id`, save.Document.ID, versionID); err != nil {
		return fmt.Errorf("advancing document %s head: %w", save.Document.ID, err)
	}
	return nil
}

func sourceMetaFromBody(body, relativePath string) (sourceURL, clipMode string) {
	if fm, ok := frontMatterMap(body); ok {
		sourceURL = strings.TrimSpace(fm["source_url"])
		clipMode = strings.TrimSpace(fm["clip_mode"])
	}
	if clipMode == "" && strings.HasSuffix(strings.ToLower(relativePath), "-note.md") {
		clipMode = "selection"
	}
	if clipMode == "" && sourceURL != "" {
		clipMode = "page"
	}
	return sourceURL, clipMode
}

func frontMatterMap(body string) (map[string]string, bool) {
	trim := strings.TrimSpace(body)
	if !strings.HasPrefix(trim, "---") {
		return nil, false
	}
	rest := trim[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, false
	}
	out := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(parts[0])
		val := ""
		if len(parts) == 2 {
			val = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		}
		out[key] = val
	}
	return out, true
}

// normalizeSourceURLLite keeps store independent of the HTTP package while
// matching browser/weixin share-link variants to the same key.
func normalizeSourceURLLite(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Strip fragment.
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	// WeChat article id is stable; drop query entirely for mp.weixin.qq.com/s/…
	if strings.Contains(raw, "mp.weixin.qq.com/s/") || strings.Contains(raw, "weixin.qq.com/s/") {
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			raw = raw[:i]
		}
		return strings.TrimRight(raw, "/")
	}
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		// Keep non-weixin queries only if essential; default strip for matching stability.
		raw = raw[:i]
	}
	return strings.TrimRight(raw, "/")
}
