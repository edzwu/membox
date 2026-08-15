package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"membox/internal/domain/catalog"
)

var (
	conversionChapterRE = regexp.MustCompile(`(?i)-chapter-(\d+)\.md$`)
	conversionPartRE    = regexp.MustCompile(`(?i)-part-([a-z]+)\.md$`)
	tocLinkRE           = regexp.MustCompile(`\[[^\]]*\]\(([^)]+\.md)\)`)
	hex32RE             = regexp.MustCompile(`(?i)^[0-9a-f]{32}`)
)

type conversionSeriesItem struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Title    string `json:"title"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // index | chapter | part | other
}

type conversionSeriesResponse struct {
	Kind     string                `json:"kind"` // pdf-conversion | none
	Index    *conversionSeriesItem `json:"index,omitempty"`
	Current  *conversionSeriesItem `json:"current,omitempty"`
	Prev     *conversionSeriesItem `json:"prev,omitempty"`
	Next     *conversionSeriesItem `json:"next,omitempty"`
	Position int                   `json:"position,omitempty"` // 1-based among non-index items; 0 on index
	Total    int                   `json:"total,omitempty"`    // non-index count
	Items    []conversionSeriesItem `json:"items,omitempty"`
}

func (s *Server) handleConversionSeries(writer http.ResponseWriter, request *http.Request, selector string) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.TrimSpace(selector) == "" {
		http.Error(writer, "missing document selector", http.StatusBadRequest)
		return
	}
	series, err := s.buildConversionSeries(request.Context(), selector)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(writer).Encode(series)
}

func (s *Server) buildConversionSeries(ctx context.Context, selector string) (conversionSeriesResponse, error) {
	document, _, err := s.service.ResolveDocument(ctx, selector)
	if err != nil {
		return conversionSeriesResponse{}, err
	}
	filename := filepath.Base(document.Location.RelativePath)
	identity, ok := conversionIdentity(filename)
	if !ok {
		return conversionSeriesResponse{Kind: "none"}, nil
	}
	// statusFilter on ListDocuments is read_status, not location status — leave
	// it empty and filter active conversion siblings ourselves.
	records, err := s.service.ListDocuments(ctx, 1000, false, "")
	if err != nil {
		return conversionSeriesResponse{}, err
	}
	var siblings []conversionSeriesItem
	for _, record := range records {
		if record.Document == nil || record.Document.Status != catalog.DocumentActive {
			continue
		}
		// Keep series inside the same managed path as the focus document.
		if record.Document.Location.PathID != document.Location.PathID {
			continue
		}
		base := filepath.Base(record.Document.Location.RelativePath)
		id, matched := conversionIdentity(base)
		if !matched || id != identity {
			continue
		}
		siblings = append(siblings, seriesItemFromDocument(record.Document))
	}
	if len(siblings) == 0 {
		return conversionSeriesResponse{Kind: "none"}, nil
	}

	// Prefer reading order from the index TOC when available.
	var indexBody string
	for _, item := range siblings {
		if item.Kind == "index" {
			if body, readErr := s.service.ReadDocument(ctx, item.ID); readErr == nil {
				indexBody = string(body)
			}
			break
		}
	}
	order := tocFilenameOrder(indexBody)
	sort.SliceStable(siblings, func(i, j int) bool {
		return seriesLess(siblings[i], siblings[j], order)
	})

	var (
		indexItem *conversionSeriesItem
		current   *conversionSeriesItem
		currentIdx = -1
		sections  []conversionSeriesItem
	)
	focusID := string(document.ID)
	for i := range siblings {
		item := siblings[i]
		if item.Kind == "index" && indexItem == nil {
			copyItem := item
			indexItem = &copyItem
		}
		if item.ID == focusID {
			copyItem := item
			current = &copyItem
			currentIdx = i
		}
		if item.Kind != "index" {
			sections = append(sections, item)
		}
	}

	response := conversionSeriesResponse{
		Kind:  "pdf-conversion",
		Index: indexItem,
		Items: siblings,
	}
	if current == nil {
		// Focus matched the identity filter but vanished from the list — still none.
		return conversionSeriesResponse{Kind: "none"}, nil
	}
	response.Current = current
	if current.Kind == "index" {
		response.Position = 0
		response.Total = len(sections)
		if len(sections) > 0 {
			next := sections[0]
			response.Next = &next
		}
		return response, nil
	}

	// Position among non-index sections for "3 / 40" chrome.
	sectionPos := 0
	for i, item := range sections {
		if item.ID == focusID {
			sectionPos = i + 1
			if i > 0 {
				prev := sections[i-1]
				response.Prev = &prev
			}
			if i+1 < len(sections) {
				next := sections[i+1]
				response.Next = &next
			}
			break
		}
	}
	response.Position = sectionPos
	response.Total = len(sections)
	_ = currentIdx
	return response, nil
}

func seriesItemFromDocument(document *catalog.Document) conversionSeriesItem {
	filename := filepath.Base(document.Location.RelativePath)
	title := strings.TrimSpace(document.Index.Title)
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	return conversionSeriesItem{
		ID:       string(document.ID),
		Path:     document.Location.RelativePath,
		Filename: filename,
		Title:    title,
		Label:    conversionDisplayLabel(filename, title),
		Kind:     conversionItemKind(filename),
	}
}

func conversionIdentity(filename string) (string, bool) {
	// Mirror TUI convertedPDFID / convertedTreeLabel: identity sits after the
	// last -pdf- marker (single or double dash both work via LastIndex).
	base := strings.ToLower(filepath.Base(strings.TrimSpace(filename)))
	if !strings.HasSuffix(base, ".md") {
		return "", false
	}
	stem := strings.TrimSuffix(base, ".md")
	if len(stem) == len("pdf-")+32 && strings.HasPrefix(stem, "pdf-") {
		return stem[len("pdf-"):], true
	}
	marker := strings.LastIndex(stem, "-pdf-")
	if marker < 0 {
		return "", false
	}
	rest := stem[marker+len("-pdf-"):]
	if !hex32RE.MatchString(rest) {
		return "", false
	}
	// rest is <32hex> or <32hex>-chapter-NNN / -part-xxx
	identity := rest[:32]
	suffix := rest[32:]
	if suffix != "" && !strings.HasPrefix(suffix, "-") {
		return "", false
	}
	return identity, true
}

func conversionItemKind(filename string) string {
	base := strings.ToLower(filepath.Base(filename))
	if conversionChapterRE.MatchString(base) {
		return "chapter"
	}
	if conversionPartRE.MatchString(base) {
		return "part"
	}
	// index: ends with -pdf-<32hex>.md (no further suffix)
	stem := strings.TrimSuffix(base, ".md")
	if marker := strings.LastIndex(stem, "-pdf-"); marker >= 0 {
		rest := stem[marker+len("-pdf-"):]
		if len(rest) == 32 {
			return "index"
		}
	}
	if strings.HasPrefix(stem, "pdf-") && len(stem) == len("pdf-")+32 {
		return "index"
	}
	return "other"
}

func conversionDisplayLabel(filename, title string) string {
	// Keep in sync with frontend labels.js / TUI convertedTreeLabel.
	base := strings.ToLower(filepath.Base(strings.TrimSpace(filename)))
	if !strings.HasSuffix(base, ".md") {
		if label := strings.TrimSpace(title); label != "" {
			return label
		}
		return filepath.Base(filename)
	}
	stem := strings.TrimSuffix(base, ".md")
	marker := strings.LastIndex(stem, "-pdf-")
	if marker < 0 {
		if label := strings.TrimSpace(title); label != "" {
			return label
		}
		return strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	prefix := strings.TrimRight(stem[:marker], "-")
	rest := stem[marker+len("-pdf-"):]
	if len(rest) < 32 {
		if label := strings.TrimSpace(title); label != "" {
			return label
		}
		return strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	rest = rest[32:]
	if rest == "" {
		return prefix
	}
	if m := conversionChapterRE.FindStringSubmatch(strings.ToLower(filepath.Base(filename))); m != nil {
		n, _ := strconv.Atoi(m[1])
		return prefix + " ch." + strconv.Itoa(n)
	}
	if m := conversionPartRE.FindStringSubmatch(strings.ToLower(filepath.Base(filename))); m != nil {
		name := m[1]
		switch name {
		case "introduction":
			name = "intro"
		}
		return prefix + " " + name
	}
	return prefix + " " + strings.TrimPrefix(rest, "-")
}

func tocFilenameOrder(indexMarkdown string) map[string]int {
	order := map[string]int{}
	if strings.TrimSpace(indexMarkdown) == "" {
		return order
	}
	rank := 0
	for _, match := range tocLinkRE.FindAllStringSubmatch(indexMarkdown, -1) {
		target := path.Base(strings.TrimSpace(match[1]))
		if target == "" || strings.Contains(target, "://") {
			continue
		}
		key := strings.ToLower(target)
		if _, exists := order[key]; exists {
			continue
		}
		order[key] = rank
		rank++
	}
	return order
}

func seriesLess(a, b conversionSeriesItem, tocOrder map[string]int) bool {
	// Index always first.
	if a.Kind == "index" && b.Kind != "index" {
		return true
	}
	if b.Kind == "index" && a.Kind != "index" {
		return false
	}
	aName := strings.ToLower(a.Filename)
	bName := strings.ToLower(b.Filename)
	aRank, aInTOC := tocOrder[aName]
	bRank, bInTOC := tocOrder[bName]
	if aInTOC && bInTOC {
		return aRank < bRank
	}
	if aInTOC != bInTOC {
		return aInTOC
	}
	// Fallback: chapter number, then part name, then filename.
	aKey := seriesSortKey(a)
	bKey := seriesSortKey(b)
	if aKey != bKey {
		return aKey < bKey
	}
	return aName < bName
}

func seriesSortKey(item conversionSeriesItem) string {
	base := strings.ToLower(item.Filename)
	if m := conversionChapterRE.FindStringSubmatch(base); m != nil {
		n, _ := strconv.Atoi(m[1])
		return "1-" + strconv.FormatInt(int64(n)+10000, 10)
	}
	if m := conversionPartRE.FindStringSubmatch(base); m != nil {
		name := m[1]
		// introduction before chapters is common; afterword after.
		switch name {
		case "introduction", "preface", "prologue":
			return "0-" + name
		case "afterword", "epilogue", "appendix", "conclusion":
			return "2-" + name
		default:
			return "1-5-" + name
		}
	}
	if item.Kind == "index" {
		return " "
	}
	return "3-" + base
}
