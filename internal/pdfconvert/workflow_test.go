package pdfconvert

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	serverURL string
	filename  string
	body      string
	result    RemoteResult
	progress  []Progress
}

func (f *fakeClient) Convert(_ context.Context, serverURL, filename string, body io.Reader, onProgress func(Progress)) (RemoteResult, error) {
	f.serverURL, f.filename = serverURL, filename
	content, _ := io.ReadAll(body)
	f.body = string(content)
	if onProgress != nil {
		for _, progress := range f.progress {
			onProgress(progress)
		}
	}
	if f.result.Markdown != "" {
		return f.result, nil
	}
	return RemoteResult{
		Filename: filename, Markdown: "# Converted\n\n![](images/chart.jpg)\n", MarkdownSHA256: "sha-md",
		Assets: []Asset{{RelativePath: "images/chart.jpg", Body: []byte("jpeg")}},
	}, nil
}

type fakePublication struct {
	owner    string
	filename string
	body     string
	assets   []Asset
	result   PublishedMarkdown
}

type fakeLink struct{ from, to string }

type fakeWorkspace struct {
	source            Source
	publishedFilename string
	publishedBody     string
	publishedAssets   []Asset
	publications      []fakePublication
	links             []fakeLink
	linkedFrom        string
	linkedTo          string
}

func (f *fakeWorkspace) OpenPDF(context.Context, string) (Source, error) { return f.source, nil }
func (f *fakeWorkspace) ResolveBundleFilename(_ context.Context, _, proposed string) (string, error) {
	return proposed, nil
}
func (f *fakeWorkspace) PublishBundle(_ context.Context, owner, filename, body string, assets []Asset) (PublishedMarkdown, error) {
	f.publishedFilename, f.publishedBody, f.publishedAssets = filename, body, assets
	published := PublishedMarkdown{
		DocumentID: "markdown-" + string(rune('1'+len(f.publications))),
		Path:       "/notes/" + filename,
		Created:    true,
	}
	f.publications = append(f.publications, fakePublication{owner: owner, filename: filename, body: body, assets: assets, result: published})
	return published, nil
}
func (f *fakeWorkspace) LinkDocuments(_ context.Context, from, to string) error {
	f.linkedFrom, f.linkedTo = from, to
	f.links = append(f.links, fakeLink{from: from, to: to})
	return nil
}

func TestWorkflowKeepsProtocolAndWorkspaceSeparated(t *testing.T) {
	home := t.TempDir()
	config := NewConfigStore(home)
	if _, err := config.SaveServerURL("http://converter.test:8000/"); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{progress: []Progress{{Stage: "split", TotalPages: 12, InitialChunkPages: 4}}}
	workspace := &fakeWorkspace{source: Source{
		DocumentID: "019ffe58-b0af-7b65-baf1-2b7d9c066b19",
		Filename:   "book.pdf",
		MediaType:  "application/pdf",
		Body:       io.NopCloser(strings.NewReader("%PDF-source")),
	}}
	var progress []Progress
	result, err := NewWorkflow(client, config).ConvertWithProgress(context.Background(), workspace, "6b19", "", func(event Progress) {
		progress = append(progress, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) != 2 || progress[0].Stage != "split" || progress[1].Stage != "publish" {
		t.Fatalf("workflow progress=%+v", progress)
	}
	if client.serverURL != "http://converter.test:8000" || client.filename != "book.pdf" || client.body != "%PDF-source" {
		t.Fatalf("unexpected client request: %+v", client)
	}
	if workspace.publishedFilename != "book-pdf-019ffe58b0af7b65baf12b7d9c066b19.md" || workspace.publishedBody != "# Converted\n\n![](/api/pdf-assets/019ffe58-b0af-7b65-baf1-2b7d9c066b19/images/chart.jpg)\n" {
		t.Fatalf("unexpected publication: %q %q", workspace.publishedFilename, workspace.publishedBody)
	}
	if workspace.publications[0].owner != workspace.source.DocumentID {
		t.Fatalf("asset owner=%q want=%q", workspace.publications[0].owner, workspace.source.DocumentID)
	}
	if len(workspace.publishedAssets) != 1 || workspace.publishedAssets[0].RelativePath != "images/chart.jpg" {
		t.Fatalf("converted assets were not published: %+v", workspace.publishedAssets)
	}
	if workspace.linkedFrom != workspace.source.DocumentID || workspace.linkedTo != "markdown-1" {
		t.Fatalf("conversion link missing: %s -> %s", workspace.linkedFrom, workspace.linkedTo)
	}
	if result.MarkdownDocumentID != "markdown-1" || !result.Created {
		t.Fatalf("unexpected workflow result: %+v", result)
	}
}

func TestWorkflowPublishesStructuredBookAsLinkedChapterDocuments(t *testing.T) {
	home := t.TempDir()
	config := NewConfigStore(home)
	if _, err := config.SaveServerURL("http://converter.test:8000"); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{result: RemoteResult{
		Markdown:       "# Long Book\n\n## 引言\n\nintro\n\n## 第 1 章 Start\n\none\n\n## 第 2 章 Context\n\ntwo\n",
		MarkdownSHA256: "sha-long",
		Assets:         []Asset{{RelativePath: "images/chart.jpg", Body: []byte("jpeg")}},
	}}
	workspace := &fakeWorkspace{source: Source{
		DocumentID: "019ffe58-b0af-7b65-baf1-2b7d9c066b19", Filename: "book.pdf",
		MediaType: "application/pdf", Body: io.NopCloser(strings.NewReader("%PDF-source")),
	}}
	result, err := NewWorkflow(client, config).Convert(context.Background(), workspace, "6b19", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chapters) != 3 || len(workspace.publications) != 4 {
		t.Fatalf("unexpected split publication: result=%+v publications=%+v", result, workspace.publications)
	}
	index := workspace.publications[3]
	if index.filename != "book-pdf-019ffe58b0af7b65baf12b7d9c066b19.md" || strings.Contains(index.body, "intro\n") {
		t.Fatalf("unexpected index publication: %+v", index)
	}
	// TOC must use catalog identity links, not bare chapter filenames.
	if strings.Contains(index.body, "chapter-001.md") || !strings.Contains(index.body, "/?id=") {
		t.Fatalf("index TOC must use /?id= document links: %+v", index)
	}
	if len(workspace.publications[0].assets) != 1 {
		t.Fatalf("assets were not published with chapters: %+v", workspace.publications)
	}
	if len(workspace.links) != 7 {
		t.Fatalf("links=%d want PDF->index plus bidirectional chapter links: %+v", len(workspace.links), workspace.links)
	}
	indexID := index.result.DocumentID
	if workspace.links[0] != (fakeLink{from: workspace.source.DocumentID, to: indexID}) {
		t.Fatalf("PDF index link missing: %+v", workspace.links)
	}
	for chapterIndex, chapter := range result.Chapters {
		forward := workspace.links[1+chapterIndex*2]
		backward := workspace.links[2+chapterIndex*2]
		if forward != (fakeLink{from: indexID, to: chapter.DocumentID}) || backward != (fakeLink{from: chapter.DocumentID, to: indexID}) {
			t.Fatalf("chapter %d links are not bidirectional: %+v %+v", chapterIndex, forward, backward)
		}
	}
}

func TestConvertedFilenameKeepsReadablePDFStemAndStableIdentity(t *testing.T) {
	source := Source{DocumentID: "019ffe58-b0af-7b65-baf1-2b7d9c066b19", Filename: "labuladong 的算法小抄（官方完整版）.pdf"}
	got := convertedFilename(source)
	want := "labuladong-的算法小抄-官方完整版-pdf-019ffe58b0af7b65baf12b7d9c066b19.md"
	if got != want {
		t.Fatalf("converted filename=%q want=%q", got, want)
	}
	if stem := readablePDFStem("---.pdf"); stem != "document" {
		t.Fatalf("punctuation-only readable stem=%q", stem)
	}
}

func TestConfigStorePersistsFeatureConfigOutsideCatalog(t *testing.T) {
	home := t.TempDir()
	store := NewConfigStore(home)
	config, err := store.SaveServerURL("http://192.168.3.42:8000/")
	if err != nil {
		t.Fatal(err)
	}
	if config.ServerURL != "http://192.168.3.42:8000" || store.Path() != filepath.Join(home, ConfigFilename) {
		t.Fatalf("unexpected config: %+v path=%s", config, store.Path())
	}
	loaded, err := store.Load()
	if err != nil || loaded != config {
		t.Fatalf("config round trip: %+v err=%v", loaded, err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("feature config is not private: %o", info.Mode().Perm())
	}
}

func TestRewriteAssetReferencesEscapesMarkdownUnsafeFilenames(t *testing.T) {
	markdown := "# Chapter\n\n![](images/Fooled by randomness__part004__abc123.jpg)\n\n![](images/plain.png)\n"
	assets := []Asset{
		{RelativePath: "images/Fooled by randomness__part004__abc123.jpg", Body: []byte("jpg")},
		{RelativePath: "images/plain.png", Body: []byte("png")},
	}
	rewritten := rewriteAssetReferences(markdown, "01a00110-6cb4-79db-952c-02e705f8c0ea", assets)
	wantEscaped := "/api/pdf-assets/01a00110-6cb4-79db-952c-02e705f8c0ea/images/Fooled%20by%20randomness__part004__abc123.jpg"
	wantPlain := "/api/pdf-assets/01a00110-6cb4-79db-952c-02e705f8c0ea/images/plain.png"
	if !strings.Contains(rewritten, "![]("+wantEscaped+")") {
		t.Fatalf("spaced filename was not percent-encoded: %q", rewritten)
	}
	if !strings.Contains(rewritten, "![]("+wantPlain+")") {
		t.Fatalf("plain filename was mangled: %q", rewritten)
	}
	if strings.Contains(rewritten, "images/Fooled by randomness") {
		t.Fatalf("raw spaced reference remained: %q", rewritten)
	}
}

type spyPlanner struct {
	called bool
	plans  []ChapterPlan
}

func (p *spyPlanner) PlanChapters(context.Context, PlanRequest) ([]ChapterPlan, error) {
	p.called = true
	return p.plans, nil
}

func TestWorkflowSkipsPlannerForSmallMarkdown(t *testing.T) {
	home := t.TempDir()
	config := NewConfigStore(home)
	if _, err := config.SaveServerURL("http://converter.test:8000/"); err != nil {
		t.Fatal(err)
	}
	workspace := &fakeWorkspace{source: Source{
		DocumentID: "019ffe58-b0af-7b65-baf1-2b7d9c066b19", Filename: "small.pdf",
		MediaType: "application/pdf", Body: io.NopCloser(strings.NewReader("%PDF-small")),
	}}
	client := &fakeClient{result: RemoteResult{Filename: "small.pdf", Markdown: "# Small\n\nshort body", MarkdownSHA256: "sha"}}
	spy := &spyPlanner{}
	workflow := NewWorkflow(client, config)
	workflow.SetStructurePlanner(spy)
	if _, err := workflow.Convert(context.Background(), workspace, "x", ""); err != nil {
		t.Fatal(err)
	}
	if spy.called {
		t.Fatal("planner must not run for small converted documents")
	}
	if len(workspace.publications) != 1 || strings.Contains(workspace.publications[0].body, "## 目录") {
		t.Fatalf("small document must stay whole: %+v", workspace.publications)
	}
}

func TestWorkflowRunsPlannerForLargeUnstructuredMarkdown(t *testing.T) {
	home := t.TempDir()
	config := NewConfigStore(home)
	if _, err := config.SaveServerURL("http://converter.test:8000/"); err != nil {
		t.Fatal(err)
	}
	markdown := "# Big Book\n\n" +
		strings.Repeat("Opening movement of the tale with plenty of words. ", 900) + "\n\n" +
		strings.Repeat("Closing movement finishes the tale with final words. ", 900)
	workspace := &fakeWorkspace{source: Source{
		DocumentID: "019ffe58-b0af-7b65-baf1-2b7d9c066b19", Filename: "big.pdf",
		MediaType: "application/pdf", Body: io.NopCloser(strings.NewReader("%PDF-big")),
	}}
	client := &fakeClient{result: RemoteResult{Filename: "big.pdf", Markdown: markdown, MarkdownSHA256: "sha"}}
	spy := &spyPlanner{plans: []ChapterPlan{
		{Title: "Opening", Line: 3},
		{Title: "Closing", Line: 5},
	}}
	workflow := NewWorkflow(client, config)
	workflow.SetStructurePlanner(spy)
	if _, err := workflow.Convert(context.Background(), workspace, "x", ""); err != nil {
		t.Fatal(err)
	}
	if !spy.called {
		t.Fatal("planner must run for large unstructured documents")
	}
	if len(workspace.publications) != 4 { // whole bundle (phase 1) + 2 chapters + index
		t.Fatalf("expected whole bundle, two chapters, and index: got %d publications", len(workspace.publications))
	}
}

type slowPlanner struct{ delay time.Duration }

func (p slowPlanner) PlanChapters(_ context.Context, _ PlanRequest) ([]ChapterPlan, error) {
	time.Sleep(p.delay)
	return nil, fmt.Errorf("planner too slow")
}

type staticPlanner struct{ plans []ChapterPlan }

func (p staticPlanner) PlanChapters(_ context.Context, _ PlanRequest) ([]ChapterPlan, error) {
	return p.plans, nil
}

// When the deterministic splitter fails and the planner is slow, the whole
// converted bundle must be published anyway — visibility must not wait on the
// local model.
func TestWorkflowPublishesWholeBundleBeforeSlowPlanner(t *testing.T) {
	// No chapter headings, over the planner size gate.
	markdown := "# Blob\n\n" + strings.Repeat("lorem ipsum dolor sit amet\n\n", 4000)
	client := &fakeClient{result: RemoteResult{Filename: "blob-pdf-0000000000000000000000000000000f.md", Markdown: markdown, MarkdownSHA256: "sha"}}
	workflow := NewWorkflow(client, NewConfigStore(t.TempDir()))
	workflow.SetStructurePlanner(slowPlanner{delay: 150 * time.Millisecond})
	workspace := &fakeWorkspace{source: Source{DocumentID: "pdf-1", Filename: "blob.pdf", MediaType: "application/pdf", Body: io.NopCloser(strings.NewReader("%PDF-1.4"))}}

	result, err := workflow.Convert(context.Background(), workspace, "pdf-1", "http://192.168.1.10:8000")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chapters) != 0 {
		t.Fatalf("expected whole-doc result, got %d chapters", len(result.Chapters))
	}
	// Exactly one publication (the whole bundle), and it carries the full body.
	if len(workspace.publications) != 1 {
		t.Fatalf("expected exactly 1 publication, got %d", len(workspace.publications))
	}
	if workspace.publications[0].body != markdown {
		t.Fatal("whole bundle was not published with the full markdown")
	}
}

// A successful planner upgrades the early whole-bundle publication into
// index+chapters (the index publishes last, overwriting the whole body).
func TestWorkflowUpgradesWholeBundleWhenPlannerSplits(t *testing.T) {
	markdown := "# Blob\n\n" + strings.Repeat("alpha beta gamma\n\n", 4000)
	// Planner line numbers must exist in the sampled structure sketch.
	_, sketchLines := BuildStructureSketch(markdown, 0)
	lineNumbers := make([]int, 0, len(sketchLines))
	for line := range sketchLines {
		lineNumbers = append(lineNumbers, line)
	}
	sort.Ints(lineNumbers)
	if len(lineNumbers) < 2 {
		t.Fatalf("sketch too small: %v", lineNumbers)
	}
	plans := []ChapterPlan{{Title: "Part A", Line: lineNumbers[0]}, {Title: "Part B", Line: lineNumbers[len(lineNumbers)/2]}}

	client := &fakeClient{result: RemoteResult{Filename: "blob-pdf-0000000000000000000000000000000f.md", Markdown: markdown, MarkdownSHA256: "sha"}}
	workflow := NewWorkflow(client, NewConfigStore(t.TempDir()))
	workflow.SetStructurePlanner(staticPlanner{plans: plans})
	workspace := &fakeWorkspace{source: Source{DocumentID: "pdf-1", Filename: "blob.pdf", MediaType: "application/pdf", Body: io.NopCloser(strings.NewReader("%PDF-1.4"))}}

	result, err := workflow.Convert(context.Background(), workspace, "pdf-1", "http://192.168.1.10:8000")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chapters) < 2 {
		t.Fatalf("expected planned chapters, got %d", len(result.Chapters))
	}
	// Phase 1 whole-bundle publish happened before the chapter publications.
	if len(workspace.publications) < 3 {
		t.Fatalf("expected whole + chapters publications, got %d", len(workspace.publications))
	}
	if workspace.publications[0].body != markdown {
		t.Fatal("phase 1 publication was not the whole bundle")
	}
}
