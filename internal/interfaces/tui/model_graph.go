package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"membox"
	"membox/internal/interfaces/host"
)

func (m Model) computeBoard() boardLayout {
	layout := boardLayout{cardSpans: map[string][2]int{}}

	var boardItems []item
	for _, candidate := range m.filtered {
		if candidate.document.Summary != "" {
			boardItems = append(boardItems, candidate)
		}
	}
	if len(boardItems) == 0 {
		return layout
	}

	const targetCardWidth = 32
	const gap = 2
	numCols := max(1, (m.width+gap)/(targetCardWidth+gap))
	cardWidth := (m.width - (numCols-1)*gap) / numCols

	type placedCard struct {
		docID string
		col   int
		row   int
		lines []string
	}
	colHeights := make([]int, numCols)
	var placed []placedCard

	var selectedID string
	if m.selected >= 0 && m.selected < len(m.filtered) {
		selectedID = m.filtered[m.selected].document.ID
	}

	for _, candidate := range boardItems {
		shortest := 0
		for c := 1; c < numCols; c++ {
			if colHeights[c] < colHeights[shortest] {
				shortest = c
			}
		}
		cardLines := m.renderCard(candidate, cardWidth, candidate.document.ID == selectedID)
		placed = append(placed, placedCard{docID: candidate.document.ID, col: shortest, row: colHeights[shortest], lines: cardLines})
		colHeights[shortest] += len(cardLines) + gap
	}

	totalHeight := 0
	for _, h := range colHeights {
		if h > totalHeight {
			totalHeight = h
		}
	}
	if totalHeight > 0 {
		totalHeight -= gap // remove trailing gap
	}

	canvas := make([][]string, totalHeight)
	for i := range canvas {
		canvas[i] = make([]string, numCols)
		for c := range canvas[i] {
			canvas[i][c] = strings.Repeat(" ", cardWidth)
		}
	}

	for _, pc := range placed {
		layout.cardSpans[pc.docID] = [2]int{pc.row, len(pc.lines)}
		for lineIdx, line := range pc.lines {
			if pc.row+lineIdx < len(canvas) {
				canvas[pc.row+lineIdx][pc.col] = line
			}
		}
	}

	for _, row := range canvas {
		layout.rows = append(layout.rows, strings.Join(row, strings.Repeat(" ", gap)))
	}
	return layout
}

// boardView renders the masonry board, offset vertically by boardScrollY so the
// selected card can be scrolled into view.
func (m Model) openGraphSelection() (tea.Model, tea.Cmd) {
	var target membox.DocumentView
	if m.graphLayout == "star" {
		visIn, visOut := m.visibleStarSegments()
		p := m.graphSelected
		switch {
		case p >= 0 && p < len(visIn):
			target = m.graphCards[visIn[p]]
		case p == len(visIn):
			target = m.graphCards[0]
		case p > len(visIn) && p-len(visIn)-1 < len(visOut):
			target = m.graphCards[visOut[p-len(visIn)-1]]
		default:
			return m, nil
		}
	} else {
		visible := m.visibleGraphIndices()
		if m.graphSelected < 0 || m.graphSelected >= len(visible) {
			return m, nil
		}
		target = m.graphCards[visible[m.graphSelected]]
	}
	m.loading = true
	if target.MediaType == "application/pdf" {
		return m, tea.Batch(m.spinner.Tick, openCmd(m.ctx, m.app, m.launcher, target.ID))
	}
	if m.viewerMode == "web" {
		return m, tea.Batch(m.spinner.Tick, openDocumentWebCmd(m.ctx, m.app, m.launcher, target.ID))
	}
	return m, tea.Batch(m.spinner.Tick, resolveViewerCmd(m.ctx, m.app, target.ID))
}

// moveStarSelection walks the star canvas: up/down stay inside a column,
// left/right hop between backlinks, focus, and links.
func (m *Model) moveStarSelection(key string) {
	visIn, visOut := m.visibleStarSegments()
	inN, outN := len(visIn), len(visOut)
	total := inN + 1 + outN
	if total == 0 {
		return
	}
	if m.graphSelected < 0 {
		m.graphSelected = 0
	}
	if m.graphSelected > total-1 {
		m.graphSelected = total - 1
	}
	var segs [][2]int
	if inN > 0 {
		segs = append(segs, [2]int{0, inN - 1})
	}
	segs = append(segs, [2]int{inN, inN})
	if outN > 0 {
		segs = append(segs, [2]int{inN + 1, total - 1})
	}
	current := 0
	for i, seg := range segs {
		if m.graphSelected >= seg[0] && m.graphSelected <= seg[1] {
			current = i
		}
	}
	pos := m.graphSelected - segs[current][0]
	size := segs[current][1] - segs[current][0]
	switch key {
	case "up", "k":
		if pos > 0 {
			pos--
		}
	case "down", "j":
		if pos < size {
			pos++
		}
	case "left", "h":
		if current > 0 {
			current--
			pos = min(pos, segs[current][1]-segs[current][0])
		}
	case "right", "l":
		if current < len(segs)-1 {
			current++
			pos = min(pos, segs[current][1]-segs[current][0])
		}
	case "pgup":
		pos = max(0, pos-5)
	case "pgdown":
		pos = min(size, pos+5)
	case "home":
		current, pos = 0, 0
	case "end":
		current, pos = len(segs)-1, segs[len(segs)-1][1]-segs[len(segs)-1][0]
	}
	m.graphSelected = segs[current][0] + pos
	m.scrollGraphToSelection()
}

// moveGraphSelection walks the thread-tree selection and keeps it in view.
func (m *Model) moveGraphSelection(key string) {
	if m.graphLayout == "star" {
		m.moveStarSelection(key)
		return
	}
	n := len(m.visibleGraphIndices())
	if n == 0 {
		return
	}
	if m.graphSelected < 0 {
		m.graphSelected = 0
	}
	if m.graphSelected > n-1 {
		m.graphSelected = n - 1
	}
	switch key {
	case "up", "left", "k":
		if m.graphSelected > 0 {
			m.graphSelected--
		}
	case "down", "right", "j", "l":
		if m.graphSelected < n-1 {
			m.graphSelected++
		}
	case "pgup":
		m.graphSelected = max(0, m.graphSelected-5)
	case "pgdown":
		m.graphSelected = min(n-1, m.graphSelected+5)
	case "home":
		m.graphSelected = 0
	case "end":
		m.graphSelected = n - 1
	}
	m.scrollGraphToSelection()
}

// scrollGraphToSelection adjusts boardScrollY so the selected thread card is
// visible.
func (m *Model) scrollGraphToSelection() {
	var rows []string
	var starts []int
	if m.graphLayout == "star" {
		rows, starts = m.graphStarRows()
	}
	if len(rows) == 0 {
		rows, starts = m.graphRows()
	}
	if len(starts) == 0 {
		m.boardScrollY = 0
		return
	}
	if m.graphSelected < 0 || m.graphSelected >= len(starts) {
		return
	}
	cardTop := starts[m.graphSelected]
	cardHeight := len(rows) - cardTop
	if m.graphSelected+1 < len(starts) {
		cardHeight = starts[m.graphSelected+1] - cardTop
	}
	visible := m.visibleRows()
	if cardTop < m.boardScrollY {
		m.boardScrollY = cardTop
	}
	if cardTop+cardHeight > m.boardScrollY+visible {
		m.boardScrollY = cardTop + cardHeight - visible
	}
	if m.boardScrollY < 0 {
		m.boardScrollY = 0
	}
	if maxScroll := len(rows) - visible; maxScroll >= 0 && m.boardScrollY > maxScroll {
		m.boardScrollY = maxScroll
	}
	if m.boardScrollY < 0 {
		m.boardScrollY = 0
	}
}

// threadSearchCmd runs the full-text query against the index so the thread
// tree can filter cards by body content — the same FTS path the document list
// uses. Name-mode filters never need it.
func (m *Model) threadSearchCmd(query string, exact bool) tea.Cmd {
	m.graphSearchSequence++
	sequence := m.graphSearchSequence
	return func() tea.Msg {
		results, err := m.app.SearchDocuments(m.ctx, membox.SearchDocumentsQuery{Query: query, Limit: 100, Exact: exact})
		return threadSearchMsg{query: query, sequence: sequence, results: results, err: err}
	}
}

// graphFocusCmd loads a document's link graph (with body previews) so it can
// be walked as a thread tree or drawn as a one-hop star canvas. layout is
// "thread" or "star".
//
// Source PDFs only store a single edge to the conversion index. Expand one more
// hop through that index so link list / graph show every converted chapter.
func graphFocusCmd(ctx context.Context, app App, selectorValue string, layout string) tea.Cmd {
	return func() tea.Msg {
		graph, err := app.GetDocumentGraph(ctx, membox.GetDocumentGraphQuery{Selector: selectorValue})
		if err != nil {
			return graphFocusMsg{err: err}
		}
		seen := map[string]bool{graph.Focus.ID: true}
		cards := make([]membox.DocumentView, 0, 1+len(graph.Outgoing)+len(graph.Incoming)+8)
		cards = append(cards, graph.Focus)
		appendUnique := func(docs ...membox.DocumentView) {
			for _, doc := range docs {
				if doc.ID == "" || seen[doc.ID] {
					continue
				}
				seen[doc.ID] = true
				cards = append(cards, doc)
			}
		}
		appendUnique(graph.Outgoing...)
		if graph.Focus.MediaType == "application/pdf" {
			for _, out := range graph.Outgoing {
				if !looksLikeConversionIndex(out) {
					continue
				}
				sub, subErr := app.GetDocumentGraph(ctx, membox.GetDocumentGraphQuery{Selector: out.ID})
				if subErr != nil {
					continue
				}
				appendUnique(sub.Outgoing...)
			}
		}
		appendUnique(graph.Incoming...)
		// Load body previews so cards show content, not just filenames.
		// Prefer the stored Summary field (board/index) when present.
		previews := make(map[string]string, len(cards))
		for _, card := range cards {
			if card.MediaType == "application/pdf" {
				previews[card.ID] = "PDF · Enter opens viewer · Tab opens TOC"
				continue
			}
			if summary := strings.TrimSpace(card.Summary); summary != "" {
				previews[card.ID] = summary
				continue
			}
			if body, readErr := app.ReadDocument(ctx, membox.ReadDocumentQuery{Selector: card.ID}); readErr == nil {
				if preview := host.DocumentPreview(body, 220); preview != "" {
					previews[card.ID] = preview
				}
			}
		}
		return graphFocusMsg{documentID: graph.Focus.ID, cards: cards, incoming: len(graph.Incoming), previews: previews, layout: layout, topics: graph.Topics}
	}
}

// looksLikeConversionIndex reports whether a document is a PDF→MD TOC index
// (…-pdf-<32hex>.md with no chapter/part suffix).
func looksLikeConversionIndex(doc membox.DocumentView) bool {
	if doc.MediaType == "application/pdf" {
		return false
	}
	_, ok := convertedPDFID(documentFilename(doc))
	return ok
}

// renderCard renders a single document as a Unicode box card
