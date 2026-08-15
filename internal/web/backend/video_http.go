package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"membox/internal/translation"
	"membox/internal/videosummary"
)

// browserVideoCourseCode is the catch-all course for browser-originated
// YouTube summaries when the caller does not supply a course_code. Identity
// remains the video_id frontmatter field.
const browserVideoCourseCode = videosummary.DefaultBrowserCourseCode

// defaultBrowserSummaryModel matches membox assist / deepseek flash.
const defaultBrowserSummaryModel = "deepseek/deepseek-v4-flash"

type videoSummaryHTTPRequest struct {
	URL        string `json:"url"`
	CourseCode string `json:"course_code,omitempty"`
	LectureNo  int    `json:"lecture_no,omitempty"`
	Force      bool   `json:"force,omitempty"`
	// LookupOnly returns an existing summary or 404 without calling mmd/echo-bp.
	LookupOnly    bool   `json:"lookup_only,omitempty"`
	Title         string `json:"title,omitempty"`
	VideoID       string `json:"video_id,omitempty"`
	Lang          string `json:"lang,omitempty"`
	Source        string `json:"source,omitempty"`
	PlaylistID    string `json:"playlist_id,omitempty"`
	PlaylistTitle string `json:"playlist_title,omitempty"`
	PlaylistIndex int    `json:"playlist_index,omitempty"`
	Model         string `json:"model,omitempty"`
	// TranscriptMarkdown is caption Markdown from the browser. Companion
	// persists it as yt-<id>-transcript.md then tells mmd the file path —
	// LLM input is system prompt + this markdown, not a JSON segment blast.
	TranscriptMarkdown string `json:"transcript_markdown,omitempty"`
}

type videoSummaryHTTPResult struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Filename        string `json:"filename"`
	Created         bool   `json:"created"`
	Updated         bool   `json:"updated,omitempty"`
	Reused          bool   `json:"reused,omitempty"`
	ViewURL         string `json:"view_url"`
	CourseCode      string `json:"course_code,omitempty"`
	CourseID        string `json:"course_id,omitempty"`
	CourseTitle     string `json:"course_title,omitempty"`
	LectureNo       int    `json:"lecture_no,omitempty"`
	LectureNoSource string `json:"lecture_no_source,omitempty"`
	PlaylistIndex   int    `json:"playlist_index,omitempty"`
	VideoID         string `json:"video_id"`
	Title           string `json:"title,omitempty"`
	SourceURL       string `json:"source_url,omitempty"`
	TranscriptPath  string `json:"transcript_path,omitempty"`
	TranscriptID    string `json:"transcript_id,omitempty"`
}

type mmdVideoSummaryRequest struct {
	URL            string `json:"url"`
	CourseCode     string `json:"course_code"`
	LectureNo      int    `json:"lecture_no,omitempty"`
	Force          bool   `json:"force,omitempty"`
	Title          string `json:"title,omitempty"`
	VideoID        string `json:"video_id,omitempty"`
	Lang           string `json:"lang,omitempty"`
	Source         string `json:"source,omitempty"`
	PlaylistID     string `json:"playlist_id,omitempty"`
	PlaylistTitle  string `json:"playlist_title,omitempty"`
	PlaylistIndex  int    `json:"playlist_index,omitempty"`
	Model          string `json:"model,omitempty"`
	TranscriptPath string `json:"transcript_path,omitempty"`
}

type mmdVideoSummaryResponse struct {
	Result videosummary.Result `json:"result"`
	Error  string              `json:"error,omitempty"`
}

// handleVideoSummary is the browser-facing idempotent entry point:
//
//  1. Resolve the YouTube video_id from the URL.
//  2. If a managed echo-bp summary already exists in membox and force is false,
//     return it immediately with a Miru view_url (no echo-bp work).
//  3. Persist transcript_markdown as yt-<id>-transcript.md in membox.
//  4. Ask mmd → ebp to summarize from that file path (prompt + markdown).
//
// course_code defaults to "youtube" for ad-hoc browser clips.
func (s *Server) handleVideoSummary(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireBridgeToken(writer, request) {
		return
	}
	if strings.TrimSpace(s.home) == "" {
		http.Error(writer, "video summary unavailable: membox home is not configured", http.StatusServiceUnavailable)
		return
	}

	// Markdown captions are compact vs JSON segments; 4 MiB is plenty.
	const maxVideoSummaryBody = 4 << 20
	request.Body = http.MaxBytesReader(writer, request.Body, maxVideoSummaryBody)
	var payload videoSummaryHTTPRequest
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&payload); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "request body too large") {
			msg = fmt.Sprintf("request body too large (limit %d MiB)", maxVideoSummaryBody>>20)
		}
		http.Error(writer, "invalid video summary request: "+msg, http.StatusBadRequest)
		return
	}
	videoURL := strings.TrimSpace(payload.URL)
	if videoURL == "" && strings.TrimSpace(payload.VideoID) == "" {
		http.Error(writer, "url or video_id is required", http.StatusBadRequest)
		return
	}
	if payload.LectureNo < 0 {
		http.Error(writer, "lecture_no must be positive when provided", http.StatusBadRequest)
		return
	}

	videoID := strings.TrimSpace(payload.VideoID)
	if videoID == "" {
		var err error
		videoID, err = videosummary.YouTubeVideoID(videoURL)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if videoURL == "" {
		videoURL = "https://www.youtube.com/watch?v=" + videoID
	}

	ctx := request.Context()
	courseCode := strings.TrimSpace(strings.ToLower(payload.CourseCode))

	// Idempotent open / explicit lookup.
	if !payload.Force || payload.LookupOnly {
		existing, found, findErr := videosummary.FindByVideoID(ctx, s.service, videoID, courseCode)
		if findErr != nil {
			http.Error(writer, findErr.Error(), http.StatusInternalServerError)
			return
		}
		if found {
			writeVideoSummaryJSON(writer, videoHTTPResultFromCatalog(s, existing, false, "", ""))
			return
		}
		if payload.LookupOnly {
			http.Error(writer, "no existing video summary for this video_id", http.StatusNotFound)
			return
		}
	}

	lectureNo := payload.LectureNo
	if payload.Force {
		existing, found, findErr := videosummary.FindByVideoID(ctx, s.service, videoID, courseCode)
		if findErr != nil {
			http.Error(writer, findErr.Error(), http.StatusInternalServerError)
			return
		}
		if found {
			if courseCode == "" {
				courseCode = existing.CourseCode
			}
			if lectureNo <= 0 {
				lectureNo = existing.LectureNo
			}
		}
	}
	if courseCode == "" {
		courseCode = browserVideoCourseCode
	}
	if lectureNo <= 0 && courseCode == browserVideoCourseCode {
		next, nextErr := videosummary.NextLectureNo(ctx, s.service, courseCode)
		if nextErr != nil {
			http.Error(writer, nextErr.Error(), http.StatusInternalServerError)
			return
		}
		lectureNo = next
	}

	transcriptMarkdown := strings.TrimSpace(payload.TranscriptMarkdown)
	var transcriptPath, transcriptID string
	if transcriptMarkdown != "" {
		path, id, pubErr := videosummary.PublishTranscript(ctx, s.service, videosummary.TranscriptMeta{
			VideoID:  videoID,
			Title:    strings.TrimSpace(payload.Title),
			URL:      videoURL,
			Lang:     strings.TrimSpace(payload.Lang),
			Source:   firstNonEmpty(strings.TrimSpace(payload.Source), "browser"),
			Markdown: transcriptMarkdown,
		})
		if pubErr != nil {
			http.Error(writer, "save transcript: "+pubErr.Error(), http.StatusInternalServerError)
			return
		}
		transcriptPath, transcriptID = path, id
	} else if existingPath, existingID, ok := s.findExistingTranscript(ctx, videoID); ok {
		// Re-summarize from a caption file already in membox (e.g. doc 104e).
		transcriptPath, transcriptID = existingPath, existingID
		if strings.TrimSpace(payload.Title) == "" {
			if body, readErr := s.service.ReadDocument(ctx, existingID); readErr == nil {
				if t := markdownFrontmatterTitle(body); t != "" {
					payload.Title = t
				}
			}
		}
	} else if payload.Source == "browser" {
		http.Error(writer, "transcript_markdown is required (or save captions first)", http.StatusBadRequest)
		return
	}

	model := strings.TrimSpace(payload.Model)
	if model == "" && transcriptPath != "" {
		model = defaultBrowserSummaryModel
	}

	mmdReq := mmdVideoSummaryRequest{
		URL: videoURL, CourseCode: courseCode, LectureNo: lectureNo, Force: payload.Force,
		Title: strings.TrimSpace(payload.Title), VideoID: videoID,
		Lang: strings.TrimSpace(payload.Lang), Source: firstNonEmpty(strings.TrimSpace(payload.Source), "browser"),
		PlaylistID: strings.TrimSpace(payload.PlaylistID), PlaylistTitle: strings.TrimSpace(payload.PlaylistTitle),
		PlaylistIndex: payload.PlaylistIndex, Model: model, TranscriptPath: transcriptPath,
	}

	result, summarizeErr := s.runVideoSummaryViaMMD(ctx, mmdReq)
	if summarizeErr != nil {
		status := http.StatusUnprocessableEntity
		msg := summarizeErr.Error()
		if strings.Contains(msg, "mmd is not running") || strings.Contains(msg, "starting mmd") || strings.Contains(msg, "unreachable") {
			status = http.StatusServiceUnavailable
		}
		http.Error(writer, msg, status)
		return
	}
	out := videoHTTPResultFromCatalog(s, result, true, transcriptPath, transcriptID)
	writeVideoSummaryJSON(writer, out)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// findExistingTranscript resolves yt-<id>-transcript.md already in membox.
func (s *Server) findExistingTranscript(ctx context.Context, videoID string) (path, id string, ok bool) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" || s.service == nil {
		return "", "", false
	}
	filename := videosummary.TranscriptFilename(videoID)
	if doc, abs, err := s.service.ResolveDocumentByRelativePath(ctx, filename); err == nil && doc != nil {
		return abs, string(doc.ID), true
	}
	records, err := s.service.ListDocuments(ctx, 1000, false, "")
	if err != nil {
		return "", "", false
	}
	for _, rec := range records {
		docID := string(rec.Document.ID)
		body, readErr := s.service.ReadDocument(ctx, docID)
		if readErr != nil {
			continue
		}
		meta := parseSimpleFrontmatter(body)
		if meta["kind"] == "youtube-transcript" && meta["video_id"] == videoID {
			_, abs, resolveErr := s.service.ResolveDocument(ctx, docID)
			if resolveErr != nil {
				continue
			}
			return abs, docID, true
		}
	}
	return "", "", false
}

func markdownFrontmatterTitle(body []byte) string {
	return strings.TrimSpace(parseSimpleFrontmatter(body)["title"])
}

func parseSimpleFrontmatter(body []byte) map[string]string {
	out := map[string]string{}
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		out[strings.TrimSpace(key)] = value
	}
	return out
}

func (s *Server) runVideoSummaryViaMMD(ctx context.Context, request mmdVideoSummaryRequest) (videosummary.Result, error) {
	if err := translation.EnsureMMD(ctx, s.home); err != nil {
		return videosummary.Result{}, fmt.Errorf("starting mmd: %w", err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return videosummary.Result{}, err
	}
	socket := translation.SocketPath(s.home)
	httpClient := &http.Client{
		Timeout: 0, // nested pi summary can take several minutes
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://mmd/v1/video/summary", bytes.NewReader(body))
	if err != nil {
		return videosummary.Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return videosummary.Result{}, ctx.Err()
		}
		return videosummary.Result{}, fmt.Errorf("mmd is unreachable at %s: %w", socket, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	var decoded mmdVideoSummaryResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return videosummary.Result{}, fmt.Errorf("decode mmd video summary response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if decoded.Error != "" {
			return decoded.Result, fmt.Errorf("%s", decoded.Error)
		}
		return decoded.Result, fmt.Errorf("video summary returned HTTP %d", response.StatusCode)
	}
	return decoded.Result, nil
}

func videoHTTPResultFromCatalog(s *Server, result videosummary.Result, fromPipeline bool, transcriptPath, transcriptID string) videoSummaryHTTPResult {
	created := result.Created
	updated := false
	reused := result.Reused
	if fromPipeline {
		reused = false
		updated = !result.Created
	}
	title := strings.TrimSpace(result.LectureTitle)
	return videoSummaryHTTPResult{
		ID: result.DocumentID, Path: result.Path, Filename: result.Filename,
		Created: created, Updated: updated, Reused: reused,
		ViewURL:    s.ViewURL(result.DocumentID),
		CourseCode: result.CourseCode, CourseID: result.CourseID, CourseTitle: result.CourseTitle,
		LectureNo: result.LectureNo, LectureNoSource: result.LectureNoSource, PlaylistIndex: result.PlaylistIndex,
		VideoID: result.VideoID, Title: title, SourceURL: result.SourceURL,
		TranscriptPath: transcriptPath, TranscriptID: transcriptID,
	}
}

func writeVideoSummaryJSON(writer http.ResponseWriter, result videoSummaryHTTPResult) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(result)
}
