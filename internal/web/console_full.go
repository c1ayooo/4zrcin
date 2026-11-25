//go:build !noconsole

package web

import (
	"bytes"
	"compress/flate"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/c1ayooo/4zrcin/internal/web/bundle"
)

// assetCache is safe because Vite puts a content hash in every asset filename:
// the URL changes whenever the bytes do. Without it a refresh revalidates every
// chunk (the xterm bundle is ~330 KB), which is exactly the reload the console
// should not pay for.
const assetCache = "public, max-age=31536000, immutable"

// bundleIndex maps a console URL path to its manifest entry. Built once from
// the generated manifest; paths are unique so a plain map is enough.
var bundleIndex = func() map[string]bundle.Entry {
	m := make(map[string]bundle.Entry, len(bundle.Entries))
	for _, e := range bundle.Entries {
		m[e.Path] = e
	}
	return m
}()

// readBundle decompresses one console file by its URL path. A missing path is
// a false ok, never an error — the caller decides between 404 and panic. A
// corrupt blob also reads as missing: the gate guarantees committed bundles
// match web/dist, so this branch means a broken build, not a runtime hazard.
func readBundle(urlPath string) ([]byte, bool) {
	e, ok := bundleIndex[urlPath]
	if !ok {
		return nil, false
	}
	raw, err := bundle.Data.ReadFile(e.Data)
	if err != nil {
		return nil, false
	}
	zr := flate.NewReader(bytes.NewReader(raw))
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, false
	}
	return data, true
}

// contentType maps a console path to its response Content-Type. The explicit
// table covers the extensions Vite emits; mime.TypeByExtension varies by
// system configuration (and can be missing .js on minimal hosts), so the
// table wins.
func contentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map", ".webmanifest":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		if t := mime.TypeByExtension(path.Ext(p)); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

// Static serves /static/, at the instance root, for every project. Every
// response body is decompressed from the bundle on the way out; assets carry
// the immutable cache policy, everything else revalidates.
func Static() http.Handler {
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		data, ok := readBundle(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", assetCache)
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		w.Header().Set("Content-Type", contentType(name))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
}

// Index serves the console shell at the root of a project. The HTML must be
// revalidated: it names the hashed assets of the running build. A console
// bundle without an index.html is a broken build — fail at startup, not on
// the first request.
func Index() http.HandlerFunc {
	index, ok := readBundle("index.html")
	if !ok {
		panic("console bundle 缺少 index.html")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Length", strconv.Itoa(len(index)))
		_, _ = w.Write(index)
	}
}

// Register mounts the whole console on a mux: index at /, assets below
// /static/. Used when a server and the page live in the same namespace.
func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", Index())
	mux.Handle("GET /static/", Static())
}
