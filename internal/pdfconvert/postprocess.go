package pdfconvert

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var (
	chineseChapterPattern = regexp.MustCompile(`^第\s*([0-9０-９一二三四五六七八九十百千〇零两]+)\s*章(?:\s*[:：]?\s*)(.*)$`)
	englishChapterPattern = regexp.MustCompile(`(?i)^chapter\s+([0-9ivxlcdm]+)\b(?:\s*[:：.-]?\s*)(.*)$`)
	ancillaryPattern      = regexp.MustCompile(`(?i)^(引言|前言|序言|序|导言|绪论|后记|结语|附录|preface|introduction|epilogue|afterword|appendix)(?:\s*[:：.-]?\s*.*)?$`)
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
	start      int
	bodyStart  int
	title      string
	chapterKey string
	ancillary  string
	chapter    bool
}

// PostprocessMarkdown turns a long converted book into an index and stable
// chapter documents. It is a pure transformation: publishing and catalog
// links remain responsibilities of the workflow and host workspace.
func PostprocessMarkdown(markdown, baseFilename string) PostprocessResult {
	markdown = normalizeHTMLTables(markdown)
	boundaries, chapterCount := markdownSections(markdown)
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
	return boundaries, chapterCount
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
