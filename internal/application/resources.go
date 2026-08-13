package application

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"membox/internal/application/port"
)

var (
	webURLPattern      = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	markdownURLPattern = regexp.MustCompile(`(?i)\[([^\]]+)\]\((https?://[^\s)]+)\)`)
)

var trackingQueryKeys = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true,
	"mc_cid": true, "mc_eid": true, "igshid": true, "ref_src": true,
	"spm": true, "si": true,
}

// ExtractedResource is a URL plus the inbox line that supplied its context.
type ExtractedResource struct {
	URL          string
	CanonicalURL string
	Title        string
	SourceLine   string
}

func trimResourceURL(value string) string {
	value = html.UnescapeString(strings.TrimSpace(value))
	value = strings.TrimRight(value, ".,;:!?")
	pairs := [][2]byte{{')', '('}, {']', '['}, {'}', '{'}}
	for changed := true; changed && value != ""; {
		changed = false
		for _, pair := range pairs {
			if value[len(value)-1] == pair[0] && strings.Count(value, string(pair[0])) > strings.Count(value, string(pair[1])) {
				value = value[:len(value)-1]
				changed = true
			}
		}
	}
	return value
}

// CanonicalizeResourceURL creates the stable deduplication key owned by membox.
func CanonicalizeResourceURL(value string) (string, error) {
	raw := trimResourceURL(value)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", fmt.Errorf("not an http(s) URL: %s", value)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	portNumber := parsed.Port()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if portNumber != "" && !((parsed.Scheme == "http" && portNumber == "80") || (parsed.Scheme == "https" && portNumber == "443")) {
		parsed.Host = net.JoinHostPort(strings.Trim(host, "[]"), portNumber)
	} else {
		parsed.Host = host
	}
	parsed.User = nil
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || trackingQueryKeys[lower] {
			query.Del(key)
		}
	}
	// url.Values.Encode is stable, but sort repeated values as well so query
	// parameter order never creates a second resource.
	for key := range query {
		sort.Strings(query[key])
	}
	parsed.RawQuery = query.Encode()
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String(), nil
}

// ExtractResourceURLs deterministically extracts and batch-deduplicates web URLs.
func ExtractResourceURLs(lines []string) []ExtractedResource {
	found := make(map[string]ExtractedResource)
	order := make([]string, 0)
	for _, line := range lines {
		titles := make(map[string]string)
		for _, match := range markdownURLPattern.FindAllStringSubmatch(line, -1) {
			canonical, err := CanonicalizeResourceURL(match[2])
			if err == nil {
				titles[canonical] = strings.TrimSpace(match[1])
			}
		}
		for _, match := range webURLPattern.FindAllString(line, -1) {
			raw := trimResourceURL(match)
			canonical, err := CanonicalizeResourceURL(raw)
			if err != nil {
				continue
			}
			item, exists := found[canonical]
			if !exists {
				item = ExtractedResource{URL: raw, CanonicalURL: canonical, SourceLine: line}
				order = append(order, canonical)
			}
			if item.Title == "" {
				item.Title = titles[canonical]
			}
			found[canonical] = item
		}
	}
	out := make([]ExtractedResource, 0, len(order))
	for _, canonical := range order {
		out = append(out, found[canonical])
	}
	return out
}

type ResourceIngestOptions struct {
	Lines            []string
	SourceDocumentID string
	SourceFile       string
	SourceCommit     string
}

// IngestResourceDocument reads an indexed Markdown document through Membox's
// catalog and ingests every URL it contains. A source_file must resolve inside
// a configured scan path; the API never reads arbitrary filesystem paths.
func (s *Service) IngestResourceDocument(ctx context.Context, sourceDocumentID, sourceFile, sourceCommit string) (ResourceIngestResult, error) {
	var documentID, absolutePath string
	if selector := strings.TrimSpace(sourceDocumentID); selector != "" {
		document, path, err := s.ResolveDocument(ctx, selector)
		if err != nil {
			return ResourceIngestResult{}, err
		}
		documentID, absolutePath = string(document.ID), path
	} else {
		candidate, err := filepath.Abs(strings.TrimSpace(sourceFile))
		if err != nil || strings.TrimSpace(sourceFile) == "" {
			return ResourceIngestResult{}, fmt.Errorf("source_document_id or indexed source_file is required")
		}
		candidate = filepath.Clean(candidate)
		if resolved, resolveErr := filepath.EvalSymlinks(candidate); resolveErr == nil {
			candidate = resolved
		}
		paths, err := s.store.ListPaths(ctx, false)
		if err != nil {
			return ResourceIngestResult{}, err
		}
		for _, summary := range paths {
			relative, relErr := filepath.Rel(summary.Path.Root, candidate)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			documents, listErr := s.store.DocumentsForPath(ctx, summary.Path.ID)
			if listErr != nil {
				return ResourceIngestResult{}, listErr
			}
			for _, document := range documents {
				if filepath.Clean(filepath.FromSlash(document.Location.RelativePath)) == filepath.Clean(relative) {
					documentID, absolutePath = string(document.ID), candidate
					break
				}
			}
			if documentID != "" {
				break
			}
		}
		if documentID == "" {
			return ResourceIngestResult{}, fmt.Errorf("source_file is not an indexed membox document: %s", candidate)
		}
	}
	body, err := s.reader.Read(ctx, absolutePath)
	if err != nil {
		return ResourceIngestResult{}, err
	}
	return s.IngestResourceLines(ctx, ResourceIngestOptions{
		Lines: strings.Split(string(body), "\n"), SourceDocumentID: documentID,
		SourceFile: absolutePath, SourceCommit: sourceCommit,
	})
}

type ResourceIngestResult struct {
	Found    int
	Inserted []port.ResourceRecord
	Existing []port.ResourceRecord
}

func (s *Service) IngestResourceLines(ctx context.Context, opts ResourceIngestOptions) (ResourceIngestResult, error) {
	release, err := s.beginMutation()
	if err != nil {
		return ResourceIngestResult{}, err
	}
	defer release()

	extracted := ExtractResourceURLs(opts.Lines)
	inputs := make([]port.ResourceInsert, 0, len(extracted))
	for _, item := range extracted {
		id, err := s.ids.NewResourceID()
		if err != nil {
			return ResourceIngestResult{}, err
		}
		inputs = append(inputs, port.ResourceInsert{
			ID: id, URL: item.URL, CanonicalURL: item.CanonicalURL, Title: item.Title,
			SourceDocumentID: strings.TrimSpace(opts.SourceDocumentID),
			SourceFile:       strings.TrimSpace(opts.SourceFile), SourceLine: item.SourceLine,
			SourceCommit: strings.TrimSpace(opts.SourceCommit), CreatedAt: s.clock.Now(),
		})
	}
	results, err := s.store.IngestResources(ctx, inputs)
	if err != nil {
		return ResourceIngestResult{}, err
	}
	out := ResourceIngestResult{Found: len(extracted)}
	for _, result := range results {
		if result.Inserted {
			out.Inserted = append(out.Inserted, result.Resource)
		} else {
			out.Existing = append(out.Existing, result.Resource)
		}
	}
	return out, nil
}

func (s *Service) AssessResources(ctx context.Context, assessments []port.ResourceAssessment) ([]port.ResourceRecord, error) {
	for i := range assessments {
		assessments[i].ID = strings.TrimSpace(assessments[i].ID)
		assessments[i].Priority = strings.ToUpper(strings.TrimSpace(assessments[i].Priority))
		assessments[i].Reason = strings.TrimSpace(assessments[i].Reason)
		if assessments[i].ID == "" {
			return nil, fmt.Errorf("resource id is required")
		}
		if assessments[i].Priority != "H" && assessments[i].Priority != "M" && assessments[i].Priority != "L" {
			return nil, fmt.Errorf("invalid resource priority: %s", assessments[i].Priority)
		}
		if assessments[i].Score < 0 || assessments[i].Score > 1 {
			return nil, fmt.Errorf("resource score must be between 0 and 1: %v", assessments[i].Score)
		}
		if assessments[i].Reason == "" {
			return nil, fmt.Errorf("resource assessment reason is required")
		}
		assessments[i].UpdatedAt = s.clock.Now()
	}
	release, err := s.beginMutation()
	if err != nil {
		return nil, err
	}
	defer release()
	return s.store.AssessResources(ctx, assessments)
}

func normalizeResourceLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 5000 {
		return 5000
	}
	return limit
}

func (s *Service) ListResources(ctx context.Context, limit int) ([]port.ResourceRecord, error) {
	return s.store.ListResources(ctx, normalizeResourceLimit(limit))
}

func (s *Service) ListResourcesBySource(ctx context.Context, sourceDocumentID, sourceFile string, limit int) ([]port.ResourceRecord, error) {
	documentID := strings.TrimSpace(sourceDocumentID)
	file := strings.TrimSpace(sourceFile)
	if file != "" {
		if absolute, err := filepath.Abs(file); err == nil {
			file = filepath.Clean(absolute)
		}
		if resolved, err := filepath.EvalSymlinks(file); err == nil {
			file = resolved
		}
	}
	if documentID == "" && file == "" {
		return nil, fmt.Errorf("source_document_id or source_file is required")
	}
	return s.store.ListResourcesBySource(ctx, documentID, file, normalizeResourceLimit(limit))
}

func (s *Service) ResourceScanState(ctx context.Context, source string) (int, error) {
	if strings.TrimSpace(source) == "" {
		return 0, fmt.Errorf("source is required")
	}
	return s.store.ResourceScanState(ctx, source)
}

func (s *Service) ResetResourceScan(ctx context.Context, source string) error {
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("source is required")
	}
	return s.store.SetResourceScanState(ctx, source, 0, true, s.clock.Now())
}

func (s *Service) AdvanceResourceScan(ctx context.Context, source string, wave int) error {
	if strings.TrimSpace(source) == "" || wave < 1 {
		return fmt.Errorf("source and positive wave are required")
	}
	return s.store.SetResourceScanState(ctx, source, wave, false, s.clock.Now())
}
