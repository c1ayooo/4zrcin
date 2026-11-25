// Package web serves the embedded single-page console.
//
// The console is one set of static files for the whole instance; only the API
// is per project. The page reads its project from /p/<id>/ in the URL, so the
// asset paths stay absolute and the HTML is served unchanged for any project.
//
// The embedded form is build-tag split (docs/04 §4.1): the default build
// embeds the generated flate bundle (bundle_gen.go + bundle_data, produced by
// internal/web/generate from web/dist); a -tags noconsole build compiles the
// stub in console_empty.go instead — test and UI-less node builds do not
// carry the console bytes. Static, Index and Register keep the same
// signatures either way, so no caller changes.
//go:generate go run generate/main.go

package web
