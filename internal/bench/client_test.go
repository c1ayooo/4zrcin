package bench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kksty/tianwu/internal/testutil"
)

// newFakePlatform starts a stub that speaks the documented wire format, with the
// token header checked on every call (a run that forgets it would otherwise pass
// every test and fail on the first real request).
func newFakePlatform(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("BENCHMARK_TOKEN"); got != "tok" {
			t.Errorf("%s %s: BENCHMARK_TOKEN = %q, want tok", r.Method, r.URL.Path, got)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestClient builds a client whose transport retries a dropped loopback
// connect. This machine intermittently drops a new SYN to 127.0.0.1 for up to
// several seconds (a bare dial then fails with WSAETIMEDOUT, which Go does not
// classify as a timeout); without the retry a correct test goes red at random.
// The retry is in the test transport only — production wiring is the runner's
// job, and there it must be visible in the attempt log rather than hidden here.
func newTestClient(t *testing.T, srv *httptest.Server, dumpDir string) *Client {
	t.Helper()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = testutil.RetryDial(nil)
	c, err := NewClient(Config{
		BaseURL: srv.URL,
		Token:   "tok",
		HTTP:    &http.Client{Transport: tr, Timeout: 60 * time.Second},
		DumpDir: dumpDir,
		Clock:   func() time.Time { return time.Date(2026, 11, 22, 7, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The list is the authority on progress, and its raw bytes are what the scope
// checker eats. Both facts are checked here: parse, legality of addresses, and a
// dump file identical to the platform's own bytes.
func TestListChallengesParsesAndDumpsTheRawAnswer(t *testing.T) {
	body := `[{"unique_code":"web_sql_injection_01","description":"通过 SQL 注入获取管理员凭证并读取 flag","difficulty":"easy","level":1,"total_score":100,"flag_count":2,"correct_flag_count":1,"is_completed":false,"container_status":"available","container_addr":["10.0.1.5:8080"]},{"unique_code":"crypto_rsa_02","description":null,"difficulty":"hard","level":3,"total_score":200,"flag_count":1,"correct_flag_count":0,"is_completed":false,"container_status":"stopped","container_addr":[]},{"unique_code":"pwn_stack_03","description":null,"difficulty":"medium","level":2,"total_score":150,"flag_count":1,"correct_flag_count":0,"is_completed":false,"container_status":"pending","container_addr":["10.0.1.9:8080"]}]`
	srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathChallenges || r.Method != http.MethodGet {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	})
	dir := t.TempDir()
	c := newTestClient(t, srv, dir)

	list, err := c.ListChallenges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("challenges = %d, want 3", len(list))
	}
	first := list[0]
	if first.UniqueCode != "web_sql_injection_01" || first.FlagCount != 2 ||
		first.CorrectFlagCount != 1 || first.ContainerStatus != ContainerAvailable {
		t.Fatalf("first challenge = %+v", first)
	}
	if first.Description == nil || !strings.Contains(*first.Description, "SQL 注入") {
		t.Fatalf("description was not decoded: %+v", first.Description)
	}
	if list[1].Description != nil {
		t.Fatalf("null description became %q", *list[1].Description)
	}

	// Only the available challenge may be a target, and only its address. The
	// third entry deliberately carries an address while pending: the API document
	// promises an empty list for every non-available status, and this proves we do
	// not rely on that promise — a mid-transition container must never be handed to
	// an executor as a legal target.
	snap := NewSnapshot(time.Now(), list)
	if got := snap.AllowedAddrs(); len(got) != 1 || got[0] != "10.0.1.5:8080" {
		t.Fatalf("allowed addresses = %v, want exactly the available container", got)
	}
	if got := len(snap.Active()); got != 1 {
		t.Fatalf("active = %d, want 1", got)
	}
	if got := len(snap.Open()); got != 3 {
		t.Fatalf("open = %d, want 3", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dump files = %d, want one per poll", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "challenges-20261122T070000.000Z-") {
		t.Fatalf("dump name = %q, want the poll timestamp", entries[0].Name())
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Fatalf("dump is not the platform's bytes:\n got %s\nwant %s", raw, body)
	}
}

// The 409 on start means two different things and the message is the only
// discriminator. One must queue, the other must stop the whole run.
func TestStartDistinguishesTheCeilingFromAFinishedTask(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    Kind
	}{
		{name: "three containers active", message: "max active challenges reached", want: KindSlotFull},
		{name: "task already finished", message: "task task_2026 already finished", want: KindTerminal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusConflict)
				body, _ := json.Marshal(map[string]any{"code": CodeInvalidState, "message": tc.message, "detail": map[string]any{}})
				_, _ = w.Write(body)
			})
			c := newTestClient(t, srv, "")
			_, err := c.StartChallenge(context.Background(), "web_sql_injection_01")
			if err == nil {
				t.Fatal("a 409 was reported as success")
			}
			if got := KindOf(err); got != tc.want {
				t.Fatalf("KindOf(%v) = %v, want %v", err, got, tc.want)
			}
		})
	}
}

// A correct flag resubmitted comes back as 409 duplicate: for us that is the
// platform confirming the point already exists, so the loop must treat it as
// success, not as a failure that consumes the retry budget.
func TestSubmitParsesTheReceiptAndDuplicateIsSuccess(t *testing.T) {
	srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UniqueCode string `json:"unique_code"`
			Flag       string `json:"flag"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad submit body: %v", err)
		}
		switch req.Flag {
		case "flag{good}":
			_, _ = w.Write([]byte(`{"correct":true,"awarded":50,"cumulative_score":80,"correct_flag_count":2,"total_flag_count":3,"matched_flag_index":1}`))
		case "flag{again}":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"duplicate","message":"flag already submitted","detail":{}}`))
		default:
			_, _ = w.Write([]byte(`{"correct":false,"awarded":0,"cumulative_score":80,"correct_flag_count":2,"total_flag_count":3,"matched_flag_index":null}`))
		}
	})
	c := newTestClient(t, srv, "")

	got, err := c.SubmitFlag(context.Background(), "web_sql_injection_01", "flag{good}")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Correct || got.Awarded != 50 || got.CumulativeScore != 80 ||
		got.CorrectFlagCount != 2 || got.TotalFlagCount != 3 ||
		got.MatchedFlagIndex == nil || *got.MatchedFlagIndex != 1 {
		t.Fatalf("receipt = %+v, want the documented fields", got)
	}

	miss, err := c.SubmitFlag(context.Background(), "web_sql_injection_01", "flag{typo}")
	if err != nil {
		t.Fatal(err)
	}
	if miss.Correct || miss.Awarded != 0 || miss.MatchedFlagIndex != nil {
		t.Fatalf("wrong flag receipt = %+v, want correct=false and no index", miss)
	}

	_, err = c.SubmitFlag(context.Background(), "web_sql_injection_01", "flag{again}")
	if err == nil {
		t.Fatal("duplicate was reported as a new acceptance")
	}
	if got := KindOf(err); got != KindDuplicate {
		t.Fatalf("KindOf(duplicate) = %v, want duplicate", got)
	}
}

// Replacing a damaged environment is close-then-start, in that order: the
// address handed out by start is only valid until close.
func TestReplaceClosesBeforeStartingAgain(t *testing.T) {
	var calls []string
	addrs := 0
	srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case pathClose:
			_, _ = w.Write([]byte(`{"unique_code":"web_sql_injection_01","closed":true}`))
		case pathStart:
			addrs++
			_, _ = w.Write([]byte(`{"unique_code":"web_sql_injection_01","container_addr":["10.0.9.9:8080"]}`))
		}
	})
	c := newTestClient(t, srv, "")
	started, err := c.Replace(context.Background(), "web_sql_injection_01")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != pathClose+","+pathStart {
		t.Fatalf("call order = %v, want close then start", calls)
	}
	if len(started.ContainerAddr) != 1 || started.ContainerAddr[0] != "10.0.9.9:8080" {
		t.Fatalf("replacement address = %v", started.ContainerAddr)
	}
}

// A framework 422 has a different body shape (§4.3). It carries no machine code,
// so it must surface as an unclassified terminal failure rather than be retried
// or mistaken for a business answer.
func TestFrameworkValidationErrorIsTerminal(t *testing.T) {
	srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body","flag"],"msg":"String should have at least 1 character","type":"string_too_short"}]}`))
	})
	c := newTestClient(t, srv, "")
	_, err := c.SubmitFlag(context.Background(), "web_sql_injection_01", "")
	if err == nil {
		t.Fatal("a 422 was reported as success")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Status != http.StatusUnprocessableEntity || f.Code != "" {
		t.Fatalf("failure = %+v, want status 422 and no invented code", f)
	}
	if got := KindOf(err); got != KindTerminal {
		t.Fatalf("KindOf(422) = %v, want terminal", got)
	}
}

// The run may only exist when both credentials were given explicitly.
func TestNewClientRequiresExplicitConfiguration(t *testing.T) {
	if _, err := NewClient(Config{Token: "tok"}); err == nil {
		t.Fatal("a client was created without a base URL")
	}
	if _, err := NewClient(Config{BaseURL: "https://example.com"}); err == nil {
		t.Fatal("a client was created without a token")
	}
}

// A start that returns an empty address list is not usable: handing that to an
// executor would ask it to attack nothing, or worse, to pick its own target.
func TestStartWithoutAddressIsAnError(t *testing.T) {
	srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"unique_code":"web_sql_injection_01","container_addr":[]}`))
	})
	c := newTestClient(t, srv, "")
	if _, err := c.StartChallenge(context.Background(), "web_sql_injection_01"); err == nil {
		t.Fatal("an empty container address list was accepted")
	}
}

// A dump failure must be visible without costing the poll: the parsed list is
// still returned, and the error names the path it could not write. Otherwise a
// run could quietly stop producing snapshots while looking healthy.
func TestListChallengesReportsAFailedDumpWithoutLosingThePoll(t *testing.T) {
	body := `[{"unique_code":"web_sql_injection_01","difficulty":"easy","level":1,"total_score":100,"flag_count":1,"correct_flag_count":0,"is_completed":false,"container_status":"available","container_addr":["10.0.1.5:8080"]}]`
	srv := newFakePlatform(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	blocked := filepath.Join(t.TempDir(), "dump")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t, srv, blocked)

	list, err := c.ListChallenges(context.Background())
	if len(list) != 1 {
		t.Fatalf("poll lost its list: %v (err %v)", list, err)
	}
	var de *DumpError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want *DumpError", err)
	}
	if de.Path != blocked {
		t.Fatalf("DumpError.Path = %q, want %q", de.Path, blocked)
	}
}
