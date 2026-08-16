// Package videosummary owns YouTube lecture-summary identity in the membox
// catalog: URL parsing, lookup by video_id, lecture-number allocation, and
// publishing echo-bp artifacts to stable Markdown files.
//
// It deliberately does not talk to mmd or echo-bp so both the daemon and the
// Web Companion bridge can share the catalog rules without import cycles.
package videosummary

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"membox/internal/application"
)

// DefaultBrowserCourseCode is used when the browser clipper summarizes a
// YouTube page without an explicit course context. Filenames stay unique via
// allocated lecture numbers; identity is still the video_id frontmatter.
const DefaultBrowserCourseCode = "youtube"

// Result is one published (or reused) video-summary document.
type Result struct {
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
	// Reused is set when an existing projection was returned without rewriting.
	Reused bool `json:"reused,omitempty"`
}

// Artifact is the JSON shape produced by `ebp automation summary`.
type Artifact struct {
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

// YouTubeVideoID extracts the video id from common YouTube URL forms.
// Pure playlist URLs and non-YouTube hosts return an error.
func YouTubeVideoID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid url: %s", raw)
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	var id string
	switch {
	case host == "youtu.be":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			return "", errors.New("URL must identify one YouTube video")
		}
		id = parts[0]
	case host == "youtube.com" || host == "youtube-nocookie.com":
		switch {
		case strings.HasPrefix(u.Path, "/watch"):
			id = u.Query().Get("v")
		case strings.HasPrefix(u.Path, "/shorts/"), strings.HasPrefix(u.Path, "/embed/"), strings.HasPrefix(u.Path, "/live/"):
			id = path.Base(u.Path)
		default:
			return "", errors.New("URL must identify one YouTube video")
		}
	default:
		return "", fmt.Errorf("not a YouTube URL: %s", host)
	}
	id = strings.TrimSpace(id)
	// YouTube ids are typically 11 chars of [A-Za-z0-9_-]; accept any non-empty
	// token without path separators so tests and older ids still round-trip.
	if id == "" || strings.ContainsAny(id, "/?&#") {
		return "", errors.New("URL must identify one YouTube video")
	}
	return id, nil
}

// FindByVideoID returns the published echo-bp projection for a video, if any.
// When courseCode is non-empty, only that course matches; otherwise the first
// managed projection for the video_id is returned so the browser can open an
// existing lecture summary without knowing its course.
func FindByVideoID(ctx context.Context, service *application.Service, videoID, courseCode string) (Result, bool, error) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return Result{}, false, errors.New("video_id is required")
	}
	courseCode = strings.TrimSpace(strings.ToLower(courseCode))
	match, err := findProjection(ctx, service, videoID, "", courseCode)
	if err != nil {
		return Result{}, false, err
	}
	if match == nil {
		return Result{}, false, nil
	}
	_, absPath, resolveErr := service.ResolveDocument(ctx, match.id)
	if resolveErr != nil {
		return Result{}, false, resolveErr
	}
	lectureNo, _ := strconv.Atoi(match.meta["lecture_no"])
	playlistIndex, _ := strconv.Atoi(match.meta["playlist_index"])
	return Result{
		DocumentID: match.id, Path: absPath, Filename: filepath.Base(absPath),
		CourseCode: match.meta["course_code"], CourseID: match.meta["course_id"],
		CourseTitle: match.meta["course_title"], LectureNo: lectureNo,
		LectureNoSource: match.meta["lecture_no_source"], PlaylistIndex: playlistIndex,
		VideoID: match.meta["video_id"], LectureTitle: match.meta["title"],
		SourceURL: match.meta["source_url"], Reused: true,
	}, true, nil
}

// NextLectureNo returns max(existing lecture_no for course)+1, or 1.
// Used when the browser catch-all course needs a free readable slot.
func NextLectureNo(ctx context.Context, service *application.Service, courseCode string) (int, error) {
	courseCode = strings.TrimSpace(strings.ToLower(courseCode))
	if courseCode == "" {
		return 0, errors.New("course_code is required")
	}
	documents, err := service.ListDocuments(ctx, 1000, false, "")
	if err != nil {
		return 0, err
	}
	maxNo := 0
	for _, record := range documents {
		body, readErr := service.ReadDocument(ctx, string(record.Document.ID))
		if readErr != nil {
			continue
		}
		meta := markdownFrontmatter(body)
		if meta["managed_by"] != "echo-bp" || meta["course_code"] != courseCode {
			continue
		}
		n, _ := strconv.Atoi(meta["lecture_no"])
		if n > maxNo {
			maxNo = n
		}
	}
	return maxNo + 1, nil
}

type projection struct {
	id   string
	meta map[string]string
}

// findProjection scans managed echo-bp docs. courseID takes precedence over
// courseCode when set (playlist-stable identity). Empty course filters match
// any course for the video_id.
func findProjection(ctx context.Context, service *application.Service, videoID, courseID, courseCode string) (*projection, error) {
	documents, err := service.ListDocuments(ctx, 1000, false, "")
	if err != nil {
		return nil, err
	}
	var match *projection
	for _, record := range documents {
		id := string(record.Document.ID)
		body, readErr := service.ReadDocument(ctx, id)
		if readErr != nil {
			continue
		}
		meta := markdownFrontmatter(body)
		if meta["managed_by"] != "echo-bp" || meta["video_id"] != videoID {
			continue
		}
		if courseID != "" {
			if meta["course_id"] != courseID {
				continue
			}
		} else if courseCode != "" && meta["course_code"] != courseCode {
			continue
		}
		if match != nil && match.id != id {
			if courseID != "" || courseCode != "" {
				return nil, fmt.Errorf("multiple membox projections match course/video identity; resolve duplicates before publishing")
			}
			continue
		}
		copyMeta := meta
		match = &projection{id: id, meta: copyMeta}
	}
	return match, nil
}

// Publish writes or updates the membox Markdown projection for an echo-bp
// artifact. Identity is video_id (+ course); the readable filename is derived
// from the source video title (never LLM section headings) so pairs read as:
//
//	yt-<id>-transcript.md
//	yt-<id>-<video-title-slug>-summary.md
//
// and a manual graph edge links transcript → summary.
func Publish(ctx context.Context, service *application.Service, artifact Artifact) (Result, error) {
	// Transcript frontmatter title is the most reliable video title on the browser path.
	if t := transcriptVideoTitle(ctx, service, artifact.VideoID); t != "" {
		if strings.TrimSpace(artifact.CourseTitle) == "" {
			artifact.CourseTitle = t
		}
		if strings.TrimSpace(artifact.LectureTitle) == "" || looksLikeSectionHeading(artifact.LectureTitle) {
			artifact.LectureTitle = t
		}
	}
	displayTitle := preferDisplayTitle(artifact)
	filename := SummaryFilename(artifact)
	transcriptName := TranscriptFilename(artifact.VideoID)

	match, err := findProjection(ctx, service, artifact.VideoID, artifact.CourseID, artifact.CourseCode)
	if err != nil {
		return Result{}, err
	}
	var existingID string
	if match != nil {
		existingID = match.id
	}

	if target, _, resolveErr := service.ResolveDocumentByRelativePath(ctx, filename); resolveErr == nil && string(target.ID) != existingID {
		body, readErr := service.ReadDocument(ctx, string(target.ID))
		if readErr != nil || markdownFrontmatter(body)["video_id"] != artifact.VideoID {
			return Result{}, fmt.Errorf("%s already belongs to another document; resolve the name collision", filename)
		}
	}
	if existingID != "" {
		_, currentPath, resolveErr := service.ResolveDocument(ctx, existingID)
		if resolveErr != nil {
			return Result{}, resolveErr
		}
		if filepath.Base(currentPath) != filename {
			if _, renameErr := service.RenameDocument(ctx, existingID, filename, displayTitle); renameErr != nil {
				return Result{}, renameErr
			}
		}
	}

	body := renderMarkdown(artifact, displayTitle, transcriptName)
	upserted, err := service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{Filename: filename, Body: body})
	if err != nil {
		return Result{}, err
	}
	if upserted.Document == nil {
		return Result{}, errors.New("published summary has no document identity")
	}
	summaryID := string(upserted.Document.ID)

	// Semantic pair: transcript document → summary (and reverse) when captions exist.
	if transcriptID := findTranscriptDocumentID(ctx, service, artifact.VideoID); transcriptID != "" && transcriptID != summaryID {
		_, _ = service.LinkDocuments(ctx, transcriptID, summaryID)
		_, _ = service.LinkDocuments(ctx, summaryID, transcriptID)
	}

	return Result{
		DocumentID: summaryID, Path: upserted.Path, Filename: filename, Created: upserted.Created,
		CourseCode: artifact.CourseCode, CourseID: artifact.CourseID, CourseTitle: artifact.CourseTitle,
		LectureNo: artifact.LectureNo, LectureNoSource: artifact.LectureNoSource, PlaylistIndex: artifact.PlaylistIndex,
		VideoID: artifact.VideoID, LectureTitle: displayTitle, SourceURL: artifact.SourceURL,
	}, nil
}

// SummaryFilename builds a stable, readable name.
// Browser/ad-hoc: yt-<video_id>-<slug>-summary.md (pairs with yt-<id>-transcript.md).
// Course path: <course>-lec<N>-<slug>.md.
// Slug always comes from the source video title, never LLM section headings.
func SummaryFilename(artifact Artifact) string {
	slug := titleSlug(videoTitleForNaming(artifact))
	vid := strings.TrimSpace(artifact.VideoID)
	code := strings.TrimSpace(strings.ToLower(artifact.CourseCode))
	if code == "" || code == DefaultBrowserCourseCode {
		if slug == "" {
			return fmt.Sprintf("yt-%s-summary.md", vid)
		}
		return fmt.Sprintf("yt-%s-%s-summary.md", vid, slug)
	}
	if artifact.LectureNo > 0 {
		if slug == "" {
			return fmt.Sprintf("%s-lec%d.md", code, artifact.LectureNo)
		}
		return fmt.Sprintf("%s-lec%d-%s.md", code, artifact.LectureNo, slug)
	}
	if slug == "" {
		return fmt.Sprintf("%s-%s.md", code, vid)
	}
	return fmt.Sprintf("%s-%s.md", code, slug)
}

// preferDisplayTitle is the human title in frontmatter / rename UI.
func preferDisplayTitle(artifact Artifact) string {
	if t := videoTitleForNaming(artifact); t != "" {
		return t
	}
	return strings.TrimSpace(artifact.VideoID)
}

// videoTitleForNaming picks the source video title for filenames.
// Prefer LectureTitle (per-video) then CourseTitle (playlist/collection),
// skipping LLM section headings like "一、背景与动机（00:00:00 – 00:07:52）".
func videoTitleForNaming(artifact Artifact) string {
	for _, candidate := range []string{artifact.LectureTitle, artifact.CourseTitle} {
		t := strings.TrimSpace(candidate)
		if t == "" || looksLikeSectionHeading(t) {
			continue
		}
		return t
	}
	return ""
}

var (
	reTimestamp = regexp.MustCompile(`\d{1,2}:\d{2}(?::\d{2})?`)
	reCNSection = regexp.MustCompile(`^[一二三四五六七八九十百千0-9]+[、.．．:：]`)
	reCNChapter = regexp.MustCompile(`^第[一二三四五六七八九十百千0-9]+[章节講讲部份]`)
)

// looksLikeSectionHeading rejects LLM chapter titles so they never become filenames.
func looksLikeSectionHeading(title string) bool {
	t := strings.TrimSpace(title)
	if t == "" {
		return false
	}
	if reTimestamp.MatchString(t) && (strings.ContainsAny(t, "–—-(") || strings.Contains(t, "至")) {
		return true
	}
	if reCNSection.MatchString(t) || reCNChapter.MatchString(t) {
		return true
	}
	return false
}

func transcriptVideoTitle(ctx context.Context, service *application.Service, videoID string) string {
	id := findTranscriptDocumentID(ctx, service, videoID)
	if id == "" {
		return ""
	}
	body, err := service.ReadDocument(ctx, id)
	if err != nil {
		return ""
	}
	t := strings.TrimSpace(markdownFrontmatter(body)["title"])
	if t == "" || looksLikeSectionHeading(t) {
		return ""
	}
	return t
}

func titleSlug(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	// Cap length so filenames stay readable in listings.
	const max = 48
	if len(slug) > max {
		slug = strings.Trim(slug[:max], "-")
	}
	return slug
}

func findTranscriptDocumentID(ctx context.Context, service *application.Service, videoID string) string {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return ""
	}
	filename := TranscriptFilename(videoID)
	if doc, _, err := service.ResolveDocumentByRelativePath(ctx, filename); err == nil && doc != nil {
		return string(doc.ID)
	}
	documents, err := service.ListDocuments(ctx, 1000, false, "")
	if err != nil {
		return ""
	}
	for _, record := range documents {
		id := string(record.Document.ID)
		body, readErr := service.ReadDocument(ctx, id)
		if readErr != nil {
			continue
		}
		meta := markdownFrontmatter(body)
		if meta["kind"] == "youtube-transcript" && meta["video_id"] == videoID {
			return id
		}
	}
	return ""
}

func renderMarkdown(artifact Artifact, displayTitle, transcriptFile string) string {
	summary := stripFrontmatter(strings.TrimSpace(artifact.Summary))
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("kind: youtube-lecture-summary\n")
	b.WriteString("managed_by: echo-bp\n")
	b.WriteString("course_code: " + yamlScalar(artifact.CourseCode) + "\n")
	b.WriteString("course_id: " + yamlScalar(artifact.CourseID) + "\n")
	b.WriteString("course_title: " + yamlScalar(artifact.CourseTitle) + "\n")
	b.WriteString(fmt.Sprintf("lecture_no: %d\n", artifact.LectureNo))
	b.WriteString("lecture_no_source: " + yamlScalar(artifact.LectureNoSource) + "\n")
	b.WriteString(fmt.Sprintf("playlist_index: %d\n", artifact.PlaylistIndex))
	b.WriteString("video_id: " + yamlScalar(artifact.VideoID) + "\n")
	b.WriteString("title: " + yamlScalar(displayTitle) + "\n")
	b.WriteString("source_url: " + yamlScalar(artifact.SourceURL) + "\n")
	if tf := strings.TrimSpace(transcriptFile); tf != "" {
		b.WriteString("transcript_file: " + yamlScalar(tf) + "\n")
		b.WriteString("related: " + yamlScalar(tf) + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(summary)
	if !strings.HasSuffix(summary, "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func yamlScalar(value string) string { return strconv.Quote(strings.TrimSpace(value)) }

// TranscriptFilename is the stable membox name for a browser-captured caption file.
func TranscriptFilename(videoID string) string {
	return fmt.Sprintf("yt-%s-transcript.md", strings.TrimSpace(videoID))
}

// TranscriptMeta describes one browser-captured YouTube caption document.
type TranscriptMeta struct {
	VideoID  string
	Title    string
	URL      string
	Lang     string
	Source   string
	Markdown string // full body including optional frontmatter; rebuilt if empty body lines only
}

// EnsureTranscriptMarkdown adds membox frontmatter when the client sent a bare body.
func EnsureTranscriptMarkdown(meta TranscriptMeta) string {
	body := strings.TrimSpace(meta.Markdown)
	if body == "" {
		return ""
	}
	if strings.HasPrefix(body, "---\n") || strings.HasPrefix(body, "---\r\n") {
		return body + "\n"
	}
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = meta.VideoID
	}
	lang := strings.TrimSpace(meta.Lang)
	if lang == "" {
		lang = "en"
	}
	source := strings.TrimSpace(meta.Source)
	if source == "" {
		source = "browser"
	}
	url := strings.TrimSpace(meta.URL)
	if url == "" {
		url = "https://www.youtube.com/watch?v=" + meta.VideoID
	}
	return fmt.Sprintf("---\nkind: youtube-transcript\nmanaged_by: membox-clipper\nvideo_id: %s\ntitle: %s\nsource_url: %s\nlang: %s\nsource: %s\n---\n\n%s\n",
		yamlScalar(meta.VideoID), yamlScalar(title), yamlScalar(url), yamlScalar(lang), yamlScalar(source), body)
}

// PublishTranscript upserts the caption Markdown under yt-<video_id>-transcript.md.
func PublishTranscript(ctx context.Context, service *application.Service, meta TranscriptMeta) (path string, documentID string, err error) {
	videoID := strings.TrimSpace(meta.VideoID)
	if videoID == "" {
		return "", "", errors.New("video_id is required")
	}
	body := EnsureTranscriptMarkdown(meta)
	if strings.TrimSpace(body) == "" {
		return "", "", errors.New("transcript markdown is empty")
	}
	filename := TranscriptFilename(videoID)
	// Prefer prior transcript doc for this video_id so UUID stays stable.
	existingID := ""
	documents, listErr := service.ListDocuments(ctx, 1000, false, "")
	if listErr != nil {
		return "", "", listErr
	}
	for _, record := range documents {
		id := string(record.Document.ID)
		raw, readErr := service.ReadDocument(ctx, id)
		if readErr != nil {
			continue
		}
		m := markdownFrontmatter(raw)
		if m["kind"] == "youtube-transcript" && m["video_id"] == videoID {
			existingID = id
			break
		}
	}
	if existingID != "" {
		_, currentPath, resolveErr := service.ResolveDocument(ctx, existingID)
		if resolveErr != nil {
			return "", "", resolveErr
		}
		if filepath.Base(currentPath) != filename {
			if _, renameErr := service.RenameDocument(ctx, existingID, filename, strings.TrimSpace(meta.Title)); renameErr != nil {
				return "", "", renameErr
			}
		}
	}
	upserted, upsertErr := service.UpsertMarkdown(ctx, application.UpsertMarkdownOptions{Filename: filename, Body: body})
	if upsertErr != nil {
		return "", "", upsertErr
	}
	if upserted.Document == nil {
		return "", "", errors.New("published transcript has no document identity")
	}
	return upserted.Path, string(upserted.Document.ID), nil
}

func stripFrontmatter(body string) string {
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
