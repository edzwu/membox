package pdfconvert

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"golang.org/x/text/unicode/norm"
)

var (
	chineseChapterPattern    = regexp.MustCompile(`^第\s*([0-9０-９一二三四五六七八九十百千〇零两]+)\s*章(?:\s*[:：]?\s*)(.*)$`)
	englishChapterPattern    = regexp.MustCompile(`(?i)^chapter\s+([0-9ivxlcdm]+)\b(?:\s*[:：.-]?\s*)(.*)$`)
	ancillaryPattern         = regexp.MustCompile(`(?i)^(引言|前言|序言|序|导言|绪论|后记|结语|附录|preface|introduction|epilogue|afterword|appendix)(?:\s*[:：.-]?\s*.*)?$`)
	tocChapterPattern        = regexp.MustCompile(`(?i)^(?:第\s*)?([0-9０-９一二三四五六七八九十百千〇零两]+)\s*章\s*[、,:：.．-]?\s*(.*?)\s+(?:[ivxlcdm]+|\d+(?:\.\d+)*)\s*$`)
	englishTOCChapterPattern = regexp.MustCompile(`(?i)^([a-z]+|[0-9]+|[ivxlcdm]+)\s*[:：.)-]\s*(.*?)\s+(?:[ivxlcdm]+|\d+(?:\.\d+)*)\s*$`)
	tocEntryPattern          = regexp.MustCompile(`(?i)^(.*?)\s+(?:[ivxlcdm]+|\d+(?:\.\d+)*)\s*$`)
)

type ChapterMarkdown struct {
	Title    string
	Filename string
	Markdown string
}

type PostprocessResult struct {
	IndexMarkdown string
	Chapters      []ChapterMarkdown
}

type sectionBoundary struct {
	start           int
	bodyStart       int
	title           string
	chapterKey      string
	ancillary       string
	chapter         bool
	preserveHeading string
}

type tocChapter struct {
	number  string
	title   string
	entries []string
	english bool
}

type topLevelHeading struct {
	start     int
	bodyStart int
	title     string
}

// PostprocessMarkdown turns a long converted book into an index and stable
// chapter documents. It is a pure transformation: publishing and catalog
// links remain responsibilities of the workflow and host workspace.
func PostprocessMarkdown(markdown, baseFilename string) PostprocessResult {
	return PostprocessMarkdownWithPlanner(context.Background(), markdown, baseFilename, nil)
}

// PostprocessMarkdownWithPlanner first tries the deterministic AST/TOC split.
// When that finds fewer than two chapters and a StructurePlanner is available,
// it asks the local LLM for proposed boundaries, verifies every anchor against
// real document lines, and only then splits. Any failure keeps the document
// whole; the LLM can never rewrite or drop content silently.
func PostprocessMarkdownWithPlanner(ctx context.Context, markdown, baseFilename string, planner StructurePlanner) PostprocessResult {
	markdown = normalizeHTMLTables(markdown)
	boundaries, chapterCount := markdownSections(markdown)
	if chapterCount < 2 && planner != nil {
		if planned := llmPlannedSections(ctx, markdown, planner); len(planned) >= 2 {
			boundaries, chapterCount = planned, len(planned)
		}
	}
	if chapterCount < 2 {
		return PostprocessResult{IndexMarkdown: markdown}
	}

	bookTitle := markdownTitle(markdown)
	if bookTitle == "" {
		bookTitle = strings.TrimSuffix(baseFilename, ".md")
	}
	stem := strings.TrimSuffix(baseFilename, ".md")
	usedNames := map[string]bool{}
	chapters := make([]ChapterMarkdown, 0, len(boundaries))
	for index, boundary := range boundaries {
		end := len(markdown)
		if index+1 < len(boundaries) {
			end = boundaries[index+1].start
		}
		key := boundary.chapterKey
		if !boundary.chapter {
			key = boundary.ancillary
		}
		if key == "" {
			key = fmt.Sprintf("part-%02d", index+1)
		}
		filename := stem + "-" + key + ".md"
		if usedNames[filename] {
			filename = fmt.Sprintf("%s-%s-%02d.md", stem, key, index+1)
		}
		usedNames[filename] = true
		body := strings.TrimSpace(markdown[boundary.bodyStart:end])
		if boundary.preserveHeading != "" {
			preserved := "## " + boundary.preserveHeading
			if body != "" {
				body = preserved + "\n\n" + body
			} else {
				body = preserved
			}
		}
		chapterBody := "# " + boundary.title + "\n\n[← " + bookTitle + "](" + baseFilename + ")\n"
		if body != "" {
			chapterBody += "\n" + body + "\n"
		}
		chapters = append(chapters, ChapterMarkdown{Title: boundary.title, Filename: filename, Markdown: chapterBody})
	}

	var index strings.Builder
	fmt.Fprintf(&index, "# %s\n\n## 目录\n\n", bookTitle)
	for i, chapter := range chapters {
		fmt.Fprintf(&index, "%d. [%s](%s)\n", i+1, chapter.Title, chapter.Filename)
	}
	return PostprocessResult{IndexMarkdown: index.String(), Chapters: chapters}
}

func llmPlannedSections(ctx context.Context, markdown string, planner StructurePlanner) []sectionBoundary {
	sketch := BuildStructureSketch(markdown)
	if len(sketch) < 200 {
		return nil
	}
	plans, err := planner.PlanChapters(ctx, sketch)
	if err != nil {
		return nil
	}
	return locatePlannedChapters(markdown, plans)
}

// locatePlannedChapters maps LLM-proposed anchors onto real document lines.
// Table rows (TOC entries) are rejected, matches must be strictly ordered,
// and at least two anchors must locate before any split happens.
func locatePlannedChapters(markdown string, plans []ChapterPlan) []sectionBoundary {
	type mdLine struct {
		start, end int
		text       string
	}
	raw := strings.Split(markdown, "\n")
	lines := make([]mdLine, 0, len(raw))
	offset := 0
	for _, text := range raw {
		lines = append(lines, mdLine{start: offset, end: offset + len(text) + 1, text: text})
		offset += len(text) + 1
	}

	cursor := 0
	boundaries := make([]sectionBoundary, 0, len(plans))
	for _, plan := range plans {
		anchor := normalizeTOCTitle(plan.Anchor)
		if len(anchor) < 6 {
			continue
		}
		for _, line := range lines {
			if line.start < cursor {
				continue
			}
			trimmed := strings.TrimSpace(line.text)
			if trimmed == "" || strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "!") {
				continue
			}
			text, heading := trimmed, false
			if match := atxHeadingText(trimmed); match != "" {
				text, heading = match, true
			}
			normalized := normalizeTOCTitle(text)
			if normalized == "" || !anchorMatches(anchor, normalized) {
				continue
			}
			start, bodyStart := line.start, line.start
			if heading {
				bodyStart = line.end
			} else if line.end < len(markdown) {
				// A Setext heading keeps its title line out of the body too.
				next := strings.TrimSpace(markdown[line.end:min(line.end+128, len(markdown))])
				if nl := strings.IndexByte(next, '\n'); nl >= 0 {
					next = next[:nl]
				}
				if len(next) >= 3 && (strings.Trim(next, "=") == "" || strings.Trim(next, "-") == "") {
					bodyStart = line.end
				}
			}
			boundaries = append(boundaries, sectionBoundary{
				start: start, bodyStart: bodyStart, title: plan.Title, chapter: true,
			})
			cursor = line.end
			break
		}
	}
	if len(boundaries) < 2 {
		return nil
	}

	// Never silently drop substantial front matter: keep it as its own chapter.
	if prefix := strings.TrimSpace(markdown[:boundaries[0].start]); len(prefix) > 800 {
		boundaries = append([]sectionBoundary{{start: 0, bodyStart: 0, title: "Front Matter", chapter: true}}, boundaries...)
	}
	for index := range boundaries {
		boundaries[index].chapterKey = fmt.Sprintf("chapter-%03d", index)
	}
	return boundaries
}

func atxHeadingText(line string) string {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return ""
	}
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[level:]), "#"))
}

// anchorMatches compares NFKC-normalized, alphanumeric-only forms. Exact or
// prefix equality is preferred; containment requires a long anchor so short
// generic strings cannot pin a boundary.
func anchorMatches(anchor, line string) bool {
	if line == anchor {
		return true
	}
	prefix := anchor
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	if strings.HasPrefix(line, prefix) {
		return true
	}
	return len(anchor) >= 16 && strings.Contains(line, anchor)
}

func markdownSections(markdown string) ([]sectionBoundary, int) {
	source := []byte(markdown)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	boundaries := make([]sectionBoundary, 0)
	chapterCount := 0
	ancillaryCounts := map[string]int{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != ast.KindHeading || node.Parent() != document {
			return ast.WalkContinue, nil
		}
		heading := node.(*ast.Heading)
		title := strings.TrimSpace(string(heading.Text(source)))
		start, bodyStart := headingOffsets(source, heading)
		if match := chineseChapterPattern.FindStringSubmatch(title); len(match) != 0 {
			chapterCount++
			key := chapterFilenameKey(match[1], chapterCount)
			boundaries = append(boundaries, sectionBoundary{start: start, bodyStart: bodyStart, title: title, chapterKey: key, chapter: true})
		} else if match := englishChapterPattern.FindStringSubmatch(title); len(match) != 0 {
			chapterCount++
			key := chapterFilenameKey(match[1], chapterCount)
			boundaries = append(boundaries, sectionBoundary{start: start, bodyStart: bodyStart, title: title, chapterKey: key, chapter: true})
		} else if heading.Level <= 3 && ancillaryPattern.MatchString(title) {
			kind := ancillaryKind(title)
			ancillaryCounts[kind]++
			key := "part-" + kind
			if ancillaryCounts[kind] > 1 {
				key += fmt.Sprintf("-%02d", ancillaryCounts[kind])
			}
			boundaries = append(boundaries, sectionBoundary{start: start, bodyStart: bodyStart, title: title, ancillary: key})
		}
		return ast.WalkContinue, nil
	})
	if chapterCount >= 2 {
		return boundaries, chapterCount
	}
	if tocBoundaries := tocMarkdownSections(document, source); len(tocBoundaries) >= 2 {
		return tocBoundaries, len(tocBoundaries)
	}
	return boundaries, chapterCount
}

func tocMarkdownSections(document ast.Node, source []byte) []sectionBoundary {
	var tocHeading ast.Node
	for node := document.FirstChild(); node != nil; node = node.NextSibling() {
		if node.Kind() != ast.KindHeading {
			continue
		}
		if isTOCHeading(string(node.(*ast.Heading).Text(source))) {
			tocHeading = node
			break
		}
	}
	if tocHeading == nil {
		return nil
	}

	chapters, lastTOCHeading := parseTOCChapters(tocHeading, source)
	if len(chapters) < 2 {
		return nil
	}
	headings := bodyHeadingsAfter(lastTOCHeading, source)
	if len(headings) < 2 {
		return nil
	}

	// First pass: exact chapter-title matches establish trustworthy windows.
	matches := make([]int, len(chapters))
	trusted := make([]bool, len(chapters))
	for index := range matches {
		matches[index] = -1
	}
	preferLastExact := false
	for _, heading := range headings {
		if normalizeTOCTitle(heading.title) == "chaptersummaries" {
			preferLastExact = true
			break
		}
	}
	cursor := 0
	for chapterIndex, chapter := range chapters {
		want := normalizeTOCTitle(chapter.title)
		found := -1
		for headingIndex := cursor; headingIndex < len(headings); headingIndex++ {
			if normalizeTOCTitle(headings[headingIndex].title) != want {
				continue
			}
			found = headingIndex
			if !preferLastExact {
				break
			}
		}
		if found >= 0 {
			matches[chapterIndex] = found
			trusted[chapterIndex] = true
			cursor = found + 1
		}
	}

	// A converter may drop the chapter heading but retain its first article.
	// Fill only inside windows bounded by exact neighboring chapters. For the
	// first TOC chapter, the first body heading is the only safe fallback.
	preserve := make([]bool, len(chapters))
	for chapterIndex, chapter := range chapters {
		if matches[chapterIndex] >= 0 {
			continue
		}
		lower := 0
		for previous := chapterIndex - 1; previous >= 0; previous-- {
			if matches[previous] >= 0 {
				lower = matches[previous] + 1
				break
			}
		}
		upper := len(headings)
		for next := chapterIndex + 1; next < len(chapters); next++ {
			if matches[next] >= 0 {
				upper = matches[next]
				break
			}
		}
		if lower >= upper {
			continue
		}
		if chapterIndex == 0 && lower == 0 {
			matches[chapterIndex], preserve[chapterIndex] = 0, true
			continue
		}
		entryTitles := make(map[string]bool, len(chapter.entries))
		for _, entry := range chapter.entries {
			entryTitles[normalizeTOCTitle(entry)] = true
		}
		for headingIndex := lower; headingIndex < upper; headingIndex++ {
			if entryTitles[normalizeTOCTitle(headings[headingIndex].title)] {
				matches[chapterIndex], preserve[chapterIndex], trusted[chapterIndex] = headingIndex, true, true
				break
			}
		}
	}

	trustedCount := 0
	for _, matched := range trusted {
		if matched {
			trustedCount++
		}
	}
	if trustedCount < 2 {
		return nil
	}

	boundaries := make([]sectionBoundary, 0, len(chapters))
	lastHeading := -1
	for chapterIndex, headingIndex := range matches {
		if headingIndex < 0 || headingIndex <= lastHeading {
			continue
		}
		chapter := chapters[chapterIndex]
		heading := headings[headingIndex]
		number, ok := parseChapterNumber(chapter.number)
		if !ok {
			continue
		}
		displayNumber := chapter.number
		if number == 0 {
			displayNumber = "零"
		}
		title := "第" + displayNumber + "章、" + chapter.title
		if chapter.english {
			title = "Chapter " + chapter.number + ": " + chapter.title
		}
		boundary := sectionBoundary{
			start: heading.start, bodyStart: heading.bodyStart,
			title: title, chapterKey: fmt.Sprintf("chapter-%03d", number), chapter: true,
		}
		if preserve[chapterIndex] {
			boundary.preserveHeading = heading.title
		}
		boundaries = append(boundaries, boundary)
		lastHeading = headingIndex
	}

	// Keep front/back matter that has an explicit semantic heading. This also
	// prevents a preface from being silently dropped before Chapter One and
	// keeps an epilogue out of the final numbered chapter.
	ancillaryCounts := map[string]int{}
	for _, heading := range headings {
		if !ancillaryPattern.MatchString(strings.TrimSpace(heading.title)) {
			continue
		}
		kind := ancillaryKind(heading.title)
		ancillaryCounts[kind]++
		key := "part-" + kind
		if ancillaryCounts[kind] > 1 {
			key += fmt.Sprintf("-%02d", ancillaryCounts[kind])
		}
		boundaries = append(boundaries, sectionBoundary{
			start: heading.start, bodyStart: heading.bodyStart, title: heading.title, ancillary: key,
		})
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].start < boundaries[j].start })
	return boundaries
}

func parseTOCChapters(firstTOCHeading ast.Node, source []byte) ([]tocChapter, ast.Node) {
	chapters := make([]tocChapter, 0)
	lastTOCHeading := firstTOCHeading
	for tocHeading := firstTOCHeading; tocHeading != nil; {
		lastTOCHeading = tocHeading
		var nextHeading ast.Node
		for node := tocHeading.NextSibling(); node != nil; node = node.NextSibling() {
			if node.Kind() == ast.KindHeading {
				nextHeading = node
				break
			}
			if node.Kind() != ast.KindParagraph {
				continue
			}
			lines := node.Lines()
			for index := 0; index < lines.Len(); index++ {
				segment := lines.At(index)
				line := strings.TrimSpace(string(segment.Value(source)))
				line = strings.TrimSpace(strings.TrimSuffix(line, "  "))
				if match := tocChapterPattern.FindStringSubmatch(line); len(match) != 0 {
					title := strings.TrimSpace(match[2])
					if title != "" {
						chapters = append(chapters, tocChapter{number: match[1], title: title})
					}
					continue
				}
				if match := englishTOCChapterPattern.FindStringSubmatch(line); len(match) != 0 {
					number, title := strings.TrimSpace(match[1]), strings.TrimSpace(match[2])
					if _, ok := parseChapterNumber(number); ok && title != "" {
						chapters = append(chapters, tocChapter{number: number, title: title, english: true})
						continue
					}
				}
				if len(chapters) == 0 {
					continue
				}
				if match := tocEntryPattern.FindStringSubmatch(line); len(match) != 0 {
					title := strings.TrimSpace(match[1])
					if title != "" {
						last := len(chapters) - 1
						chapters[last].entries = append(chapters[last].entries, title)
					}
				}
			}
		}
		if nextHeading == nil || !isTOCHeading(string(nextHeading.(*ast.Heading).Text(source))) {
			break
		}
		tocHeading = nextHeading
	}
	return chapters, lastTOCHeading
}

func bodyHeadingsAfter(tocHeading ast.Node, source []byte) []topLevelHeading {
	headings := make([]topLevelHeading, 0)
	for node := tocHeading.NextSibling(); node != nil; node = node.NextSibling() {
		if node.Kind() != ast.KindHeading {
			continue
		}
		heading := node.(*ast.Heading)
		start, bodyStart := headingOffsets(source, heading)
		headings = append(headings, topLevelHeading{
			start: start, bodyStart: bodyStart, title: strings.TrimSpace(string(heading.Text(source))),
		})
	}
	return headings
}

func isTOCHeading(value string) bool {
	switch normalizeTOCTitle(value) {
	case "tableofcontents", "contents", "目录":
		return true
	default:
		return false
	}
}

func normalizeTOCTitle(value string) string {
	value = norm.NFKC.String(strings.ToLower(strings.TrimSpace(value)))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
}

func headingOffsets(source []byte, heading *ast.Heading) (int, int) {
	if heading.Lines().Len() == 0 {
		return 0, 0
	}
	contentStart := heading.Lines().At(0).Start
	start := bytes.LastIndexByte(source[:contentStart], '\n') + 1
	lineEnd := bytes.IndexByte(source[start:], '\n')
	if lineEnd < 0 {
		return start, len(source)
	}
	lineEnd += start
	line := bytes.TrimLeft(source[start:lineEnd], " \t")
	if bytes.HasPrefix(line, []byte("#")) {
		return start, lineEnd + 1
	}
	// A Setext heading spans the title line and the following ===/--- line.
	underlineEnd := bytes.IndexByte(source[lineEnd+1:], '\n')
	if underlineEnd < 0 {
		return start, len(source)
	}
	return start, lineEnd + 1 + underlineEnd + 1
}

func markdownTitle(markdown string) string {
	source := []byte(markdown)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	title := ""
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if title != "" || !entering || node.Kind() != ast.KindHeading || node.Parent() != document {
			return ast.WalkContinue, nil
		}
		heading := node.(*ast.Heading)
		candidate := strings.TrimSpace(string(heading.Text(source)))
		if heading.Level == 1 && chineseChapterPattern.FindStringSubmatch(candidate) == nil && englishChapterPattern.FindStringSubmatch(candidate) == nil {
			title = candidate
		}
		return ast.WalkContinue, nil
	})
	return title
}

func chapterFilenameKey(number string, fallback int) string {
	if value, ok := parseChapterNumber(number); ok && value >= 0 {
		return fmt.Sprintf("chapter-%03d", value)
	}
	return fmt.Sprintf("chapter-%03d", fallback)
}

func parseChapterNumber(number string) (int, bool) {
	number = strings.Map(func(r rune) rune {
		if r >= '０' && r <= '９' {
			return '0' + (r - '０')
		}
		return r
	}, strings.TrimSpace(number))
	if value, err := strconv.Atoi(number); err == nil {
		return value, true
	}
	englishNumbers := map[string]int{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20,
	}
	if value, ok := englishNumbers[strings.ToLower(number)]; ok {
		return value, true
	}
	upper := strings.ToUpper(number)
	if strings.IndexFunc(upper, func(r rune) bool { return !strings.ContainsRune("IVXLCDM", r) }) == -1 {
		return parseRomanNumeral(upper), true
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, current, seen := 0, 0, false
	for _, r := range number {
		if value, ok := digits[r]; ok {
			current, seen = value, true
			continue
		}
		unit, ok := units[r]
		if !ok {
			return 0, false
		}
		seen = true
		if current == 0 {
			current = 1
		}
		total += current * unit
		current = 0
	}
	return total + current, seen
}

func parseRomanNumeral(number string) int {
	values := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total := 0
	for index := 0; index < len(number); index++ {
		value := values[number[index]]
		if index+1 < len(number) && value < values[number[index+1]] {
			total -= value
		} else {
			total += value
		}
	}
	return total
}

func ancillaryKind(title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.HasPrefix(lower, "引言"), strings.HasPrefix(lower, "前言"), strings.HasPrefix(lower, "序言"), lower == "序", strings.HasPrefix(lower, "导言"), strings.HasPrefix(lower, "绪论"), strings.HasPrefix(lower, "preface"), strings.HasPrefix(lower, "introduction"):
		return "introduction"
	case strings.HasPrefix(lower, "后记"), strings.HasPrefix(lower, "结语"), strings.HasPrefix(lower, "epilogue"), strings.HasPrefix(lower, "afterword"):
		return "afterword"
	default:
		return "appendix"
	}
}
