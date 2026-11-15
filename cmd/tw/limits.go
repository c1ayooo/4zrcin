package main

import "net/http"

// maxRequestBody is the instance's backstop for request bodies. Handlers that
// accept a small JSON document already cap themselves tighter (1 KiB–2 MiB);
// this is for the ones that decode straight from r.Body (a new handler, or an
// old one that forgot). 16 MiB is far above the largest legitimate payload —
// a 2 MiB login-material document plus its envelope — so no feature changes
// behavior, while a multi-gigabyte body is refused at the first read instead of
// being decoded into memory.
const maxRequestBody = 16 << 20

// withBodyLimit wraps every request body in http.MaxBytesReader before routing.
// It also rejects a declared Content-Length over the limit immediately, so the
// common case fails without reading anything; a chunked or lying client still
// hits the reader cap. A handler that ignores its decode error cannot eat an
// unbounded body: the read itself fails at the limit.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequestBody {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "请求体过大"})
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}
