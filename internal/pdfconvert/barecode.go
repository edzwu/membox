package pdfconvert

import (
	"regexp"
	"strings"
)

// normalizeBareCodeBlocks wraps converter-produced bare code lines in fenced
// code blocks.
//
// MinerU (and other PDF→Markdown pipelines) often emit code as loose lines
// separated by blank lines instead of a fenced block — each statement becomes
// its own paragraph, and inline "<int>"/"<mutex>" angle brackets get parsed as
// HTML tags by markdown renderers that disable raw HTML, so the code looks
// broken in Miru. Examples seen in Effective Modern C++ Chapter 4:
//
//	std::atomic<int> ai(0); // initialize ai to 0
//
//	ai = 10;
//
//	// atomically set ai to 10
//
// This pass groups consecutive code-looking lines (allowing blank lines and
// standalone comment lines between them) into a ```cpp fence. It is line
// oriented on purpose: a goldmark AST pass cannot help, because the bare code
// is exactly what the parser mis-reads as prose paragraphs.
//
// Heuristics stay conservative to avoid swallowing normal prose:
//   - a line must start with a strong code prefix (std::, a type/keyword,
//     function call, assignment, "++", "{", "//", "#include", …)
//   - unless it is unconditional (comment markers, braces, preprocessor,
//     template, using-namespace), the code part before any inline "//"
//     comment must end in a code symbol (;, (, ), {, }, ::, =, ++, --)
//   - lines already inside ```/~~~ fences, headings, lists, quotes, tables
//     and thematic breaks are never touched
var (
	// Line starts that look like code. Strong prefix plus either an
	// unconditional marker or a code-symbol ending (after stripping an inline
	// "//" comment) decides. Kept single-line: Go regexp has no extended mode
	// that tolerates formatting whitespace inside the pattern.
	bareCodePrefixRe = regexp.MustCompile(`(?i)^(?:#\s*(?:include|define|ifn?def|endif|else|pragma)\b|std::[a-z_][a-z0-9_]*|using\s+namespace\s|template\s*<|(?:auto|constexpr|static|const|volatile|register|unsigned|signed|extern|inline|thread_local)\s+|(?:int|bool|char|double|float|long|short|void|size_t|string|wchar_t|struct|class|enum|union|namespace|typename)\b|[a-z_][a-z0-9_]*\s*(?:<[^>\n]*>)?\s*::|[a-z_][a-z0-9_]*\s*\(|[a-z_][a-z0-9_]*\s*=|\+\+|--)`)

	// Unconditional code starts: comment markers, braces, preprocessor
	// directives and template/using-namespace lines never need a code suffix.
	bareCodeUnconditionalRe = regexp.MustCompile(`^(?://|/\*|\*/|[{}]|\+\+|--|#\s*(?:include|define|ifn?def|endif|else|pragma)\b|template\s*<|using\s+namespace\s)`)

	// The code part (inline "//" comment stripped) must end in one of these
	// to be treated as code rather than a prose sentence ("volatile is the
	// way we tell compilers…" fails, "volatile int x;" passes).
	bareCodeMainEndRe = regexp.MustCompile(`(?:;|\(|\)|\{|\}|::|=|\+\+|--)$`)

	fenceOpenRe = regexp.MustCompile("^\\s*(?:```+|~~~+)")
	headingRe   = regexp.MustCompile(`^#{1,6}\s`)
	blockRe     = regexp.MustCompile("^\\s*(?:>|[-*+]\\s|\\d+[.)]\\s|\\|\\s|---+|~~~+|```+)")
)

// normalizeBareCodeBlocks returns markdown with bare code lines wrapped in
// fenced code blocks.
func normalizeBareCodeBlocks(markdown string) string {
	lines := strings.Split(markdown, "\n")
	inFence := false
	out := make([]string, 0, len(lines))
	i := 0
	for i < len(lines) {
		line := lines[i]
		if fenceOpenRe.MatchString(line) {
			inFence = !inFence
			out = append(out, line)
			i++
			continue
		}
		if inFence || !isBareCodeLine(line) {
			out = append(out, line)
			i++
			continue
		}

		// Collect a run of code lines. Blank lines stay in the block only when
		// the following line is still code, so two adjacent snippets separated
		// by one blank line merge, while prose paragraphs end the block.
		var buf []string
		j := i
	collect:
		for j < len(lines) {
			trim := strings.TrimSpace(lines[j])
			switch {
			case fenceOpenRe.MatchString(lines[j]) || headingRe.MatchString(trim):
				break collect
			case trim == "":
				if j+1 < len(lines) && isBareCodeLine(lines[j+1]) {
					buf = append(buf, "")
					j++
					continue
				}
				break collect
			case blockRe.MatchString(trim):
				break collect
			case isBareCodeLine(lines[j]):
				buf = append(buf, lines[j])
				j++
			default:
				break collect
			}
		}
		for len(buf) > 0 && strings.TrimSpace(buf[0]) == "" {
			buf = buf[1:]
		}
		for len(buf) > 0 && strings.TrimSpace(buf[len(buf)-1]) == "" {
			buf = buf[:len(buf)-1]
		}
		if len(buf) == 0 {
			out = append(out, line)
			i++
			continue
		}
		out = append(out, "```"+inferCodeLang(buf))
		out = append(out, buf...)
		out = append(out, "```")
		i = j
	}
	return strings.Join(out, "\n")
}

// isBareCodeLine reports whether line looks like converter-produced loose
// code. Strong prefix plus either an unconditional marker or a code-symbol
// ending (after stripping an inline "//" comment).
func isBareCodeLine(line string) bool {
	trim := strings.TrimSpace(line)
	if trim == "" {
		return false
	}
	// Converter artifacts sometimes splice a prose sentence onto a code line
	// after an inline comment ("auto fut = std::async(f); // … • It’s not
	// possible to predict…"). A bullet mid-line or an overly long line means
	// it is no longer clean code — leave it to the reader rather than locking
	// prose inside a fence.
	if strings.Contains(trim, " • ") || len(trim) > 150 {
		return false
	}
	// Unconditional markers (comment lines, braces, preprocessor, …) are code
	// by themselves; everything else needs the strong prefix AND a code-symbol
	// ending (after stripping an inline "//" comment).
	if bareCodeUnconditionalRe.MatchString(trim) {
		return true
	}
	if !bareCodePrefixRe.MatchString(trim) {
		return false
	}
	main := trim
	if idx := strings.Index(trim, " //"); idx >= 0 {
		main = strings.TrimSpace(trim[:idx])
	} else if idx := strings.Index(trim, "\t//"); idx >= 0 {
		main = strings.TrimSpace(trim[:idx])
	}
	return bareCodeMainEndRe.MatchString(main)
}

// inferCodeLang picks a fence language. The converter corpus is C++-heavy, so
// cpp is the default; obvious other-language markers flip it.
func inferCodeLang(block []string) string {
	joined := strings.Join(block, "\n")
	switch {
	case strings.Contains(joined, "#!/"):
		return "bash"
	case strings.Contains(joined, "def ") && strings.Contains(joined, ":"):
		return "python"
	case strings.Contains(joined, "module.exports") || strings.Contains(joined, "=>"):
		return "javascript"
	default:
		return "cpp"
	}
}
