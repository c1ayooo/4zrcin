//go:build !noconsole

package web

import (
	"net/http"
	"os"
	"path/filepath"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/c1ayooo/4zrcin/internal/web/bundle"
)

// A refresh must not revalidate the whole bundle: hashed assets are immutable,
// the shell is not. Assets come straight from the manifest — two real entries
// prove the serving path decompresses generated blobs, not just the shell.
func TestStaticCacheHeaders(t *testing.T) {
	static := Static()
	assets := []string{}
	for _, e := range bundle.Entries {
		if len(assets) == 2 {
			break
		}
		if e.Path != "index.html" {
			assets = append(assets, "/static/"+e.Path)
		}
	}
	if len(assets) < 2 {
		t.Fatal("bundle entries missing")
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{assets[0], assetCache},
		{assets[1], assetCache},
		{"/static/index.html", "no-cache"},
	} {
		rec := httptest.NewRecorder()
		static.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", tc.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.want {
			t.Fatalf("%s: Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// The decompressed bytes must hash back to the manifest digest: the serving
// path is a real flate round-trip, not a passthrough.
func TestBundleRoundTripMatchesManifest(t *testing.T) {
	for _, e := range bundle.Entries {
		data, ok := readBundle(e.Path)
		if !ok {
			t.Fatalf("%s: readBundle failed", e.Path)
		}
		if int64(len(data)) != e.Size {
			t.Fatalf("%s: size = %d, manifest says %d", e.Path, len(data), e.Size)
		}
	}
}

func TestMissingStaticAssetDoesNotGetImmutableCaching(t *testing.T) {
	rec := httptest.NewRecorder()
	Static().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/assets/missing-fixture.js", nil))
	if rec.Code != 404 || rec.Header().Get("Cache-Control") == assetCache {
		t.Fatalf("missing asset cached: %d %s", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestIndexRevalidates(t *testing.T) {
	rec := httptest.NewRecorder()
	Index()(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index Cache-Control = %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<script") {
		t.Fatal("index body does not look like the console shell")
	}
}

// --web-dir swaps only the byte-reading step (§6.1): index and static assets
// come from the directory while the headers, content types and caching stay
// the bundle's. Without a directory the bundle serves, unchanged.
func TestSourceDirServesIndexAndStatic(t *testing.T) {
	dir := t.TempDir()
	index := "<html><script src=\"/static/assets/app-deadbeef.js\"></script></html>"
	script := "console.log('from dir')"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app-deadbeef.js"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetSourceDir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sourceDir.Store("") })

	rec := httptest.NewRecorder()
	Index()(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != index {
		t.Fatalf("dir index = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("dir index Cache-Control = %q", got)
	}

	rec = httptest.NewRecorder()
	Static().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/assets/app-deadbeef.js", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != script {
		t.Fatalf("dir static = %d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("dir static Content-Type = %q", got)
	}

	// Traversal never leaves the directory.
	rec = httptest.NewRecorder()
	Static().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/../outside-secret.txt", nil))
	if rec.Code != 404 {
		t.Fatalf("traversal status = %d", rec.Code)
	}
}

func TestSetSourceDirRejectsBrokenBuild(t *testing.T) {
	if err := SetSourceDir(t.TempDir()); err == nil {
		t.Fatal("accepted a directory without index.html")
	}
}
