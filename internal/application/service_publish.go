package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"membox/internal/application/port"
	"membox/internal/domain/catalog"
)

// SettingBlogRoot configures the Hugo site directory that publish operations
// export into. It must contain a config.toml (the agora blog root).
const SettingBlogRoot = "blog_root"

// SettingBlogBaseURL is the public site's origin, used to display live URLs.
const SettingBlogBaseURL = "blog_base_url"

// DefaultBlogBaseURL matches the GitHub Pages deployment target.
const DefaultBlogBaseURL = "https://edzwu.github.io"

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// PublishOptions controls exporting one document to the public blog.
type PublishOptions struct {
	Selector string
	Lang     string // "en" (default) or "zh"
	Slug     string // default: the document's logical short ID
	NoPush   bool   // commit locally without pushing
}

// Publication describes one document's public-blog state.
type Publication struct {
	DocumentID      string // physical UUID
	Slug            string
	Lang            string
	Title           string
	Status          string // "published" | "stale"
	PublishedSHA256 string
	CurrentSHA256   string
	PublishedAt     time.Time
	BundlePath      string // absolute path of the generated bundle directory
	URL             string // live page URL
}

const (
	PublicationPublished = "published"
	PublicationStale     = "stale"
)

// Publish exports a document into blog/content/notes/<slug>/, records the
// publication, and commits/pushes the generated files.
func (s *Service) Publish(ctx context.Context, opts PublishOptions) (Publication, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return Publication{}, lockErr
	}
	defer release()
	if s.blogGit == nil {
		return Publication{}, errors.New("blog git committer is not configured")
	}
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return Publication{}, err
	}
	document, absolute, err := s.ResolveDocument(ctx, opts.Selector)
	if err != nil {
		return Publication{}, err
	}
	if document.Index.MediaType != "text/markdown" {
		return Publication{}, fmt.Errorf("only Markdown documents can be published, %s is %s", document.Index.Title, document.Index.MediaType)
	}
	// Re-publishing inherits the existing slug/language unless explicitly
	// overridden, so `mm blog publish <id> --lang zh` updates in place
	// instead of forking a new bundle under the default slug.
	existing, found, err := s.store.GetPublication(ctx, document.ID)
	if err != nil {
		return Publication{}, err
	}
	lang := strings.TrimSpace(opts.Lang)
	if lang == "" {
		lang = "en"
		if found {
			lang = existing.Lang
		}
	}
	if lang != "en" && lang != "zh" {
		return Publication{}, fmt.Errorf("unsupported language %q: use en or zh", opts.Lang)
	}
	slug := strings.TrimSpace(opts.Slug)
	if slug == "" {
		if found {
			slug = existing.Slug
		} else {
			slug, err = s.store.LogicalID(ctx, string(document.ID))
			if err != nil {
				return Publication{}, err
			}
		}
	}
	if !slugPattern.MatchString(slug) {
		return Publication{}, fmt.Errorf("invalid slug %q: use lowercase letters, digits, and dashes", slug)
	}
	body, err := s.ReadDocumentAt(ctx, document, absolute)
	if err != nil {
		return Publication{}, err
	}
	// Re-publishing with a different slug retires the old bundle; a same-slug
	// re-export cleans the directory first so a language switch cannot leave
	// a stale index.<old-lang>.md behind.
	stagePaths := []string{}
	if found && existing.Slug != slug {
		oldDir := filepath.Join(blogRoot, "content", "notes", existing.Slug)
		if err := os.RemoveAll(oldDir); err != nil {
			return Publication{}, fmt.Errorf("removing previous bundle %s: %w", oldDir, err)
		}
		stagePaths = append(stagePaths, filepath.Join("content", "notes", existing.Slug))
	}
	if err := os.RemoveAll(filepath.Join(blogRoot, "content", "notes", slug)); err != nil {
		return Publication{}, err
	}
	bundleDir, err := exportBundle(blogRoot, slug, lang, document.Index.Title, documentDate(document), body)
	if err != nil {
		return Publication{}, err
	}
	stagePaths = append(stagePaths, filepath.Join("content", "notes", slug))
	record := port.PublicationRecord{
		DocumentID:      string(document.ID),
		Slug:            slug,
		Lang:            lang,
		PublishedSHA256: document.Index.SHA256,
		PublishedAt:     s.clock.Now(),
	}
	if err := s.store.UpsertPublication(ctx, record); err != nil {
		return Publication{}, err
	}
	message := fmt.Sprintf("publish: %s (%s)", document.Index.Title, slug)
	if found {
		message = fmt.Sprintf("update: %s (%s)", document.Index.Title, slug)
	}
	if _, err := s.blogGit.CommitAndPush(ctx, blogRoot, message, stagePaths, !opts.NoPush); err != nil {
		return Publication{}, err
	}
	return Publication{
		DocumentID:      record.DocumentID,
		Slug:            slug,
		Lang:            lang,
		Title:           document.Index.Title,
		Status:          PublicationPublished,
		PublishedSHA256: record.PublishedSHA256,
		CurrentSHA256:   document.Index.SHA256,
		PublishedAt:     record.PublishedAt,
		BundlePath:      bundleDir,
		URL:             publicationURL(s.blogBaseURL(ctx), lang, slug),
	}, nil
}

// Unpublish removes a document's blog bundle, deletes the publication record,
// and commits/pushes the removal.
func (s *Service) Unpublish(ctx context.Context, selector string, noPush bool) (Publication, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return Publication{}, lockErr
	}
	defer release()
	if s.blogGit == nil {
		return Publication{}, errors.New("blog git committer is not configured")
	}
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return Publication{}, err
	}
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return Publication{}, err
	}
	record, found, err := s.store.GetPublication(ctx, document.ID)
	if err != nil {
		return Publication{}, err
	}
	if !found {
		return Publication{}, fmt.Errorf("document %s is not published", document.Index.Title)
	}
	bundleDir := filepath.Join(blogRoot, "content", "notes", record.Slug)
	if err := os.RemoveAll(bundleDir); err != nil {
		return Publication{}, fmt.Errorf("removing bundle %s: %w", bundleDir, err)
	}
	if err := s.store.DeletePublication(ctx, document.ID); err != nil {
		return Publication{}, err
	}
	rel := filepath.Join("content", "notes", record.Slug)
	message := fmt.Sprintf("unpublish: %s (%s)", document.Index.Title, record.Slug)
	if _, err := s.blogGit.CommitAndPush(ctx, blogRoot, message, []string{rel}, !noPush); err != nil {
		return Publication{}, err
	}
	record.DocumentID = string(document.ID)
	return Publication{
		DocumentID: record.DocumentID,
		Slug:       record.Slug,
		Lang:       record.Lang,
		Title:      document.Index.Title,
		Status:     "unpublished",
		BundlePath: bundleDir,
		URL:        publicationURL(s.blogBaseURL(ctx), record.Lang, record.Slug),
	}, nil
}

// GetPublication returns the publication state for one document.
func (s *Service) GetPublication(ctx context.Context, selector string) (Publication, error) {
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return Publication{}, err
	}
	document, _, err := s.ResolveDocument(ctx, selector)
	if err != nil {
		return Publication{}, err
	}
	record, found, err := s.store.GetPublication(ctx, document.ID)
	if err != nil {
		return Publication{}, err
	}
	if !found {
		return Publication{}, fmt.Errorf("document %s is not published", document.Index.Title)
	}
	status := PublicationPublished
	if document.Index.SHA256 != record.PublishedSHA256 {
		status = PublicationStale
	}
	return Publication{
		DocumentID:      record.DocumentID,
		Slug:            record.Slug,
		Lang:            record.Lang,
		Title:           document.Index.Title,
		Status:          status,
		PublishedSHA256: record.PublishedSHA256,
		CurrentSHA256:   document.Index.SHA256,
		PublishedAt:     record.PublishedAt,
		BundlePath:      filepath.Join(blogRoot, "content", "notes", record.Slug),
		URL:             publicationURL(s.blogBaseURL(ctx), record.Lang, record.Slug),
	}, nil
}

// ListPublications returns every published document with drift status.
func (s *Service) ListPublications(ctx context.Context) ([]Publication, error) {
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return nil, err
	}
	records, err := s.store.ListPublications(ctx)
	if err != nil {
		return nil, err
	}
	publications := make([]Publication, 0, len(records))
	for _, record := range records {
		publication := Publication{
			DocumentID:      record.DocumentID,
			Slug:            record.Slug,
			Lang:            record.Lang,
			PublishedSHA256: record.PublishedSHA256,
			PublishedAt:     record.PublishedAt,
			BundlePath:      filepath.Join(blogRoot, "content", "notes", record.Slug),
			URL:             publicationURL(s.blogBaseURL(ctx), record.Lang, record.Slug),
			Status:          PublicationPublished,
		}
		document, _, resolveErr := s.store.ResolveDocument(ctx, record.DocumentID)
		if resolveErr == nil && document != nil {
			publication.Title = document.Index.Title
			publication.CurrentSHA256 = document.Index.SHA256
			if document.Index.SHA256 != record.PublishedSHA256 {
				publication.Status = PublicationStale
			}
		}
		publications = append(publications, publication)
	}
	return publications, nil
}

// SyncPublications re-exports every stale publication and commits/pushes them
// in a single commit. It returns the number of re-exported documents.
func (s *Service) SyncPublications(ctx context.Context, noPush bool) (int, error) {
	release, lockErr := s.beginMutation()
	if lockErr != nil {
		return 0, lockErr
	}
	defer release()
	if s.blogGit == nil {
		return 0, errors.New("blog git committer is not configured")
	}
	publications, err := s.ListPublications(ctx)
	if err != nil {
		return 0, err
	}
	blogRoot, err := s.blogRoot(ctx)
	if err != nil {
		return 0, err
	}
	var slugs []string
	var titles []string
	for _, publication := range publications {
		if publication.Status != PublicationStale {
			continue
		}
		document, absolute, err := s.store.ResolveDocument(ctx, publication.DocumentID)
		if err != nil {
			return 0, err
		}
		body, err := s.ReadDocumentAt(ctx, document, absolute)
		if err != nil {
			return 0, err
		}
		if _, err := exportBundle(blogRoot, publication.Slug, publication.Lang, document.Index.Title, documentDate(document), body); err != nil {
			return 0, err
		}
		record := port.PublicationRecord{
			DocumentID:      publication.DocumentID,
			Slug:            publication.Slug,
			Lang:            publication.Lang,
			PublishedSHA256: document.Index.SHA256,
			PublishedAt:     s.clock.Now(),
		}
		if err := s.store.UpsertPublication(ctx, record); err != nil {
			return 0, err
		}
		slugs = append(slugs, publication.Slug)
		titles = append(titles, document.Index.Title)
	}
	if len(slugs) == 0 {
		return 0, nil
	}
	paths := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		paths = append(paths, filepath.Join("content", "notes", slug))
	}
	message := fmt.Sprintf("sync: update %d published note(s) [%s]", len(slugs), strings.Join(titles, ", "))
	if _, err := s.blogGit.CommitAndPush(ctx, blogRoot, message, paths, !noPush); err != nil {
		return 0, err
	}
	return len(slugs), nil
}

// blogRoot resolves and validates the configured blog root directory.
func (s *Service) blogRoot(ctx context.Context) (string, error) {
	root, err := s.store.GetSetting(ctx, SettingBlogRoot)
	if err != nil {
		return "", err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("blog root is not configured; run: mm config set %s <blog-directory>", SettingBlogRoot)
	}
	return root, nil
}

// blogBaseURL resolves the public site origin for display URLs.
func (s *Service) blogBaseURL(ctx context.Context) string {
	value, err := s.store.GetSetting(ctx, SettingBlogBaseURL)
	if err != nil || strings.TrimSpace(value) == "" {
		return DefaultBlogBaseURL
	}
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

// publicationURL builds the live page URL; the Hugo config publishes every
// language under its own subdirectory (defaultContentLanguageInSubdir).
func publicationURL(baseURL, lang, slug string) string {
	return fmt.Sprintf("%s/%s/notes/%s/", baseURL, lang, slug)
}

// validateBlogRoot checks that a directory looks like the Hugo blog root.
func validateBlogRoot(directory string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(directory))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("blog root %q: %w", directory, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("blog root %q is not a directory", directory)
	}
	if _, err := os.Stat(filepath.Join(absolute, "config.toml")); err != nil {
		return "", fmt.Errorf("blog root %q has no config.toml; point it at the Hugo site directory", directory)
	}
	return filepath.Clean(absolute), nil
}

// exportBundle writes blog/content/notes/<slug>/index.<lang>.md with generated
// TOML frontmatter and the cleaned document body. It returns the bundle dir.
func exportBundle(blogRoot, slug, lang, title string, date time.Time, body []byte) (string, error) {
	bundleDir := filepath.Join(blogRoot, "content", "notes", slug)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return "", fmt.Errorf("creating bundle %s: %w", bundleDir, err)
	}
	content := buildBundleMarkdown(title, date, body)
	filename := fmt.Sprintf("index.%s.md", lang)
	if err := os.WriteFile(filepath.Join(bundleDir, filename), []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing bundle %s: %w", bundleDir, err)
	}
	return bundleDir, nil
}

// buildBundleMarkdown renders the Hugo page: TOML frontmatter with the membox
// title/date, then the source body with any YAML frontmatter and a duplicated
// leading H1 removed (the theme renders the frontmatter title itself).
func buildBundleMarkdown(title string, date time.Time, body []byte) string {
	text := strings.TrimPrefix(string(body), "\uFEFF")
	text = stripYAMLFrontmatter(text)
	text = stripLeadingH1(text, title)
	var builder strings.Builder
	builder.WriteString("+++\n")
	builder.WriteString("title = ")
	builder.WriteString(tomlString(title))
	builder.WriteString("\n")
	builder.WriteString("date = ")
	builder.WriteString(tomlString(date.Format(time.RFC3339)))
	builder.WriteString("\n+++\n\n")
	builder.WriteString(strings.TrimLeft(text, "\n"))
	return builder.String()
}

// stripYAMLFrontmatter removes a leading --- ... --- block when present.
func stripYAMLFrontmatter(text string) string {
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return text
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return text
	}
	after := rest[end+4:]
	if after == "" || strings.HasPrefix(after, "\n") || strings.HasPrefix(after, "\r") {
		return strings.TrimLeft(after, "\r\n")
	}
	return text
}

// stripLeadingH1 removes a leading "# ..." heading when it matches the title,
// avoiding a duplicate visible heading under the theme's rendered title.
func stripLeadingH1(text, title string) string {
	trimmed := strings.TrimLeft(text, "\n")
	if !strings.HasPrefix(trimmed, "# ") {
		return text
	}
	line, rest, _ := strings.Cut(trimmed, "\n")
	heading := strings.TrimSpace(strings.TrimPrefix(line, "# "))
	if !strings.EqualFold(heading, strings.TrimSpace(title)) {
		return text
	}
	return rest
}

// tomlString quotes a string for TOML basic-string syntax.
func tomlString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			builder.WriteString(`\\`)
		case '"':
			builder.WriteString(`\"`)
		case '\n':
			builder.WriteString(`\n`)
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

// documentDate prefers the source creation date, falling back to mtime and
// then to the index time so the frontmatter date is always sensible.
func documentDate(document *catalog.Document) time.Time {
	if !document.Index.SourceCreatedAt.IsZero() {
		return document.Index.SourceCreatedAt
	}
	if document.Index.MTime > 0 {
		return time.Unix(0, document.Index.MTime)
	}
	if !document.Index.IndexedAt.IsZero() {
		return document.Index.IndexedAt
	}
	return time.Now()
}
