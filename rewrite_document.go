package membox

import (
	"context"
	"fmt"
	"strings"

	"membox/internal/docrewrite"
	"membox/internal/domain/catalog"
)

func rewriteDocument(ctx context.Context, b *Box, command RewriteDocumentCommand) (RewriteDocumentResult, error) {
	selector := strings.TrimSpace(command.Selector)
	if selector == "" {
		return RewriteDocumentResult{}, fmt.Errorf("document selector is required")
	}
	spec, err := docrewrite.ParseModel(command.Model)
	if err != nil {
		return RewriteDocumentResult{}, err
	}
	document, absolute, err := b.service.ResolveDocument(ctx, selector)
	if err != nil {
		return RewriteDocumentResult{}, err
	}
	if document == nil {
		return RewriteDocumentResult{}, fmt.Errorf("document not found")
	}
	if document.Status != catalog.DocumentActive {
		return RewriteDocumentResult{}, fmt.Errorf("document %s is %s at %s", document.ID, document.Status, absolute)
	}
	media := strings.TrimSpace(document.Index.MediaType)
	if media == "application/pdf" {
		return RewriteDocumentResult{}, fmt.Errorf("document %s is a PDF; convert to Markdown first", document.ID)
	}
	if media != "" && media != "text/markdown" && !strings.HasPrefix(media, "text/") {
		return RewriteDocumentResult{}, fmt.Errorf("rewrite supports Markdown documents, not %s", media)
	}
	body, err := b.service.ReadDocument(ctx, string(document.ID))
	if err != nil {
		return RewriteDocumentResult{}, err
	}
	title := strings.TrimSpace(document.Index.Title)
	runner := docrewrite.Runner{Home: b.home}
	if command.OnProgress != nil {
		runner.OnProgress = func(p docrewrite.Progress) {
			command.OnProgress(RewriteProgress{Done: p.Done, Total: p.Total, Detail: p.Detail})
		}
	}
	result, err := runner.Rewrite(ctx, docrewrite.Request{
		Title: title, Markdown: string(body), Model: spec, Hint: command.Hint,
	})
	if err != nil {
		return RewriteDocumentResult{}, err
	}
	out := RewriteDocumentResult{
		DocumentID: string(document.ID),
		Path:       absolute,
		Title:      title,
		Model:      result.Model,
		Provider:   result.Provider,
		Label:      result.Label,
		BytesIn:    len(body),
		BytesOut:   len(result.Markdown),
		Chunks:     result.Chunks,
		ChunkRunes: result.ChunkRunes,
		DryRun:     command.DryRun,
	}
	if command.DryRun {
		out.Markdown = result.Markdown
		return out, nil
	}
	synced, err := b.service.SyncDocument(ctx, string(document.ID), result.Markdown)
	if err != nil {
		return RewriteDocumentResult{}, err
	}
	out.Path = synced.Path
	out.DocumentID = string(synced.DocumentID)
	return out, nil
}
