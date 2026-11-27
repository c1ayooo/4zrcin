//go:build !noconsole

package web

// The directory source (docs/19 P1-3, the --web-dir flag): a development
// 4zweb checkout serves its built dist directly, so frontend iteration needs
// no Go rebuild. The bundle remains the fallback when no directory is set.
// The directory swaps only the byte-reading step (§6.1): readBundle consults
// it first, so index and static serving keep the bundle's headers, content
// types and caching semantics exactly — the page cannot tell which source
// served it.

import (
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

// readDirFile reads one console file from the source directory. ok is false
// when no directory is set or the file is missing — the caller falls back to
// the bundle or answers 404. Traversal is refused: the cleaned path must stay
// inside the directory.
func readDirFile(urlPath string) ([]byte, bool) {
	v := sourceDir.Load()
	if v == nil || v.(string) == "" {
		return nil, false
	}
	name := path.Clean("/" + urlPath)
	if name == "/" || strings.HasPrefix(name, "/..") {
		return nil, false
	}
	raw, err := os.ReadFile(filepath.Join(v.(string), filepath.FromSlash(strings.TrimPrefix(name, "/"))))
	if err != nil {
		return nil, false
	}
	return raw, true
}
