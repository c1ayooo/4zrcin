package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

// decodeOptionalJSON decodes a request body the endpoint also accepts as absent.
//
// A body that is present but malformed is a 400, never a zero value. The zero
// value of these requests is the widest action they can take — an empty goal_id
// resumes the whole project, an empty stop_reason is the default stop, an empty
// evidence_id lets a goal stamp pick its own evidence — so defaulting a truncated
// body would perform more than the caller asked for. An absent body keeps its
// documented meaning.
//
// The second Decode is deliberate: Decoder.Decode returns as soon as it has one
// complete value, so `{"goal_id":"p_x"}zzz` or two concatenated objects would
// otherwise be accepted by reading the first and ignoring the rest. One request
// carries one JSON value; anything after it is a malformed request, not a second
// one to drop.
func decodeOptionalJSON(w http.ResponseWriter, body io.Reader, v any) bool {
	if err := decodeOptionalValue(body, v); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

// decodeOptionalValue is the rule behind decodeOptionalJSON, exposed for the one
// caller that must answer with its own machine code (continue's
// continue_invalid). An absent body is fine; anything present must be exactly one
// JSON value.
func decodeOptionalValue(body io.Reader, v any) error {
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("请求体不是合法 JSON：%w", err)
	}
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case err == nil:
		return errors.New("请求体在第一个 JSON 值之后还有内容：一次请求只接受一个 JSON 对象")
	case !errors.Is(err, io.EOF):
		return fmt.Errorf("请求体不是合法 JSON：%w", err)
	}
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// isPlainID reports whether s is safe to use as a single path segment. The
// generated ids are opaque [type]_[random]; anything else (a dot segment, a slash from a
// decoded %2F, a drive letter, a NUL) is not an id we ever wrote.
func isPlainID(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
