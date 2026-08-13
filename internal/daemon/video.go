package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"membox/internal/application"
)

type VideoSummaryRequest struct {
	URL        string `json:"url"`
	CourseCode string `json:"course_code"`
	LectureNo  int    `json:"lecture_no,omitempty"`
	Force      bool   `json:"force,omitempty"`
}

type VideoSummaryResult struct {
	DocumentID      string `json:"document_id"`
	Path            string `json:"path"`
	Filename        string `json:"filename"`
	Created         bool   `json:"created"`
	CourseCode      string `json:"course_code"`
	CourseID        string `json:"course_id,omitempty"`
	CourseTitle     string `json:"course_title,omitempty"`
	LectureNo       int    `json:"lecture_no"`
	LectureNoSource string `json:"lecture_no_source,omitempty"`
	PlaylistIndex   int    `json:"playlist_index,omitempty"`
	VideoID         string `json:"video_id"`
	LectureTitle    string `json:"lecture_title"`
	SourceURL       string `json:"source_url"`
}

type videoSummaryResponse struct {
	Result VideoSummaryResult `json:"result"`
	Error  string             `json:"error,omitempty"`
}

type echoSummaryArtifact struct {
	CourseCode      string `json:"course_code"`
	CourseID        string `json:"course_id"`
	CourseTitle     string `json:"course_title"`
	LectureNo       int    `json:"lecture_no"`
	LectureNoSource string `json:"lecture_no_source"`
	PlaylistIndex   int    `json:"playlist_index"`
	VideoID         string `json:"video_id"`
	LectureTitle    string `json:"lecture_title"`
	SourceURL       string `json:"source_url"`
	SummaryPath     string `json:"summary_path"`
	Summary         string `json:"summary"`
}

type echoSummaryRunner interface {
	Summarize(context.Context, VideoSummaryRequest) (echoSummaryArtifact, error)
}

type echoBPCommand struct{ binary string }

func findEchoBP() string {
	if configured := strings.TrimSpace(os.Getenv("MMD_EBP_BIN")); configured != "" {
		return configured
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "repo", "echo-bp", ".venv", "bin", "ebp")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return "ebp"
}

func (r echoBPCommand) Summarize(ctx context.Context, request VideoSummaryRequest) (echoSummaryArtifact, error) {
	binary := r.binary
	if strings.TrimSpace(binary) == "" {
		binary = findEchoBP()
	}
	args := []string{"automation", "summary", request.URL, "--course-code", request.CourseCode}
	if request.LectureNo > 0 {
		args = append(args, "--lecture-no", strconv.Itoa(request.LectureNo))
	}
	if request.Force {
		args = append(args, "--force")
	}
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return echoSummaryArtifact{}, ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return echoSummaryArtifact{}, fmt.Errorf("echo-bp summary: %s", detail)
	}
	var artifact echoSummaryArtifact
	if err := json.Unmarshal(stdout.Bytes(), &artifact); err != nil {
		return echoSummaryArtifact{}, fmt.Errorf("decode echo-bp summary output: %w", err)
	}
	if artifact.CourseCode == "" || artifact.VideoID == "" || artifact.LectureNo <= 0 || strings.TrimSpace(artifact.Summary) == "" {
		return echoSummaryArtifact{}, errors.New("echo-bp returned an incomplete summary artifact")
	}
	return artifact, nil
}

func publishVideoSummary(ctx context.Context, service *application.Service, artifact echoSummaryArtifact) (VideoSummaryResult, error) {
	filename := fmt.Sprintf("%s-lec%d.md", artifact.CourseCode, artifact.LectureNo)
	identity := map[string]string{
		"course_code": artifact.CourseCode,
		"course_id":   artifact.CourseID,
		"video_id":    artifact.VideoID,
	}

	// Find the prior projection by stable source identity. If playlist order
	// changed, rename the same membox document before updating its body.
	documents, err := service.ListDocuments(ctx, 1000, false, "")
	if err != nil {
		return VideoSummaryResult{}, err
	}
	var existingID string
	for _, record := range documents {
		body, readErr := service.ReadDocument(ctx, string(record.Document.ID))
		if readErr != nil {
			continue
		}
		meta := markdownFrontmatter(body)
		if meta["managed_by"] != "echo-bp" || meta["video_id"] != identity["video_id"] {
			continue
		}
		if identity["course_id"] != "" {
			if meta["course_id"] != identity["course_id"] {
				continue
			}
		} else if meta["course_code"] != identity["course_code"] {
			continue
		}
		if existingID != "" && existingID != string(record.Document.ID) {
			return VideoSummaryResult{}, fmt.Errorf("multiple membox projections match course/video identity; resolve duplicates before publishing")
		}
		existingID = string(record.Document.ID)
	}

	// Never overwrite the readable slot if it belongs to another video.
	if target, _, resolveErr := service.ResolveDocumentByRelativePath(ctx, filename); resolveErr == nil && string(target.ID) != existingID {
		body, readErr := service.ReadDocument(ctx, string(target.ID))
		if readErr != nil || markdownFrontmatter(body)["video_id"] != artifact.VideoID {
			return VideoSummaryResult{}, fmt.Errorf("%s already belongs to another document; choose a different course code or lecture number", filename)
		}
	}
	if existingID != "" {
		_, currentPath, resolveErr := service.ResolveDocument(ctx, existingID)
		if resolveErr != nil {
			return VideoSummaryResult{}, resolveErr
		}
		if filepath.Base(currentPath) != filename {
			if _, renameErr := service.RenameDocument(ctx, existingID, filename, artifact.LectureTitle); renameErr != nil {
				return VideoSummaryResult{}, renameErr
			}
		}
	}

	body := renderVideoSummaryMarkdown(artifact)
	upserted, err := service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{Filename: filename, Body: body})
	if err != nil {
		return VideoSummaryResult{}, err
	}
	if upserted.Document == nil {
		return VideoSummaryResult{}, errors.New("published summary has no document identity")
	}
	return VideoSummaryResult{
		DocumentID: string(upserted.Document.ID), Path: upserted.Path, Filename: filename, Created: upserted.Created,
		CourseCode: artifact.CourseCode, CourseID: artifact.CourseID, CourseTitle: artifact.CourseTitle,
		LectureNo: artifact.LectureNo, LectureNoSource: artifact.LectureNoSource, PlaylistIndex: artifact.PlaylistIndex,
		VideoID: artifact.VideoID, LectureTitle: artifact.LectureTitle, SourceURL: artifact.SourceURL,
	}, nil
}

func renderVideoSummaryMarkdown(artifact echoSummaryArtifact) string {
	summary := stripMarkdownFrontmatter(strings.TrimSpace(artifact.Summary))
	return fmt.Sprintf("---\nkind: youtube-lecture-summary\nmanaged_by: echo-bp\ncourse_code: %s\ncourse_id: %s\ncourse_title: %s\nlecture_no: %d\nlecture_no_source: %s\nplaylist_index: %d\nvideo_id: %s\ntitle: %s\nsource_url: %s\n---\n\n%s\n",
		yamlScalar(artifact.CourseCode), yamlScalar(artifact.CourseID), yamlScalar(artifact.CourseTitle), artifact.LectureNo,
		yamlScalar(artifact.LectureNoSource), artifact.PlaylistIndex, yamlScalar(artifact.VideoID), yamlScalar(artifact.LectureTitle),
		yamlScalar(artifact.SourceURL), summary)
}

func yamlScalar(value string) string { return strconv.Quote(strings.TrimSpace(value)) }

func stripMarkdownFrontmatter(body string) string {
	if !strings.HasPrefix(body, "---\n") {
		return body
	}
	if end := strings.Index(body[4:], "\n---\n"); end >= 0 {
		return strings.TrimSpace(body[4+end+5:])
	}
	return body
}

func markdownFrontmatter(body []byte) map[string]string {
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
