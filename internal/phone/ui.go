package phone

import (
	"embed"
	"io/fs"
	"net/http"
)

// The phone's UI, served from the binary so it always matches the gateway.
// For now it is a stub that pairs and lists agents; the PWA is phase 2 of
// the plan.
//
//go:embed ui
var uiFiles embed.FS

func uiHandler() http.Handler {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return http.FileServerFS(sub)
}
