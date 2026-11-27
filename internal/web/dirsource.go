//go:build !noconsole

package web

// The directory source (docs/19 P1-3, the --web-dir flag): a development
// 4zweb checkout serves its built dist directly, so frontend iteration needs
// no Go rebuild. The bundle remains the fallback when no directory is set.
// Headers and content types match the bundle path exactly — the page cannot
// tell which source served it.

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
)

var sourceDir atomic.Value // string

// SetSourceDir points the console at a built dist directory. The directory
// must contain index.html; anything else is a broken build, reported here.
func SetSourceDir(dir string) error {
	dir = filepath.Clean(dir)
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.ErrInvalid
	}
	sourceDir.Store(dir)
	return nil
}

func sourceDirSet() bool {
	v := sourceDir.Load()
	return v != nil && v.(string) != ""
}

// dirIndex serves <dir>/index.html with the bundle's no-cache headers.
func dirIndex() http.HandlerFunc {
	dir := sourceDir.Load().(string)
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := os.ReadFile(filepath.Join(dir, "index.html"))
		if err != nil {
			http.Error(w, "console index unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(raw)
	}
}

// dirStatic serves <dir>/<path> with the bundle's immutable-asset headers and
// explicit content types. Traversal is refused: the cleaned path must stay
// inside the directory.
func dirStatic() http.HandlerFunc {
	dir := sourceDir.Load().(string)
	return func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/static/"))
		if name == "." || name == "" || strings.HasPrefix(name, "..") {
			http.NotFound(w, r)
			return
		}
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", assetCache)
		w.Header().Set("Content-Type", contentType(name))
		_, _ = w.Write(raw)
	}
}
