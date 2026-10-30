package desktop

import (
	"embed"
	"io/fs"
)

// The page is plain HTML, CSS and ES modules embedded as they are, with no
// build step, as the phone's is.
//
//go:embed ui
var uiDir embed.FS

// Assets is the page, for Handler or the window's own scheme.
func Assets() fs.FS {
	sub, err := fs.Sub(uiDir, "ui")
	if err != nil {
		panic(err) // the embed above says it is there
	}
	return sub
}
