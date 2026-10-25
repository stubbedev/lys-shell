package bitwarden

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"
)

// fakeServer serves one handler per path and records the forms and
// headers it saw.
type fakeServer struct {
	*httptest.Server
	forms   []url.Values
	headers []http.Header
}

func newFakeServer(t *testing.T, routes map[string]http.HandlerFunc) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	mux := http.NewServeMux()
	for path, h := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
			}
			f.forms = append(f.forms, r.PostForm)
			f.headers = append(f.headers, r.Header.Clone())
			h(w, r)
		})
	}
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func testClient(f *fakeServer) *Client {
	return NewClient(Server{API: f.URL + "/api", Identity: f.URL + "/identity"}, Device{ID: "dev-1", Name: "lys-shell"})
}

var testNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// The current prelogin answers the KDF and a salt; an older server
// without it (a 404) answers the original endpoint, salted by email.
func TestPrelogin(t *testing.T) {
	f := newFakeServer(t, map[string]http.HandlerFunc{
		"/identity/accounts/prelogin/password": reply(200, `{"KdfSettings":{"KdfType":1,"Iterations":3,"Memory":64,"Parallelism":4},"Salt":"salty"}`),
	})
	p, err := testClient(f).Prelogin(context.Background(), "a@b.c")
	if err != nil || p.Kdf != (Kdf{Type: KdfArgon2id, Iterations: 3, Memory: 64, Parallelism: 4}) || p.Salt != "salty" {
		t.Errorf("current prelogin = %+v, %v", p, err)
	}
	old := newFakeServer(t, map[string]http.HandlerFunc{
		"/identity/accounts/prelogin": reply(200, `{"kdf":0,"kdfIterations":600000,"kdfMemory":null,"kdfParallelism":null}`),
	})
	p, err = testClient(old).Prelogin(context.Background(), "a@b.c")
	if err != nil || p.Kdf != (Kdf{Type: KdfPBKDF2, Iterations: 600000}) || p.Salt != "a@b.c" {
		t.Errorf("legacy prelogin = %+v, %v", p, err)
	}
	broken := newFakeServer(t, map[string]http.HandlerFunc{
		"/identity/accounts/prelogin/password": reply(500, `{"message":"boom"}`),
	})
	var ae *APIError
	if _, err := testClient(broken).Prelogin(context.Background(), "a@b.c"); !errors.As(err, &ae) || !ae.Retryable() {
		t.Errorf("a 500 prelogin = %v, want a retryable APIError without the fallback", err)
	}
}

// A password login posts the hash, the device and the base64url email
// header, and reads the tokens and wrapped keys.
func TestPasswordLogin(t *testing.T) {
	f := newFakeServer(t, map[string]http.HandlerFunc{
		"/identity/connect/token": reply(200, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"token_type":"Bearer","Key":"2.k","PrivateKey":"2.p"}`),
	})
	tok, err := testClient(f).PasswordLogin(context.Background(), "a@b.c", "hash", &TwoFactor{Provider: TwoFactorAuthenticator, Code: "123456"}, "", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" || !tok.Expires.Equal(testNow.Add(time.Hour)) || tok.Key != "2.k" || tok.PrivateKey != "2.p" {
		t.Errorf("token = %+v", tok)
	}
	form := f.forms[0]
	for k, want := range map[string]string{
		"grant_type": "password", "username": "a@b.c", "password": "hash", "scope": "api offline_access",
		"client_id": "desktop", "deviceType": "8", "deviceIdentifier": "dev-1", "deviceName": "lys-shell",
		"twoFactorToken": "123456", "twoFactorProvider": "0", "twoFactorRemember": "0",
	} {
		if form.Get(k) != want {
			t.Errorf("form %s = %q, want %q", k, form.Get(k), want)
		}
	}
	if got := f.headers[0].Get("Auth-Email"); got != base64.RawURLEncoding.EncodeToString([]byte("a@b.c")) {
		t.Errorf("Auth-Email = %q", got)
	}
	if form.Has("newDeviceOtp") {
		t.Error("a login without a device code sent one")
	}
}

// The server's challenges come back as typed errors: a second factor
// (with the providers, sorted), a new device's code, refused
// credentials; anything else is an APIError.
func TestLoginChallenges(t *testing.T) {
	for _, c := range []struct {
		body   string
		status int
		check  func(error) bool
	}{
		{`{"TwoFactorProviders2":{"1":{"Email":"a***@b.c"},"0":null}}`, 400, func(err error) bool {
			var tf *TwoFactorRequiredError
			return errors.As(err, &tf) && slices.Equal(tf.Providers, []TwoFactorProvider{TwoFactorAuthenticator, TwoFactorEmail})
		}},
		{`{"ErrorModel":{"Message":"New device verification required"}}`, 400, func(err error) bool { return errors.Is(err, ErrNewDeviceVerification) }},
		{`{"error":"invalid_grant","ErrorModel":{"Message":"Username or password is incorrect. Try again."}}`, 400, func(err error) bool { return errors.Is(err, ErrInvalidCredentials) }},
		{`{"error":"slow_down"}`, 429, func(err error) bool {
			var ae *APIError
			return errors.As(err, &ae) && ae.Retryable() && ae.Message == "slow_down"
		}},
		{`not json`, 502, func(err error) bool { return err != nil }},
	} {
		f := newFakeServer(t, map[string]http.HandlerFunc{"/identity/connect/token": reply(c.status, c.body)})
		if _, err := testClient(f).PasswordLogin(context.Background(), "a@b.c", "h", nil, "", testNow); !c.check(err) {
			t.Errorf("%d %s = %v", c.status, c.body, err)
		}
	}
	f := newFakeServer(t, map[string]http.HandlerFunc{"/identity/connect/token": reply(200, `{"token_type":"Bearer"}`)})
	if _, err := testClient(f).PasswordLogin(context.Background(), "a@b.c", "h", nil, "", testNow); err == nil {
		t.Error("a 200 without an access token logged in")
	}
}

// A new device's emailed code rides the retry.
func TestNewDeviceCodeIsSent(t *testing.T) {
	f := newFakeServer(t, map[string]http.HandlerFunc{"/identity/connect/token": reply(200, `{"access_token":"at","expires_in":1}`)})
	if _, err := testClient(f).PasswordLogin(context.Background(), "a@b.c", "h", nil, "998877", testNow); err != nil {
		t.Fatal(err)
	}
	if got := f.forms[0].Get("newDeviceOtp"); got != "998877" {
		t.Errorf("newDeviceOtp = %q", got)
	}
}

// The API key logs in with client credentials and scope api; a refresh
// sends the login's client id and keeps the refresh token the server
// did not rotate.
func TestAPIKeyLoginAndRefresh(t *testing.T) {
	f := newFakeServer(t, map[string]http.HandlerFunc{"/identity/connect/token": reply(200, `{"access_token":"at2","expires_in":60}`)})
	c := testClient(f)
	if _, err := c.APIKeyLogin(context.Background(), "user.abc", "s3cret", testNow); err != nil {
		t.Fatal(err)
	}
	if f.forms[0].Get("grant_type") != "client_credentials" || f.forms[0].Get("client_id") != "user.abc" || f.forms[0].Get("client_secret") != "s3cret" || f.forms[0].Get("scope") != "api" {
		t.Errorf("API key form = %v", f.forms[0])
	}
	tok, err := c.Refresh(context.Background(), "rt-old", "desktop", testNow)
	if err != nil || tok.AccessToken != "at2" || tok.RefreshToken != "rt-old" {
		t.Errorf("refresh = %+v, %v", tok, err)
	}
	if f.forms[1].Get("grant_type") != "refresh_token" || f.forms[1].Get("refresh_token") != "rt-old" || f.forms[1].Get("client_id") != "desktop" {
		t.Errorf("refresh form = %v", f.forms[1])
	}
}

// Sync sends the bearer token and reads the account; a 401 is an
// APIError that is not retryable.
func TestSync(t *testing.T) {
	f := newFakeServer(t, map[string]http.HandlerFunc{
		"/api/sync": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer at" {
				reply(401, `{"message":"no"}`)(w, r)
				return
			}
			reply(200, `{"profile":{"email":"a@b.c","key":"2.k","privateKey":"2.p","organizations":[{"id":"o1","key":"4.x"}]},"folders":[{"id":"f1","name":"2.n"}],"ciphers":[{"id":"c1","type":1,"name":"2.n","login":{"username":"2.u","uris":[{"uri":"2.u"}]}}]}`)(w, r)
		},
	})
	s, err := testClient(f).Sync(context.Background(), "at")
	if err != nil || s.Profile.Key != "2.k" || len(s.Profile.Organizations) != 1 || len(s.Ciphers) != 1 || s.Ciphers[0].Login.Uris[0].URI != "2.u" {
		t.Errorf("sync = %+v, %v", s, err)
	}
	var ae *APIError
	if _, err := testClient(f).Sync(context.Background(), "bad"); !errors.As(err, &ae) || ae.Status != 401 || ae.Retryable() {
		t.Errorf("unauthorized sync = %v", err)
	}
}

// The clouds resolve by name; a self-hosted base URL puts the API and
// identity under it; anything else is refused.
func TestParseServer(t *testing.T) {
	if s, err := ParseServer(CloudEU); err != nil || s.API != "https://api.bitwarden.eu" || s.Identity != "https://identity.bitwarden.eu" {
		t.Errorf("EU cloud = %+v, %v", s, err)
	}
	s, err := ParseServer("https://vault.example.com/")
	if err != nil || s.API != "https://vault.example.com/api" || s.Identity != "https://vault.example.com/identity" {
		t.Errorf("self-hosted = %+v, %v", s, err)
	}
	for _, bad := range []string{"vault.example.com", "ftp://x", "", "https://x/?a=b", "bitwarden.org"} {
		if _, err := ParseServer(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
