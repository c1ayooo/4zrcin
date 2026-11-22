package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Paths, verbatim from the API document §2.
const (
	pathChallenges = "/openapi/v1/challenges"
	pathStart      = "/openapi/v1/challenges/start"
	pathHint       = "/openapi/v1/challenges/hint"
	pathSubmit     = "/openapi/v1/challenges/submit"
	pathClose      = "/openapi/v1/challenges/close"
)

// maxBody bounds one platform response. The five documented answers are all
// small; a body larger than this is not the platform talking.
const maxBody = 4 << 20

// Config is everything a run needs from outside. Both credential fields are
// required: NewClient refuses an empty one rather than defaulting to anything,
// so a project that was not configured for the benchmark cannot wander into it.
type Config struct {
	BaseURL string // BENCHMARK_BASE_URL, e.g. https://benchmark.example.com
	Token   string // BENCHMARK_TOKEN, sent as the BENCHMARK_TOKEN header
	HTTP    *http.Client
	// DumpDir receives the raw bytes of every GET /challenges answer. The scope
	// checker reads them as the allow-list, so they are written before decoding:
	// what lands there is the platform's bytes, not our model of them. Empty
	// disables the dump.
	DumpDir string
	Clock   func() time.Time
}

// Client talks to one measurement task.
type Client struct {
	cfg  Config
	http *http.Client
	seq  atomic.Int64
	// lastDump is the file the newest successful poll wrote. The runner passes it
	// on as the audit evidence for the scope rewrite, so a scope revision can
	// always be traced back to the exact platform bytes it came from.
	lastDump string
}

// LastDumpFile is the path of the most recent successful dump ("" if the dump is
// disabled or every attempt failed).
func (c *Client) LastDumpFile() string { return c.lastDump }

// NewClient validates the explicit configuration and returns a client.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("bench: BENCHMARK_BASE_URL and BENCHMARK_TOKEN are both required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("bench: bad BENCHMARK_BASE_URL %q: %w", cfg.BaseURL, err)
	}
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Client{cfg: cfg, http: cfg.HTTP}, nil
}

// Challenge is one item of GET /challenges, verbatim from §5.1.
type Challenge struct {
	UniqueCode       string   `json:"unique_code"`
	Description      *string  `json:"description"`
	Difficulty       string   `json:"difficulty"`
	Level            int      `json:"level"`
	TotalScore       int      `json:"total_score"`
	FlagCount        int      `json:"flag_count"`
	CorrectFlagCount int      `json:"correct_flag_count"`
	IsCompleted      bool     `json:"is_completed"`
	ContainerStatus  string   `json:"container_status"`
	ContainerAddr    []string `json:"container_addr"`
}

// Container status values, §5.1.
const (
	ContainerPending     = "pending"
	ContainerAvailable   = "available"
	ContainerStopPending = "stop_pending"
	ContainerStopped     = "stopped"
)

// Started is the answer of POST /challenges/start (§5.2).
type Started struct {
	UniqueCode    string   `json:"unique_code"`
	ContainerAddr []string `json:"container_addr"`
}

// SubmitResult is the answer of POST /challenges/submit (§5.4).
type SubmitResult struct {
	Correct          bool `json:"correct"`
	Awarded          int  `json:"awarded"`
	CumulativeScore  int  `json:"cumulative_score"`
	CorrectFlagCount int  `json:"correct_flag_count"`
	TotalFlagCount   int  `json:"total_flag_count"`
	MatchedFlagIndex *int `json:"matched_flag_index"`
}

// ListChallenges is GET /challenges and the only authority on progress: the
// platform counts the flags, we do not. The raw answer is dumped first when
// DumpDir is set.
func (c *Client) ListChallenges(ctx context.Context) ([]Challenge, error) {
	raw, err := c.do(ctx, http.MethodGet, pathChallenges, nil, nil)
	if err != nil {
		return nil, err
	}
	// The dump is the scope checker's only source of truth. Decoding first and
	// dumping after would let a body we cannot use decide whether the platform's
	// own bytes are kept, so the bytes go to disk first; a dump failure is
	// returned as a DumpError *alongside* the parsed list, because the run needs
	// to hear about it (and record it) without losing a usable poll.
	dumpErr := c.dumpChallenges(raw)
	var out []Challenge
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("bench: decode challenges: %w", err)
	}
	if dumpErr != nil {
		return out, dumpErr
	}
	return out, nil
}

// DumpError says the poll itself worked but its bytes could not be stored. It is
// separate from a poll failure: the caller should keep the parsed list, record
// the failure where the run log can see it, and treat the missing file as "the
// scope is unverified" rather than as "no scope".
type DumpError struct {
	Path string
	Err  error
}

func (e *DumpError) Error() string {
	return fmt.Sprintf("bench: dump challenges to %s: %v", e.Path, e.Err)
}

func (e *DumpError) Unwrap() error { return e.Err }

// StartChallenge is POST /challenges/start. It is idempotent on the platform
// side: starting an already-started challenge returns its current addresses.
// A 409 whose message says "max active" is the three-container ceiling, not a
// broken request — see IsSlotFull.
func (c *Client) StartChallenge(ctx context.Context, uniqueCode string) (*Started, error) {
	q := url.Values{"unique_code": {uniqueCode}}
	raw, err := c.do(ctx, http.MethodPost, pathStart+"?"+q.Encode(), nil, nil)
	if err != nil {
		return nil, err
	}
	var out Started
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("bench: decode start: %w", err)
	}
	if out.UniqueCode != "" && out.UniqueCode != uniqueCode {
		return nil, fmt.Errorf("bench: start %q answered for %q", uniqueCode, out.UniqueCode)
	}
	if len(out.ContainerAddr) == 0 {
		// The document says available containers always carry addresses. An empty
		// list here means the environment is not really up; caller should re-list
		// rather than hand an empty target set to an executor.
		return nil, fmt.Errorf("bench: start %q returned no container address", uniqueCode)
	}
	return &out, nil
}

// CloseChallenge is POST /challenges/close. It is idempotent, and it is the only
// documented way to free a container slot. The addresses that were handed out
// die with it (best practice §三.2), so a caller must never keep them across a
// close — the next start of any challenge may be given the same IP.
func (c *Client) CloseChallenge(ctx context.Context, uniqueCode string) (bool, error) {
	q := url.Values{"unique_code": {uniqueCode}}
	raw, err := c.do(ctx, http.MethodPost, pathClose+"?"+q.Encode(), nil, nil)
	if err != nil {
		return false, err
	}
	var out struct {
		UniqueCode string `json:"unique_code"`
		Closed     bool   `json:"closed"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, fmt.Errorf("bench: decode close: %w", err)
	}
	return out.Closed, nil
}

// Replace closes and starts the same challenge again. The API document's first
// best practice recommends it when an environment was damaged, and it is also
// the only correct way to renew an address: after close, the old address may
// belong to another challenge.
func (c *Client) Replace(ctx context.Context, uniqueCode string) (*Started, error) {
	if _, err := c.CloseChallenge(ctx, uniqueCode); err != nil {
		return nil, fmt.Errorf("replace %q: close: %w", uniqueCode, err)
	}
	started, err := c.StartChallenge(ctx, uniqueCode)
	if err != nil {
		return nil, fmt.Errorf("replace %q: start: %w", uniqueCode, err)
	}
	return started, nil
}

// SubmitFlag is POST /challenges/submit. A correct flag that was already
// accepted comes back as 409 duplicate, which KindOf reports as success.
func (c *Client) SubmitFlag(ctx context.Context, uniqueCode, flag string) (*SubmitResult, error) {
	body, err := json.Marshal(map[string]string{"unique_code": uniqueCode, "flag": flag})
	if err != nil {
		return nil, err
	}
	raw, err := c.do(ctx, http.MethodPost, pathSubmit, body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return nil, err
	}
	var out SubmitResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("bench: decode submit: %w", err)
	}
	return &out, nil
}

// Hint is GET /challenges/hint. It exists because the platform documents it, but
// the run does not call it: reading a hint lowers that challenge's score by the
// platform's ratio, and a hint is the answer key, not a measurement of what the
// agent could do. Keeping it unwired is the point.
func (c *Client) Hint(ctx context.Context, uniqueCode string) (string, error) {
	q := url.Values{"unique_code": {uniqueCode}}
	raw, err := c.do(ctx, http.MethodGet, pathHint+"?"+q.Encode(), nil, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		UniqueCode string  `json:"unique_code"`
		Hint       *string `json:"hint"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("bench: decode hint: %w", err)
	}
	if out.Hint == nil {
		return "", nil
	}
	return *out.Hint, nil
}

// do performs one request and converts a non-2xx answer into a *Failure. The
// body it returns on success is the platform's bytes, untouched.
func (c *Client) do(ctx context.Context, method, path string, body []byte, headers map[string]string) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.BaseURL, "/")+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("BENCHMARK_TOKEN", c.cfg.Token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bench %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("bench %s %s: read: %w", method, path, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// Business errors share one shape (§4.1); the framework's 422 does not
	// (§4.3). Missing a code is reported as-is instead of being invented: an
	// unclassified refusal must stop the run, and its body is the evidence.
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(raw))
	if len(msg) > 400 {
		msg = msg[:400]
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Code != "" {
		msg = env.Message
	}
	return nil, &Failure{Status: resp.StatusCode, Code: env.Code, Message: msg}
}

// dumpChallenges writes the platform's raw answer to DumpDir, atomically, one
// file per poll. Every failure is returned with the path it was writing: a run
// that silently stopped producing snapshots would look healthy while the scope
// checker saw nothing, which is the hardest kind of gap to find afterwards.
func (c *Client) dumpChallenges(raw []byte) error {
	if c.cfg.DumpDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.cfg.DumpDir, 0o755); err != nil {
		return &DumpError{Path: c.cfg.DumpDir, Err: err}
	}
	name := fmt.Sprintf("challenges-%s-%03d.json",
		c.cfg.Clock().UTC().Format("20060102T150405.000Z"), c.seq.Add(1))
	path := filepath.Join(c.cfg.DumpDir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return &DumpError{Path: tmp, Err: err}
	}
	if err := os.Rename(tmp, path); err != nil {
		return &DumpError{Path: path, Err: err}
	}
	c.lastDump = path
	return nil
}
