//go:build dev

package frontend

import "net/http"

// Development serves the UI through Vite; Go builds do not need dist or Node.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "The development UI is served by Vite. Run make dev.", http.StatusNotFound)
	})
}
