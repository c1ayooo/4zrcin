package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/c1ayooo/4zrcin/internal/collab"
	"github.com/c1ayooo/4zrcin/internal/testutil"
)

// The JSON face of the gate: one registration, then closed; login issues a
// bearer JWT the gate accepts; failures never say whether the username or the
// password was wrong.
func TestAuthRegisterFirstBootOnly(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := testutil.NewServer(t, withConsoleAuth(ok, testAuth(testAccountStore(t), nil)))
	defer srv.Close()

	post := func(base, path, body string) (int, map[string]any) {
		t.Helper()
		res, err := http.Post(base+path, "application/json", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("%s %s: decode %v", path, body, err)
		}
		return res.StatusCode, out
	}

	code, out := post(srv.URL, "/api/auth/register", `{"username":"operator","password":"s3cretPa55"}`)
	if code != http.StatusCreated || out["code"] != "OK" {
		t.Fatalf("first register = %d %v, want 201 OK", code, out)
	}
	data := out["data"].(map[string]any)
	if data["uid"].(float64) != 1 || data["username"] != "operator" {
		t.Fatalf("register data = %v", data)
	}

	// Registration closed for good: a second account is refused even with a
	// fresh username.
	code, out = post(srv.URL, "/api/auth/register", `{"username":"someone-else","password":"s3cretPa55"}`)
	if code != http.StatusForbidden || out["code"] != "REGISTRATION_CLOSED" {
		t.Fatalf("second register = %d %v, want 403 REGISTRATION_CLOSED", code, out)
	}

	// Registration closed wins over payload validation: once the account
	// exists, every register call is 403, whatever the body says.
	code, out = post(srv.URL, "/api/auth/register", `{"username":"a","password":"s3cretPa55"}`)
	if code != http.StatusForbidden || out["code"] != "REGISTRATION_CLOSED" {
		t.Fatalf("register after close = %d %v, want 403 REGISTRATION_CLOSED", code, out)
	}

	// Validation still guards the one open window: a fresh store.
	fresh := testutil.NewServer(t, withConsoleAuth(ok, testAuth(testAccountStore(t), nil)))
	defer fresh.Close()
	code, _ = post(fresh.URL, "/api/auth/register", `{"username":"weakpass-user","password":"onlyletters"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("weak password = %d, want 400", code)
	}
}

func TestAuthLoginIssuesUsableJWT(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	st := testAccountStore(t)
	if _, err := st.Create("operator", "s3cretPa55"); err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewServer(t, withConsoleAuth(ok, testAuth(st, nil)))
	defer srv.Close()

	body := []byte(`{"username":"operator","password":"nope-nope"}`)
	res, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var wrong map[string]any
	_ = json.NewDecoder(res.Body).Decode(&wrong)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || wrong["code"] != "USER_OR_PASSWORD_WRONG" {
		t.Fatalf("wrong password = %d %v, want 401 USER_OR_PASSWORD_WRONG", res.StatusCode, wrong)
	}

	// An unknown username answers with the same status and code: the response
	// must not say which half was wrong.
	body = []byte(`{"username":"ghost","password":"nope-nope"}`)
	res, err = http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var ghost map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ghost)
	res.Body.Close()
	if ghost["code"] != wrong["code"] || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown user = %d %v, want the same answer as a wrong password", res.StatusCode, ghost)
	}

	// The correct pair mints a bearer JWT the gate accepts, and the response
	// also sets the session cookie for the browser face.
	body = []byte(`{"username":"operator","password":"s3cretPa55"}`)
	res, err = http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Code string `json:"code"`
		Data struct {
			Token     string `json:"token"`
			ExpiresAt int64  `json:"expires_at"`
			User      struct {
				UID      int64  `json:"uid"`
				Username string `json:"username"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	cookies := res.Cookies()
	res.Body.Close()
	if res.StatusCode != http.StatusOK || out.Code != "OK" || out.Data.Token == "" {
		t.Fatalf("login = %d %v", res.StatusCode, out)
	}
	if out.Data.User.UID != 1 || out.Data.User.Username != "operator" || out.Data.ExpiresAt == 0 {
		t.Fatalf("login data = %+v", out.Data)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/board", nil)
	req.Header.Set("Authorization", "Bearer "+out.Data.Token)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bearer JWT at the gate = %d, want 200", res.StatusCode)
	}

	if len(cookies) == 0 || cookies[0].Name != consoleCookie || cookies[0].Value != out.Data.Token {
		t.Fatalf("session cookie missing or mismatched: %v", cookies)
	}
}

// The first-boot flow: /login redirects to /setup while the store is empty,
// /setup takes username + password + confirmation, and the first successful
// submission creates the admin, signs the browser in and closes /setup for
// good. The login form itself never registers.
func TestSetupFlowInitializesAndCloses(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	st := testAccountStore(t)
	srv := testutil.NewServer(t, withConsoleAuth(ok, testAuth(st, nil)))
	defer srv.Close()

	// Do not follow the redirect: the status the gate answers is what this
	// test pins.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(base, path string) *http.Response {
		t.Helper()
		res, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	postForm := func(base, path string, values url.Values) *http.Response {
		t.Helper()
		res, err := client.PostForm(base+path, values)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// Empty store: /login points at /setup, /setup renders the form.
	res := get(srv.URL, "/login")
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "/setup") {
		res.Body.Close()
		t.Fatalf("GET /login before setup = %d (%s), want redirect to /setup", res.StatusCode, res.Header.Get("Location"))
	}
	res.Body.Close()
	res = get(srv.URL, "/setup")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `name="confirm"`) {
		t.Fatalf("GET /setup = %d, want the initialization form with a confirm field", res.StatusCode)
	}

	// Mismatched confirmation is refused and no account is created.
	res = postForm(srv.URL, "/setup", url.Values{"username": {"operator"}, "password": {"s3cretPa55"}, "confirm": {"different1"}, "next": {"/"}})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("setup with mismatched confirm = %d, want 400", res.StatusCode)
	}
	if exists, _ := st.Has(); exists {
		t.Fatal("mismatched setup created an account")
	}

	// The valid submission creates the admin and signs the browser in.
	res = postForm(srv.URL, "/setup", url.Values{"username": {"operator"}, "password": {"s3cretPa55"}, "confirm": {"s3cretPa55"}, "next": {"/"}})
	if res.StatusCode != http.StatusFound {
		res.Body.Close()
		t.Fatalf("valid setup = %d, want a redirect into the console", res.StatusCode)
	}
	cookies := res.Cookies()
	res.Body.Close()
	if len(cookies) == 0 || cookies[0].Name != consoleCookie || cookies[0].Value == "" {
		t.Fatalf("setup did not sign the browser in: %v", cookies)
	}

	// Registration closed: /setup (GET and POST) sends everyone to /login, and
	// a late POST cannot create a second account.
	res = get(srv.URL, "/setup")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/login" {
		res.Body.Close()
		t.Fatalf("GET /setup after initialization = %d (%s), want redirect to /login", res.StatusCode, res.Header.Get("Location"))
	}
	res.Body.Close()
	res = postForm(srv.URL, "/setup", url.Values{"username": {"second"}, "password": {"s3cretPa55"}, "confirm": {"s3cretPa55"}})
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/login" {
		res.Body.Close()
		t.Fatalf("POST /setup after initialization = %d (%s), want redirect to /login", res.StatusCode, res.Header.Get("Location"))
	}
	res.Body.Close()
	if exists, _ := st.Has(); !exists {
		t.Fatal("account store lost the operator")
	}

	// /login is a plain verify-only form now: a wrong password is 401.
	res = postForm(srv.URL, "/login", url.Values{"username": {"operator"}, "password": {"wrong-wrong1"}})
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with wrong password = %d, want 401", res.StatusCode)
	}

	// An uninitialized instance never registers through the login form: a
	// POST /login there redirects to /setup and the store stays empty.
	empty := testutil.NewServer(t, withConsoleAuth(ok, testAuth(testAccountStore(t), nil)))
	defer empty.Close()
	res = postForm(empty.URL, "/login", url.Values{"username": {"sneaky"}, "password": {"s3cretPa55"}})
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "/setup") {
		res.Body.Close()
		t.Fatalf("POST /login before setup = %d (%s), want redirect to /setup", res.StatusCode, res.Header.Get("Location"))
	}
	res.Body.Close()
}

// POST /api/auth/account is the operator's self-service card: every change is
// closed by the current password; the rename and the re-password are
// independent; success re-issues the session with fresh claims; a harness
// credential sees the route as absent (operator-only).
func TestAuthAccountUpdate(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	st := testAccountStore(t)
	if _, err := st.Create("operator", "old-pass-11"); err != nil {
		t.Fatal(err)
	}
	srv := testutil.NewServer(t, withConsoleAuth(ok, testAuth(st, nil)))
	defer srv.Close()

	call := func(token, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/account", strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	login := func(username, password string) string {
		t.Helper()
		res, err := http.Post(srv.URL+"/api/auth/login", "application/json",
			strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.Data.Token == "" {
			t.Fatalf("login %s: %v", username, err)
		}
		return out.Data.Token
	}
	token := login("operator", "old-pass-11")

	// Wrong current password: the change is refused.
	if code, _ := call(token, `{"currentPassword":"nope-nope1","username":"renamed"}`); code != http.StatusUnauthorized {
		t.Fatalf("rename with wrong current password = %d, want 401", code)
	}
	// Nothing to do: 400.
	if code, _ := call(token, `{"currentPassword":"old-pass-11"}`); code != http.StatusBadRequest {
		t.Fatalf("empty change = %d, want 400", code)
	}
	// Weak new password: 400.
	if code, _ := call(token, `{"currentPassword":"old-pass-11","newPassword":"onlyletters"}`); code != http.StatusBadRequest {
		t.Fatalf("weak password = %d, want 400", code)
	}

	// Rename issues a fresh session whose username is the new one; the new
	// name logs in, the old one stops.
	code, out := call(token, `{"currentPassword":"old-pass-11","username":"chief"}`)
	if code != http.StatusOK || out["code"] != "OK" {
		t.Fatalf("rename = %d %v, want 200 OK", code, out)
	}
	_ = login("chief", "old-pass-11")
	res, err := http.Post(srv.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"operator","password":"old-pass-11"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old username after rename = %d, want 401", res.StatusCode)
	}

	// Re-password: the fresh token (with the renamed claims) changes it; the
	// old password stops working and the new one logs in.
	token = login("chief", "old-pass-11")
	if code, _ := call(token, `{"currentPassword":"old-pass-11","newPassword":"new-pass-42"}`); code != http.StatusOK {
		t.Fatalf("re-password = %d, want 200", code)
	}
	res, err = http.Post(srv.URL+"/api/auth/login", "application/json",
		strings.NewReader(`{"username":"chief","password":"old-pass-11"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password after change = %d, want 401", res.StatusCode)
	}
	_ = login("chief", "new-pass-42")

	// A harness credential sees the route as absent, not forbidden.
	registry := collab.NewRegistry(time.Hour)
	commander := sessionCredential(t, registry, "p1")
	if code, _ := call(commander.Token, `{"currentPassword":"new-pass-42","username":"hijack"}`); code != http.StatusNotFound {
		t.Fatalf("agent at account endpoint = %d, want 404", code)
	}
}
