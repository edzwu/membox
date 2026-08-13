package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"membox/internal/application"
	"membox/internal/bootstrap"
)

type fakeEchoSummary struct{ artifact echoSummaryArtifact }

func (f fakeEchoSummary) Summarize(context.Context, VideoSummaryRequest) (echoSummaryArtifact, error) {
	return f.artifact, nil
}

func TestPublishVideoSummaryUsesReadableStableFilename(t *testing.T) {
	home := shortTempDir(t)
	notes := t.TempDir()
	config, err := DefaultConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	service, err := bootstrap.Open(config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.AddPath(context.Background(), notes); err != nil {
		t.Fatal(err)
	}
	artifact := echoSummaryArtifact{
		CourseCode: "cs336", CourseID: "PL336", CourseTitle: "Stanford CS336",
		LectureNo: 2, LectureNoSource: "title", PlaylistIndex: 13,
		VideoID: "video-two", LectureTitle: "Tokenizer Design",
		SourceURL: "https://youtube.com/watch?v=video-two",
		Summary:   "---\nvideo_id: old\n---\n\n# Tokenizer Design\n\nSummary.",
	}
	result, err := publishVideoSummary(context.Background(), service, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if result.Filename != "cs336-lec2.md" || !result.Created || result.DocumentID == "" {
		t.Fatalf("first result = %+v", result)
	}
	body, err := os.ReadFile(filepath.Join(notes, result.Filename))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"managed_by: echo-bp", `course_code: "cs336"`, `course_id: "PL336"`, "lecture_no: 2", `lecture_no_source: "title"`, "playlist_index: 13", `video_id: "video-two"`, "# Tokenizer Design", "Summary."} {
		if !strings.Contains(text, want) {
			t.Fatalf("published body missing %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "video_id:") != 1 {
		t.Fatalf("echo-bp frontmatter was not replaced:\n%s", text)
	}

	artifact.Summary = "# Tokenizer Design\n\nRegenerated."
	result2, err := publishVideoSummary(context.Background(), service, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Created || result2.DocumentID != result.DocumentID {
		t.Fatalf("upsert lost identity: first=%+v second=%+v", result, result2)
	}
}

func TestPublishVideoSummaryRejectsOccupiedReadableSlot(t *testing.T) {
	home := shortTempDir(t)
	notes := t.TempDir()
	config, _ := DefaultConfig(home)
	service, err := bootstrap.Open(config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.AddPath(context.Background(), notes); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertMarkdown(context.Background(), application.UpsertMarkdownOptions{Filename: "cs336-lec2.md", Body: "# Personal note\n"}); err != nil {
		t.Fatal(err)
	}
	artifact := echoSummaryArtifact{CourseCode: "cs336", CourseID: "PL336", LectureNo: 2, VideoID: "v", LectureTitle: "L", SourceURL: "u", Summary: "# L\n"}
	if _, err := publishVideoSummary(context.Background(), service, artifact); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Fatalf("occupied slot error = %v", err)
	}
}

func TestPublishVideoSummaryRenamesWhenLectureOrderChanges(t *testing.T) {
	home := shortTempDir(t)
	notes := t.TempDir()
	config, _ := DefaultConfig(home)
	service, err := bootstrap.Open(config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.AddPath(context.Background(), notes); err != nil {
		t.Fatal(err)
	}
	artifact := echoSummaryArtifact{CourseCode: "cs336", CourseID: "PL336", LectureNo: 2, VideoID: "v", LectureTitle: "L", SourceURL: "u", Summary: "# L\n"}
	first, err := publishVideoSummary(context.Background(), service, artifact)
	if err != nil {
		t.Fatal(err)
	}
	artifact.LectureNo = 3
	second, err := publishVideoSummary(context.Background(), service, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if first.DocumentID != second.DocumentID || second.Filename != "cs336-lec3.md" {
		t.Fatalf("rename result first=%+v second=%+v", first, second)
	}
	if _, err := os.Stat(filepath.Join(notes, "cs336-lec2.md")); !os.IsNotExist(err) {
		t.Fatalf("old lecture path remains: %v", err)
	}
}

func TestVideoSummaryAPIProcessesAndPublishes(t *testing.T) {
	home := shortTempDir(t)
	notes := t.TempDir()
	config, _ := DefaultConfig(home)
	seed, err := bootstrap.Open(config.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.AddPath(context.Background(), notes); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := New(config)
	d.echoSummary = fakeEchoSummary{artifact: echoSummaryArtifact{CourseCode: "cs336", CourseID: "PL", LectureNo: 1, VideoID: "v1", LectureTitle: "Intro", SourceURL: "u", Summary: "# Intro\n"}}
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run(ctx) }()
	waitFor(t, func() bool { _, err := Healthcheck(config); return err == nil })

	requestCtx, requestCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer requestCancel()
	result, err := VideoSummary(requestCtx, config, VideoSummaryRequest{URL: "https://youtube.com/watch?v=v1", CourseCode: "cs336"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Filename != "cs336-lec1.md" || result.DocumentID == "" {
		t.Fatalf("result = %+v", result)
	}
	if err := Stop(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
