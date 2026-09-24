//go:build dev

package httpapi

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Development can serve locally built downloads without embedding them or
// rebuilding all four platforms on every Go edit. Run make cli to refresh.
func downloads() fs.FS {
	return os.DirFS(filepath.Join("internal", "httpapi", "dist"))
}
