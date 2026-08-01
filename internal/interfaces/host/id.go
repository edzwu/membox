package host

// ShortDocumentID displays a compact, user-facing document selector prefix.
func ShortDocumentID(id string) string {
	if len(id) > 4 {
		return id[len(id)-4:]
	}
	return id
}
