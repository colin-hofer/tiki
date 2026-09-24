package httpapi

import (
	_ "embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed install.sh
var installScript string

var cliPlatforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}

func registerDownloads(mux *http.ServeMux, files fs.FS) {
	mux.HandleFunc("GET /api/v1/cli", func(w http.ResponseWriter, r *http.Request) {
		platforms := []string{}
		for _, platform := range cliPlatforms {
			if _, err := fs.Stat(files, platform+".gz"); err == nil {
				platforms = append(platforms, platform)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"platforms": platforms})
	})
	mux.HandleFunc("GET /api/v1/cli/install.sh", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeContent(w, r, "install.sh", time.Time{}, strings.NewReader(installScript))
	})
	serve := http.FileServerFS(files)
	mux.Handle("GET /api/v1/cli/downloads/", http.StripPrefix("/api/v1/cli/downloads/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, platform := range cliPlatforms {
			if r.URL.Path == platform+".gz" || r.URL.Path == platform+".sha256" {
				// Browsers must download the gzip file unchanged for verification.
				w.Header().Set("Content-Type", "application/octet-stream")
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
				serve.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})))
}
