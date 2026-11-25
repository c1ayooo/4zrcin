package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

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

// The HTML form and the JSON endpoints agree on first-boot semantics: the
// first form submission registers the admin, later ones must log in.
func TestLoginFormFirstSubmissionRegistersAdmin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := testutil.NewServer(t, withConsoleAuth(ok, testAuth(testAccountStore(t), nil)))
	defer srv.Close()

	// Do not follow the redirect: the status the gate answers is what this
	// test pins.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := func(values url.Values) (*http.Response, error) {
		return client.PostForm(srv.URL+"/login", values)
	}
	res, err := form(url.Values{"username": {"operator"}, "password": {"s3cretPa55"}, "next": {"/"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusFound {
		t.Fatalf("first submission = %d, want a redirect", res.StatusCode)
	}
	res.Body.Close()

	// Now the account exists and the same form must verify, not re-register:
	// a wrong password is 401, not a second account.
	res, err = form(url.Values{"username": {"operator"}, "password": {"wrong-wrong1"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("second submission with wrong password = %d, want 401", res.StatusCode)
	}
}
