package docrewrite

import (
	"strings"
	"unicode/utf8"
)

// Per-call body budgets (runes). Prompt overhead + model context leave little
// room for local qwen3:14b; mmd also caps completion prompts at 128KiB.
const (
	ChunkRunesLocal = 5_500
	ChunkRunesCloud = 20_000
	// Absolute ceiling for one model call body (defense in depth).
	ChunkRunesHardMax = 28_000

	// Continuity: how much of the previous rewritten tail is shown when
	// rewriting the next chunk (context only — not re-emitted).
	PrevTailRunesLocal = 350
	PrevTailRunesCloud = 600

	// Seam polish: window taken from each side of a join for a second pass.
	SeamSideRunesLocal = 280
	SeamSideRunesCloud = 450
)

// chunkPlan describes how a document is split for rewrite.
type chunkPlan struct {
	FrontMatter string   // raw YAML block including --- fences, or empty
	Parts       []string // body segments, each rewritten independently
}

func chunkBudget(local bool) int {
	if local {
		return ChunkRunesLocal
	}
	return ChunkRunesCloud
}

// splitForRewrite peels front matter, then packs body units under maxRunes.
//
// Split strategy:
//  1. ATX headings (# … ######) when the doc has an outline
//  2. Structural blocks (paragraphs / fenced code / tables / hr) when flat
//  3. Hard rune windows inside a single oversized block
//
// Front matter is never sent to the model; it is reattached unchanged.
func splitForRewrite(markdown string, maxRunes int) chunkPlan {
	if maxRunes <= 0 {
		maxRunes = ChunkRunesLocal
	}
	if maxRunes > ChunkRunesHardMax {
		maxRunes = ChunkRunesHardMax
	}
	md := strings.ReplaceAll(markdown, "\r\n", "\n")
	fm, body := peelFrontMatter(md)
	body = strings.TrimSpace(body)
	if body == "" {
		return chunkPlan{FrontMatter: fm}
	}
	if utf8.RuneCountInString(body) <= maxRunes {
		return chunkPlan{FrontMatter: fm, Parts: []string{body}}
	}
	units := splitBodyUnits(body)
	parts := packSections(units, maxRunes)
	return chunkPlan{FrontMatter: fm, Parts: parts}
}

func peelFrontMatter(md string) (front, body string) {
	trim := strings.TrimSpace(md)
	if !strings.HasPrefix(trim, "---") {
		return "", md
	}
	start := strings.Index(md, "---")
	if start < 0 {
		return "", md
	}
	rest := md[start+3:]
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		return "", md
	}
	endRel := strings.Index(rest, "\n---")
	if endRel < 0 {
		return "", md
	}
	closeStart := endRel + 1
	after := rest[closeStart+3:]
	if after != "" && !strings.HasPrefix(after, "\n") && !strings.HasPrefix(after, "\r\n") {
		return "", md
	}
	front = strings.TrimSpace(md[:start+3+closeStart+3]) + "\n"
	body = strings.TrimPrefix(after, "\n")
	body = strings.TrimPrefix(body, "\r\n")
	return front, body
}

// splitBodyUnits chooses heading cuts when available; otherwise structural blocks.
func splitBodyUnits(body string) []string {
	if hasATXHeading(body) {
		return splitHeadingSections(body)
	}
	return splitStructuralBlocks(body)
}

func hasATXHeading(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if isATXHeading(line) {
			return true
		}
	}
	return false
}

// splitHeadingSections returns segments that each start at an ATX heading
// (except possibly a lead-in before the first heading).
func splitHeadingSections(body string) []string {
	lines := strings.Split(body, "\n")
	var starts []int
	for i, line := range lines {
		if isATXHeading(line) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return []string{body}
	}
	var sections []string
	if starts[0] > 0 {
		lead := strings.TrimSpace(strings.Join(lines[:starts[0]], "\n"))
		if lead != "" {
			sections = append(sections, lead)
		}
	}
	for i, start := range starts {
		end := len(lines)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		sec := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if sec != "" {
			sections = append(sections, sec)
		}
	}
	return sections
}

func isATXHeading(line string) bool {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "#") {
		return false
	}
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return false
	}
	if n == len(s) {
		return true
	}
	return s[n] == ' ' || s[n] == '\t'
}

// splitStructuralBlocks yields rewrite units without headings: fenced code,
// tables, thematic breaks, and blank-line paragraphs.
func splitStructuralBlocks(body string) []string {
	lines := strings.Split(body, "\n")
	var blocks []string
	var buf []string
	inFence := false
	inTable := false

	flush := func() {
		if len(buf) == 0 {
			return
		}
		block := strings.TrimSpace(strings.Join(buf, "\n"))
		buf = nil
		inTable = false
		if block != "" {
			blocks = append(blocks, block)
		}
	}

	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			if !inFence {
				flush()
				inFence = true
				buf = append(buf, line)
				continue
			}
			buf = append(buf, line)
			inFence = false
			flush()
			continue
		}
		if inFence {
			buf = append(buf, line)
			continue
		}
		if isThematicBreak(trim) {
			flush()
			blocks = append(blocks, trim)
			continue
		}
		if isTableRow(trim) {
			if !inTable && len(buf) > 0 {
				flush()
			}
			inTable = true
			buf = append(buf, line)
			continue
		}
		if inTable {
			if trim == "" {
				flush()
				continue
			}
			flush()
		}
		if trim == "" {
			flush()
			continue
		}
		buf = append(buf, line)
	}
	flush()
	if len(blocks) == 0 {
		return []string{body}
	}
	return blocks
}

func isThematicBreak(trim string) bool {
	if trim == "" {
		return false
	}
	for _, ch := range []byte{'-', '*', '_'} {
		ok := true
		count := 0
		for i := 0; i < len(trim); i++ {
			c := trim[i]
			if c == ' ' || c == '\t' {
				continue
			}
			if c != ch {
				ok = false
				break
			}
			count++
		}
		if ok && count >= 3 {
			return true
		}
	}
	return false
}

func isTableRow(trim string) bool {
	if trim == "" || !strings.Contains(trim, "|") {
		return false
	}
	if strings.ContainsAny(trim, "-:") && strings.Count(trim, "-") >= 3 {
		return true
	}
	return strings.HasPrefix(trim, "|") || strings.HasSuffix(trim, "|") || strings.Count(trim, "|") >= 2
}

func packSections(sections []string, maxRunes int) []string {
	if len(sections) == 0 {
		return nil
	}
	var parts []string
	var buf strings.Builder
	bufRunes := 0
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		parts = append(parts, strings.TrimSpace(buf.String()))
		buf.Reset()
		bufRunes = 0
	}
	for _, sec := range sections {
		sec = strings.TrimSpace(sec)
		if sec == "" {
			continue
		}
		n := utf8.RuneCountInString(sec)
		if n > maxRunes {
			flush()
			parts = append(parts, splitOversizedSection(sec, maxRunes)...)
			continue
		}
		need := n
		if bufRunes > 0 {
			need += 2
		}
		if bufRunes > 0 && bufRunes+need > maxRunes {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
			bufRunes += 2
		}
		buf.WriteString(sec)
		bufRunes += n
	}
	flush()
	return parts
}

func splitOversizedSection(sec string, maxRunes int) []string {
	// Even inside one "section", prefer structural re-split (e.g. huge lead-in).
	if hasATXHeading(sec) {
		// shouldn't recurse forever: heading sections already atomic by heading
	} else {
		blocks := splitStructuralBlocks(sec)
		if len(blocks) > 1 {
			return packSections(blocks, maxRunes)
		}
	}
	paras := splitParagraphs(sec)
	if len(paras) > 1 {
		return packSections(paras, maxRunes)
	}
	return splitByRuneHard(sec, maxRunes)
}

func splitParagraphs(s string) []string {
	return splitStructuralBlocks(s)
}

func splitByRuneHard(s string, maxRunes int) []string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return []string{s}
	}
	var parts []string
	for len(runes) > 0 {
		n := maxRunes
		if n > len(runes) {
			n = len(runes)
		}
		if n < len(runes) {
			window := runes[:n]
			if i := lastIndexRune(window, '\n'); i > n/2 {
				n = i + 1
			}
		}
		part := strings.TrimSpace(string(runes[:n]))
		if part != "" {
			parts = append(parts, part)
		}
		runes = runes[n:]
	}
	return parts
}

func lastIndexRune(runes []rune, r rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

func continuityBudget(local bool) (prevTail, seamSide int) {
	if local {
		return PrevTailRunesLocal, SeamSideRunesLocal
	}
	return PrevTailRunesCloud, SeamSideRunesCloud
}

func tailRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[len(r)-n:])
}

func headRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// applySeamReplacement swaps the boundary windows of left/right with polished.
// leftTail/rightHead are the exact windows that were sent to the model;
// polished replaces leftTail+"\n\n"+rightHead inside left+"\n\n"+right.
func applySeamReplacement(left, right, leftTail, rightHead, polished string) (string, string, bool) {
	polished = strings.TrimSpace(polished)
	if polished == "" || leftTail == "" || rightHead == "" {
		return left, right, false
	}
	l := strings.TrimSpace(left)
	r := strings.TrimSpace(right)
	if !strings.HasSuffix(l, leftTail) || !strings.HasPrefix(r, rightHead) {
		// Fallback: still try if only whitespace drifted.
		if !strings.HasSuffix(l, strings.TrimSpace(leftTail)) || !strings.HasPrefix(r, strings.TrimSpace(rightHead)) {
			return left, right, false
		}
		leftTail = strings.TrimSpace(leftTail)
		rightHead = strings.TrimSpace(rightHead)
	}
	// Prefer splitting polished on a blank line near the middle.
	mid := splitSeamPolished(polished, leftTail, rightHead)
	if mid[0] == "" && mid[1] == "" {
		return left, right, false
	}
	newLeft := strings.TrimSuffix(l, leftTail) + mid[0]
	newRight := mid[1] + strings.TrimPrefix(r, rightHead)
	return strings.TrimSpace(newLeft), strings.TrimSpace(newRight), true
}

func splitSeamPolished(polished, leftTail, rightHead string) [2]string {
	// If model kept a clear \n\n break, use the break closest to proportional middle.
	if i := strings.Index(polished, "\n\n"); i >= 0 {
		// pick break nearest to left-weight fraction
		leftW := utf8.RuneCountInString(leftTail)
		totalW := leftW + utf8.RuneCountInString(rightHead)
		if totalW == 0 {
			totalW = 1
		}
		target := len(polished) * leftW / totalW
		best, bestDist := i, absInt(i-target)
		for j := i + 2; j < len(polished)-1; j++ {
			if polished[j] == '\n' && polished[j+1] == '\n' {
				if d := absInt(j - target); d < bestDist {
					best, bestDist = j, d
				}
			}
		}
		return [2]string{
			strings.TrimSpace(polished[:best]),
			strings.TrimSpace(polished[best+2:]),
		}
	}
	// No blank line: cut by rune proportion.
	runess := []rune(polished)
	leftW := utf8.RuneCountInString(leftTail)
	totalW := leftW + utf8.RuneCountInString(rightHead)
	if totalW == 0 {
		totalW = 1
	}
	cut := len(runess) * leftW / totalW
	if cut < 1 {
		cut = 1
	}
	if cut >= len(runess) {
		cut = len(runess) - 1
	}
	if cut < 1 {
		return [2]string{polished, ""}
	}
	return [2]string{
		strings.TrimSpace(string(runess[:cut])),
		strings.TrimSpace(string(runess[cut:])),
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func joinRewriteParts(frontMatter string, parts []string) string {
	var b strings.Builder
	if fm := strings.TrimSpace(frontMatter); fm != "" {
		b.WriteString(fm)
		if !strings.HasSuffix(fm, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	first := true
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		first = false
		b.WriteString(p)
	}
	if b.Len() == 0 {
		return ""
	}
	out := b.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}
