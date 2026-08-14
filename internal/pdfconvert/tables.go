package pdfconvert

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
)

const (
	maxTableRows          = 1000
	maxTableColumns       = 256
	maxExpandedTableCells = 100_000
)

type markdownReplacement struct {
	start int
	end   int
	body  string
}

type tableSlot struct {
	value    string
	occupied bool
}

// normalizeHTMLTables converts converter-produced HTML table blocks to GFM
// pipe tables. Markdown AST ranges ensure examples inside code and nested
// quoted blocks are never rewritten as document tables.
func normalizeHTMLTables(markdown string) string {
	source := []byte(markdown)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	replacements := make([]markdownReplacement, 0)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != ast.KindHTMLBlock || node.Parent() != document {
			return ast.WalkContinue, nil
		}
		block := node.(*ast.HTMLBlock)
		start, end, ok := htmlBlockRange(block)
		if !ok || start < 0 || end > len(source) || start >= end {
			return ast.WalkContinue, nil
		}
		raw := string(source[start:end])
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(strings.ToLower(trimmed), "<table") || !strings.HasSuffix(strings.ToLower(trimmed), "</table>") {
			return ast.WalkContinue, nil
		}
		converted, ok := htmlTableToGFM(trimmed)
		if !ok {
			return ast.WalkContinue, nil
		}
		hadTrailingNewline := strings.HasSuffix(raw, "\n") || strings.HasSuffix(raw, "\r")
		for end < len(source) && (source[end] == '\n' || source[end] == '\r') {
			end++
		}
		if end < len(source) {
			converted += "\n\n"
		} else if hadTrailingNewline {
			converted += "\n"
		}
		replacements = append(replacements, markdownReplacement{start: start, end: end, body: converted})
		return ast.WalkSkipChildren, nil
	})
	if len(replacements) == 0 {
		return markdown
	}
	var output strings.Builder
	cursor := 0
	for _, replacement := range replacements {
		if replacement.start < cursor {
			continue
		}
		output.WriteString(markdown[cursor:replacement.start])
		output.WriteString(replacement.body)
		cursor = replacement.end
	}
	output.WriteString(markdown[cursor:])
	return output.String()
}

func htmlBlockRange(block *ast.HTMLBlock) (int, int, bool) {
	if block.Lines().Len() == 0 {
		return 0, 0, false
	}
	start := block.Lines().At(0).Start
	end := block.Lines().At(block.Lines().Len() - 1).Stop
	if block.HasClosure() {
		if block.ClosureLine.Start < start {
			start = block.ClosureLine.Start
		}
		if block.ClosureLine.Stop > end {
			end = block.ClosureLine.Stop
		}
	}
	return start, end, true
}

func htmlTableToGFM(fragment string) (string, bool) {
	document, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return "", false
	}
	table := firstHTMLElement(document, "table")
	if table == nil {
		return "", false
	}
	rows := tableRows(table)
	if len(rows) == 0 || len(rows) > maxTableRows {
		return "", false
	}
	grid := make([][]tableSlot, 0, len(rows))
	expandedCells := 0
	for rowIndex, row := range rows {
		ensureGridRow(&grid, rowIndex, 0)
		column := 0
		cells := rowCells(row)
		if len(cells) == 0 {
			continue
		}
		for _, cell := range cells {
			for column < len(grid[rowIndex]) && grid[rowIndex][column].occupied {
				column++
			}
			rowspan := positiveSpan(cell, "rowspan")
			colspan := positiveSpan(cell, "colspan")
			expandedCells += rowspan * colspan
			if rowIndex+rowspan > maxTableRows || column+colspan > maxTableColumns || expandedCells > maxExpandedTableCells {
				return "", false
			}
			value := markdownTableCell(cell)
			for rowOffset := 0; rowOffset < rowspan; rowOffset++ {
				targetRow := rowIndex + rowOffset
				ensureGridRow(&grid, targetRow, column+colspan)
				for columnOffset := 0; columnOffset < colspan; columnOffset++ {
					targetColumn := column + columnOffset
					grid[targetRow][targetColumn] = tableSlot{value: value, occupied: true}
				}
			}
			column += colspan
		}
	}
	width := 0
	for _, row := range grid {
		if len(row) > width {
			width = len(row)
		}
	}
	if width == 0 {
		return "", false
	}
	for len(grid) > 0 && emptyTableRow(grid[len(grid)-1]) {
		grid = grid[:len(grid)-1]
	}
	if len(grid) == 0 {
		return "", false
	}

	var output strings.Builder
	writeGFMRow(&output, grid[0], width)
	output.WriteString("| ")
	for column := 0; column < width; column++ {
		if column > 0 {
			output.WriteString(" | ")
		}
		output.WriteString("---")
	}
	output.WriteString(" |\n")
	for _, row := range grid[1:] {
		writeGFMRow(&output, row, width)
	}
	return strings.TrimSuffix(output.String(), "\n"), true
}

func firstHTMLElement(node *html.Node, name string) *html.Node {
	if node.Type == html.ElementNode && strings.EqualFold(node.Data, name) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := firstHTMLElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func tableRows(table *html.Node) []*html.Node {
	rows := make([]*html.Node, 0)
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode && strings.EqualFold(child.Data, "table") {
				continue
			}
			if child.Type == html.ElementNode && strings.EqualFold(child.Data, "tr") {
				rows = append(rows, child)
				continue
			}
			visit(child)
		}
	}
	visit(table)
	return rows
}

func rowCells(row *html.Node) []*html.Node {
	cells := make([]*html.Node, 0)
	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && (strings.EqualFold(child.Data, "td") || strings.EqualFold(child.Data, "th")) {
			cells = append(cells, child)
		}
	}
	return cells
}

func positiveSpan(cell *html.Node, name string) int {
	for _, attribute := range cell.Attr {
		if strings.EqualFold(attribute.Key, name) {
			value, err := strconv.Atoi(strings.TrimSpace(attribute.Val))
			if err == nil && value > 0 && value <= 1000 {
				return value
			}
			return 1
		}
	}
	return 1
}

func ensureGridRow(grid *[][]tableSlot, row, width int) {
	for len(*grid) <= row {
		*grid = append(*grid, nil)
	}
	if len((*grid)[row]) < width {
		(*grid)[row] = append((*grid)[row], make([]tableSlot, width-len((*grid)[row]))...)
	}
}

func markdownTableCell(cell *html.Node) string {
	value := strings.TrimSpace(renderTableCellChildren(cell))
	value = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "|", `\|`)
	return value
}

func renderTableCellChildren(node *html.Node) string {
	var output strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		renderTableCellNode(&output, child)
	}
	return output.String()
}

func renderTableCellNode(output *strings.Builder, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		output.WriteString(node.Data)
	case html.ElementNode:
		name := strings.ToLower(node.Data)
		switch name {
		case "br":
			output.WriteString(" / ")
		case "code":
			output.WriteByte('`')
			output.WriteString(renderTableCellChildren(node))
			output.WriteByte('`')
		case "strong", "b":
			output.WriteString("**")
			output.WriteString(renderTableCellChildren(node))
			output.WriteString("**")
		case "em", "i":
			output.WriteByte('*')
			output.WriteString(renderTableCellChildren(node))
			output.WriteByte('*')
		case "a":
			label := strings.TrimSpace(renderTableCellChildren(node))
			href := htmlAttribute(node, "href")
			if label != "" && href != "" && !strings.ContainsAny(href, "\n\r() ") {
				fmt.Fprintf(output, "[%s](%s)", label, href)
			} else {
				output.WriteString(label)
			}
		default:
			output.WriteString(renderTableCellChildren(node))
		}
	}
}

func htmlAttribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return strings.TrimSpace(attribute.Val)
		}
	}
	return ""
}

func emptyTableRow(row []tableSlot) bool {
	for _, cell := range row {
		if cell.occupied || cell.value != "" {
			return false
		}
	}
	return true
}

func writeGFMRow(output *strings.Builder, row []tableSlot, width int) {
	output.WriteString("| ")
	for column := 0; column < width; column++ {
		if column > 0 {
			output.WriteString(" | ")
		}
		if column < len(row) {
			output.WriteString(row[column].value)
		}
	}
	output.WriteString(" |\n")
}
