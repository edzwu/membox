package membox

import (
	"context"

	"membox/internal/application"
	"membox/internal/translation"
)

// mmdNoteNamer asks the local mmd completer (qwen3:14b) for a short title when
// a quick note has no Markdown H1 after the editor closes.
type mmdNoteNamer struct {
	home string
}

func (n mmdNoteNamer) NameNote(ctx context.Context, body string) (string, error) {
	client := translation.MMDClient{
		SocketPath: translation.SocketPath(n.home),
		Ensure: func(ensureCtx context.Context) error {
			return translation.EnsureMMD(ensureCtx, n.home)
		},
	}
	return client.Complete(ctx, application.QuickNoteTitlePrompt(body))
}
