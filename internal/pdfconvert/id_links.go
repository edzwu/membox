package pdfconvert

import (
	"path/filepath"
	"regexp"
	"strings"
)

// markdownLinkTargetRE matches Markdown link destinations that point at a
// relative .md file (conversion TOC / chapter backlinks).
var markdownLinkTargetRE = regexp.MustCompile(`\]\(([^)]+\.md)\)`)

// DocumentIDLink builds a Miru/companion-stable link that opens a document by
// UUID. Filename-based links break when markdown-it percent-encodes CJK paths;
// identity links use the catalog UUID that conversion already stores in
// graph_edges and document_locations.
func DocumentIDLink(documentID string) string {
	id := strings.TrimSpace(documentID)
	if id == "" {
		return ""
	}
	return "/?id=" + id
}

// rewriteRelativeMarkdownLinks replaces relative *.md link targets with
// /?id=<uuid> whenever the basename is present in filenameToID (keys should be
// lower-case basenames). Unknown targets are left unchanged.
func rewriteRelativeMarkdownLinks(markdown string, filenameToID map[string]string) string {
	if markdown == "" || len(filenameToID) == 0 {
		return markdown
	}
	return markdownLinkTargetRE.ReplaceAllStringFunc(markdown, func(match string) string {
		// match is "](target.md)"
		inner := match[2 : len(match)-1] // strip ]( and )
		// Drop optional title: path "title"
		target := strings.TrimSpace(inner)
		if i := strings.Index(target, ` "`); i >= 0 {
			target = strings.TrimSpace(target[:i])
		}
		if i := strings.Index(target, " '"); i >= 0 {
			target = strings.TrimSpace(target[:i])
		}
		if strings.Contains(target, "://") || strings.HasPrefix(target, "/?") {
			return match
		}
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(target, "\\", "/")))
		id := strings.TrimSpace(filenameToID[base])
		if id == "" {
			return match
		}
		return "](" + DocumentIDLink(id) + ")"
	})
}
