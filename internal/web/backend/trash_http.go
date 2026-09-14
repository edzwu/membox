package backend

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleDocumentTrash soft-deletes the document: its source file moves into
// the path root's trash directory and stays restorable via `mm trash
// restore`. The reader's file-action rail calls this from the delete button.
func (s *Server) handleDocumentTrash(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Same guard as translation: a cross-site form POST must not be able to
	// spend local actions. Miru is same-origin and always supplies it.
	if request.Header.Get(miruClientHeader) != "1" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	if s.service == nil {
		http.Error(writer, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}

	s.documentMu.Lock()
	document, trashedTo, err := s.service.TrashDocumentFile(request.Context(), selector)
	s.documentMu.Unlock()
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"ok":         true,
		"id":         string(document.ID),
		"trashed_to": trashedTo,
	})
}
