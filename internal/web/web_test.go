//go:build !noconsole

package web

import (
	"net/http"
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
