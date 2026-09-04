// Package web embeds the static frontend (landing, map demo, API docs) so the
// single b2d-server binary serves both the JSON API (/v1/*) and the site
// (everything else) from one origin — no CORS, one Docker image.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:public
var embedded embed.FS

// Assets is the site file tree rooted at web/public (so "/" maps to index.html).
var Assets fs.FS = mustSub()

func mustSub() fs.FS {
	sub, err := fs.Sub(embedded, "public")
	if err != nil {
		panic(err) // embed path is a compile-time constant; this cannot fail at runtime
	}
	return sub
}
