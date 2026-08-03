package host

// ShortDocumentID displays a compact, user-facing document selector.
// It is the last 4 characters of the id (not a prefix). ResolveDocument
// accepts this form via unique suffix match.
func ShortDocumentID(id string) string {
	if len(id) > 4 {
		return id[len(id)-4:]
	}
	return id
}
