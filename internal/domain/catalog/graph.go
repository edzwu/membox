package catalog

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type EdgeKind string

const (
	EdgeManual EdgeKind = "manual"
	EdgeMember EdgeKind = "member"
)

type GraphEdge struct {
	FromDocumentID DocumentID
	ToDocumentID   DocumentID
	Kind           EdgeKind
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type DocumentGraph struct {
	Outgoing []DocumentLink
	Incoming []DocumentLink
	Topics   []DocumentLink
}

type DocumentLink struct {
	Document *Document
	Edge     GraphEdge
	Path     string
}

func NewGraphEdge(fromDocumentID, toDocumentID DocumentID, kind EdgeKind, now time.Time) (GraphEdge, error) {
	if strings.TrimSpace(string(fromDocumentID)) == "" || strings.TrimSpace(string(toDocumentID)) == "" {
		return GraphEdge{}, errors.New("graph edge documents are required")
	}
	if fromDocumentID == toDocumentID {
		return GraphEdge{}, errors.New("graph edges cannot reference themselves")
	}
	switch kind {
	case EdgeManual, EdgeMember:
	default:
		return GraphEdge{}, fmt.Errorf("invalid graph edge kind %q", kind)
	}
	return GraphEdge{FromDocumentID: fromDocumentID, ToDocumentID: toDocumentID, Kind: kind, CreatedAt: now, UpdatedAt: now}, nil
}

func RehydrateGraphEdge(fromDocumentID, toDocumentID DocumentID, kind EdgeKind, createdAt, updatedAt time.Time) (GraphEdge, error) {
	edge, err := NewGraphEdge(fromDocumentID, toDocumentID, kind, createdAt)
	if err != nil {
		return GraphEdge{}, err
	}
	if updatedAt.IsZero() {
		return GraphEdge{}, errors.New("graph edge updated timestamp is required")
	}
	edge.UpdatedAt = updatedAt
	return edge, nil
}

func TopicNameForDocument(document *Document) (string, bool) {
	if document == nil {
		return "", false
	}
	base := pathBase(document.Location.RelativePath)
	if !strings.HasPrefix(base, "topic-") || !isMarkdownName(base) {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(base, "topic-"), pathExt(base))
	return strings.ReplaceAll(name, "-", " "), name != ""
}

func pathBase(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	index := strings.LastIndex(value, "/")
	if index >= 0 {
		return value[index+1:]
	}
	return value
}

func pathExt(value string) string {
	index := strings.LastIndex(value, ".")
	if index < 0 {
		return ""
	}
	return value[index:]
}

func isMarkdownName(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}
