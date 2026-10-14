package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"membox/internal/remotedocs"
)

// documentItem is the stable API shape the React home consumes.
type documentItem struct {
	ID        string `json:"id"`
	Revision  int    `json:"revision"`
	Title     string `json:"title"`
	SourceURL string `json:"source_url,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

func toItem(doc remotedocs.Document) documentItem {
	return documentItem{
		ID:        doc.ID,
		Revision:  doc.CurrentRevision,
		Title:     doc.Title,
		SourceURL: doc.SourceURL,
		UpdatedAt: doc.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ---- create ---------------------------------------------------------------

func (s *Server) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	user := s.userFrom(r) // requireAuth already ran
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+4096)
	if err := r.ParseMultipartForm(s.cfg.MaxUploadBytes); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart field \"file\" required")
		return
	}
	defer file.Close()

	title := strings.TrimSpace(r.FormValue("title"))
	filename := header.Filename
	if filename == "" {
		filename = title
	}
	doc, err := s.docs.Create(r.Context(), user.ID, filename, r.FormValue("source_url"), file)
	if err != nil {
		s.writeDocError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toItem(doc))
}

// ---- list / read ----------------------------------------------------------

// handleListDocuments replaces the M4 stub with the real query. Shape kept
// stable since T02: {documents, next_page_token}.
func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	user := s.userFrom(r)
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	result, err := s.docs.List(r.Context(), remotedocs.ListQuery{
		OwnerID: user.ID,
		Query:   query.Get("q"),
		Limit:   limit,
		Cursor:  query.Get("page_token"),
	})
	if err != nil {
		s.writeDocError(w, err)
		return
	}
	items := make([]documentItem, 0, len(result.Documents))
	for _, doc := range result.Documents {
		items = append(items, toItem(doc))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"documents":       items,
		"next_page_token": result.NextCursor,
	})
}

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	doc, err := s.docs.Get(r.Context(), s.userFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.writeDocError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toItem(doc))
}

func (s *Server) handleDocumentContent(w http.ResponseWriter, r *http.Request) {
	rev, _ := strconv.Atoi(r.URL.Query().Get("rev"))
	version, body, err := s.docs.OpenContent(r.Context(), s.userFrom(r).ID, r.PathValue("id"), rev)
	if err != nil {
		s.writeDocError(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", version.MimeType)
	w.Header().Set("Content-Length", strconv.FormatInt(version.SizeBytes, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", version.Title))
	w.Header().Set("ETag", `"`+version.SHA256+`"`)
	_, _ = io.Copy(w, body)
}

// ---- mutation -------------------------------------------------------------

type patchDocumentBody struct {
	Title     string `json:"title"`
	SourceURL string `json:"source_url"`
}

func (s *Server) handlePatchDocument(w http.ResponseWriter, r *http.Request) {
	expected, ok := s.ifMatchRevision(w, r)
	if !ok {
		return
	}
	var body patchDocumentBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	doc, err := s.docs.UpdateMeta(r.Context(), s.userFrom(r).ID, r.PathValue("id"), expected, body.Title, body.SourceURL)
	if err != nil {
		s.writeDocError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toItem(doc))
}

func (s *Server) handleReplaceContent(w http.ResponseWriter, r *http.Request) {
	expected, ok := s.ifMatchRevision(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+4096)
	filename := r.URL.Query().Get("filename")
	var content io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(s.cfg.MaxUploadBytes); err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "multipart field \"file\" required")
			return
		}
		defer file.Close()
		content = file
		if filename == "" {
			filename = header.Filename
		}
	}
	doc, err := s.docs.ReplaceContent(r.Context(), s.userFrom(r).ID, r.PathValue("id"), expected, filename, content)
	if err != nil {
		s.writeDocError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toItem(doc))
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	expected, ok := s.ifMatchRevision(w, r)
	if !ok {
		return
	}
	if err := s.docs.SoftDelete(r.Context(), s.userFrom(r).ID, r.PathValue("id"), expected); err != nil {
		s.writeDocError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- helpers ---------------------------------------------------------------

// ifMatchRevision parses If-Match: "rev-N" (plan T17). Returns ok=false after
// writing the error response.
func (s *Server) ifMatchRevision(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	rev, err := strconv.Atoi(strings.TrimPrefix(raw, "rev-"))
	if raw == "" || err != nil || rev < 1 {
		writeError(w, http.StatusPreconditionRequired, `If-Match: "rev-N" header required`)
		return 0, false
	}
	return rev, true
}

func (s *Server) writeDocError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, remotedocs.ErrNotFound):
		// Foreign documents are 404 as well — never leak existence (T18).
		writeError(w, http.StatusNotFound, "document not found")
	case errors.Is(err, remotedocs.ErrRevisionConflict):
		writeError(w, http.StatusPreconditionFailed, "revision conflict")
	case errors.Is(err, remotedocs.ErrUnsupportedType):
		writeError(w, http.StatusUnsupportedMediaType, "only PDF and Markdown are supported")
	case strings.Contains(err.Error(), "upload exceeds"):
		writeError(w, http.StatusRequestEntityTooLarge, "upload too large")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
