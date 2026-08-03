// Package web composes the independent web backend with two frontend trees:
// the upstream-friendly Miru reader and the small membox integration adapter.
package web

import (
	"embed"
	"io/fs"

	"membox/internal/application"
	"membox/internal/web/backend"
)

// Frontend assets live outside the backend package so Miru can be refreshed
// independently without mixing product-specific code into its source tree.
//
//go:embed all:frontend/miru all:frontend/membox
var frontendFS embed.FS

// Server keeps the historical public package API while its implementation
// belongs to internal/web/backend.
type Server = backend.Server

func NewServer(service *application.Service) *Server {
	miruFS, err := fs.Sub(frontendFS, "frontend/miru")
	if err != nil {
		panic(err) // compile-time embedded path; an error indicates a broken build
	}
	integrationFS, err := fs.Sub(frontendFS, "frontend/membox")
	if err != nil {
		panic(err)
	}
	return backend.NewServer(service, miruFS, integrationFS)
}
