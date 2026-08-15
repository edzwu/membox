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
	"membox/internal/videosummary"
)

// Re-export catalog helpers so existing daemon callers keep a stable import path.
const DefaultBrowserCourseCode = videosummary.DefaultBrowserCourseCode

// DefaultBrowserSummaryModel is the pi model used when the browser extension
// supplies a pre-fetched transcript (skip yt-dlp). Matches membox assist.
const DefaultBrowserSummaryModel = "deepseek/deepseek-v4-flash"

type VideoSummaryRequest struct {
	URL        string `json:"url"`
	CourseCode string `json:"course_code"`
	LectureNo  int    `json:"lecture_no,omitempty"`
	Force      bool   `json:"force,omitempty"`
	// Browser / local-transcript path (skips yt-dlp when a markdown path is set).
	Title         string `json:"title,omitempty"`
	VideoID       string `json:"video_id,omitempty"`
	Lang          string `json:"lang,omitempty"`
	Source        string `json:"source,omitempty"`
	PlaylistID    string `json:"playlist_id,omitempty"`
	PlaylistTitle string `json:"playlist_title,omitempty"`
	PlaylistIndex int    `json:"playlist_index,omitempty"`
	Model         string `json:"model,omitempty"`
	// TranscriptPath is an absolute path to caption Markdown already stored
	// (typically membox yt-<id>-transcript.md). Preferred over inlining body.
	TranscriptPath string `json:"transcript_path,omitempty"`
}

type VideoSummaryResult = videosummary.Result

type videoSummaryResponse struct {
	Result VideoSummaryResult `json:"result"`
	Error  string             `json:"error,omitempty"`
}

type echoSummaryArtifact = videosummary.Artifact

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
	if path := strings.TrimSpace(request.TranscriptPath); path != "" {
		return r.summarizeLocalFile(ctx, binary, request, path)
	}
	return r.summarizeURL(ctx, binary, request)
}

func (r echoBPCommand) summarizeURL(ctx context.Context, binary string, request VideoSummaryRequest) (echoSummaryArtifact, error) {
	args := []string{"automation", "summary", request.URL, "--course-code", request.CourseCode}
	if request.LectureNo > 0 {
		args = append(args, "--lecture-no", strconv.Itoa(request.LectureNo))
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		args = append(args, "--model", model)
	}
	if request.Force {
		args = append(args, "--force")
	}
	return r.runEBP(ctx, binary, args, nil)
}

func (r echoBPCommand) summarizeLocalFile(ctx context.Context, binary string, request VideoSummaryRequest, transcriptPath string) (echoSummaryArtifact, error) {
	if _, err := os.Stat(transcriptPath); err != nil {
		return echoSummaryArtifact{}, fmt.Errorf("transcript file: %w", err)
	}
	videoID := strings.TrimSpace(request.VideoID)
	if videoID == "" {
		if id, err := videosummary.YouTubeVideoID(request.URL); err == nil {
			videoID = id
		}
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = DefaultBrowserSummaryModel
	}
	args := []string{
		"automation", "summary-local",
		"--course-code", request.CourseCode,
		"--model", model,
		"--transcript-file", transcriptPath,
	}
	if videoID != "" {
		args = append(args, "--video-id", videoID)
	}
	if title := strings.TrimSpace(request.Title); title != "" {
		args = append(args, "--title", title)
	}
	if url := strings.TrimSpace(request.URL); url != "" {
		args = append(args, "--url", url)
	}
	if lang := strings.TrimSpace(request.Lang); lang != "" {
		args = append(args, "--lang", lang)
	}
	if request.LectureNo > 0 {
		args = append(args, "--lecture-no", strconv.Itoa(request.LectureNo))
	}
	if request.Force {
		args = append(args, "--force")
	}
	return r.runEBP(ctx, binary, args, nil)
}

func (r echoBPCommand) runEBP(ctx context.Context, binary string, args []string, stdin []byte) (echoSummaryArtifact, error) {
	command := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if len(stdin) > 0 {
		command.Stdin = bytes.NewReader(stdin)
	}
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

func YouTubeVideoID(raw string) (string, error) {
	return videosummary.YouTubeVideoID(raw)
}

func FindVideoSummaryByVideoID(ctx context.Context, service *application.Service, videoID, courseCode string) (VideoSummaryResult, bool, error) {
	return videosummary.FindByVideoID(ctx, service, videoID, courseCode)
}

func NextCourseLectureNo(ctx context.Context, service *application.Service, courseCode string) (int, error) {
	return videosummary.NextLectureNo(ctx, service, courseCode)
}

func publishVideoSummary(ctx context.Context, service *application.Service, artifact echoSummaryArtifact) (VideoSummaryResult, error) {
	return videosummary.Publish(ctx, service, artifact)
}
