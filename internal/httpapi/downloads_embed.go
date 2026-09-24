//go:build !dev

package httpapi

import (
	"embed"
	"io/fs"
)

//go:embed dist/*.gz dist/*.sha256
var cliFiles embed.FS

func downloads() fs.FS {
	files, err := fs.Sub(cliFiles, "dist")
	if err != nil {
		panic(err)
	}
	return files
}
