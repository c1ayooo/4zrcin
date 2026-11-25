//go:build noconsole

package web

import "net/http"

// The noconsole build carries no console bytes: test binaries and UI-less
// node deployments compile this stub instead of the generated bundle. The
// signatures match console_full.go, so callers and the mux wiring are
// identical across both forms — only the responses differ. An operator who
// reaches a noconsole control plane gets a plain, honest 404 rather than a
// broken shell.
const noConsoleMessage = "此构建未内嵌 console（noconsole）：控制面 API 正常，Web 界面不可用。\n"

// Static serves nothing: every /static/ path is a 404.
func Static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, noConsoleMessage, http.StatusNotFound)
	})
}

// Index serves nothing: the shell route is a 404, same as any other path.
func Index() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, noConsoleMessage, http.StatusNotFound)
	}
}

// Register mounts the stub routes, mirroring console_full.go.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", Index())
	mux.Handle("GET /static/", Static())
}
