// Package webui embeds the browser frontend so release binaries are
// self-contained. Development can override it with a directory on disk via
// $TRANSLATOR_STATIC (see internal/web), which avoids recompiling on frontend
// changes.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var embedded embed.FS

// FS returns the embedded frontend, rooted at the static directory.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic("webui: embedded static dir missing: " + err.Error())
	}
	return sub
}
