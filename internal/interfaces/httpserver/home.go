package httpserver

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// static holds the built React home app (home/ → vite build → static/).
// Only .gitkeep is committed; run `make remote-web` to produce real assets.
//
//go:embed all:static
var static embed.FS

// registerHome serves the SPA at /. Asset paths are served from the embedded
// build; every other non-API GET falls back to index.html so client-side
// routing works. The document API lives in routes.go (documents_http.go).
func (s *Server) registerHome() {
	assets, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" && name != "index.html" {
			if file, err := assets.Open(name); err == nil {
				_ = file.Close()
				http.FileServer(http.FS(assets)).ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "home app not built: run `make remote-web`", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}
