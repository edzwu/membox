package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// printJSON prints v as indented JSON, returning a user-friendly error on failure.
func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(w, "error encoding JSON: %v\n", err)
	}
}
