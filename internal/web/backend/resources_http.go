package backend

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"membox/internal/application"
	"membox/internal/application/port"
)

type resourceIngestRequest struct {
	Lines            []string `json:"lines"`
	SourceDocumentID string   `json:"source_document_id"`
	SourceFile       string   `json:"source_file"`
	SourceCommit     string   `json:"source_commit"`
}

type resourceAssessmentRequest struct {
	Assessments   []port.ResourceAssessment `json:"assessments"`
	Wave          int                       `json:"wave"`
	Source        string                    `json:"source"`
	ExpectedCount *int                      `json:"expected_count"`
}

// /api/resources is the resource read model. URL identity and classification
// live in membox; clients such as timension only keep stable resource IDs.
func (s *Server) handleResources(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 5000 {
			http.Error(writer, "limit must be between 1 and 5000", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	sourceDocumentID := strings.TrimSpace(request.URL.Query().Get("source_document_id"))
	sourceFile := strings.TrimSpace(request.URL.Query().Get("source_file"))
	var resources []port.ResourceRecord
	var err error
	if sourceDocumentID != "" || sourceFile != "" {
		resources, err = s.service.ListResourcesBySource(request.Context(), sourceDocumentID, sourceFile, limit)
	} else {
		resources, err = s.service.ListResources(request.Context(), limit)
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writeResourceJSON(writer, map[string]any{"resources": resources, "count": len(resources)})
}

func (s *Server) handleResourceIngest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	var payload resourceIngestRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	var result application.ResourceIngestResult
	var err error
	if payload.Lines == nil {
		result, err = s.service.IngestResourceDocument(
			request.Context(), payload.SourceDocumentID, payload.SourceFile, payload.SourceCommit,
		)
	} else {
		result, err = s.service.IngestResourceLines(request.Context(), application.ResourceIngestOptions{
			Lines: payload.Lines, SourceDocumentID: payload.SourceDocumentID,
			SourceFile: payload.SourceFile, SourceCommit: payload.SourceCommit,
		})
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	resources := append(append([]port.ResourceRecord{}, result.Inserted...), result.Existing...)
	writeResourceJSON(writer, map[string]any{
		"ok": true, "found": result.Found, "inserted": len(result.Inserted),
		"existing": len(result.Existing), "resources": resources,
	})
}

func (s *Server) handleResourceAssess(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	var payload resourceAssessmentRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	if payload.Assessments == nil {
		http.Error(writer, "assessments must be an array", http.StatusBadRequest)
		return
	}
	if payload.Wave > 0 {
		if payload.ExpectedCount == nil || len(payload.Assessments) != *payload.ExpectedCount {
			http.Error(writer, "full-scan wave must assess exactly expected_count resources", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(payload.Source) == "" {
			http.Error(writer, "source is required for a full-scan wave", http.StatusBadRequest)
			return
		}
	}
	resources, err := s.service.AssessResources(request.Context(), payload.Assessments)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if payload.Wave > 0 {
		if err := s.service.AdvanceResourceScan(request.Context(), payload.Source, payload.Wave); err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeResourceJSON(writer, map[string]any{"ok": true, "assessed": len(resources), "resources": resources})
}

func (s *Server) handleResourceScan(writer http.ResponseWriter, request *http.Request) {
	if !s.requireBridgeToken(writer, request) {
		return
	}
	source := strings.TrimSpace(request.URL.Query().Get("source"))
	if request.Method == http.MethodPost {
		var payload struct {
			Source string `json:"source"`
			Reset  bool   `json:"reset"`
		}
		if !decodeJSON(writer, request, &payload) {
			return
		}
		source = strings.TrimSpace(payload.Source)
		if !payload.Reset {
			http.Error(writer, "reset=true is required for POST", http.StatusBadRequest)
			return
		}
		if err := s.service.ResetResourceScan(request.Context(), source); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
	} else if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wave, err := s.service.ResourceScanState(request.Context(), source)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writeResourceJSON(writer, map[string]any{"source": source, "wave": wave})
}

func writeResourceJSON(writer http.ResponseWriter, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(payload)
}
