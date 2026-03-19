package script

import (
	"embed"
	"io/fs"
)

//go:embed all:scripts
var embeddedScripts embed.FS

// EmbeddedFS returns an fs.FS rooted at the scripts directory.
func EmbeddedFS() (fs.FS, error) {
	return fs.Sub(embeddedScripts, "scripts")
}
