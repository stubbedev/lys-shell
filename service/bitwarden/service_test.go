package bitwarden

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// account serves the test@bitwarden.com account: prelogin, the token
// grants (checking the password hash, a second factor when asked, the
// refresh token), and a sync with one organization item.
type account struct {
	t          *testing.T
	mu         sync.Mutex
	srv        *httptest.Server
	wantHash   string
	needTOTP   bool
	refresh    string
	rejectRefr bool
	grants     []string
	itemName   string
}

func newAccount(t *testing.T) *account {
	t.Helper()
	a := &account{t: t, refresh: "rt-1", itemName: testOrgName}
	mk, err := deriveMasterKey("asdfasdfasdf", "test@bitwarden.com", Kdf{Type: KdfPBKDF2, Iterations: 600000})
	if err != nil {
		t.Fatal(err)
	}
	a.wantHash, _ = mk.serverHash("asdfasdfasdf")
	mux := http.NewServeMux()
	mux.HandleFunc("/identity/accounts/prelogin/password", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"KdfSettings": map[string]any{"KdfType": 0, "Iterations": 600000}, "Salt": "test@bitwarden.com"})
	})
	mux.HandleFunc("/identity/connect/token", a.token)
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at-") {
			writeJSON(w, 401, map[string]any{"message": "unauthorized"})
			return
		}
		a.mu.Lock()
		name := a.itemName
		a.mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"profile": map[string]any{
				"email": "test@bitwarden.com", "key": testUserKey, "privateKey": testPrivateKey,
				"organizations": []any{map[string]any{"id": testOrgID, "key": testOrgKey}},
			},
			"ciphers": []any{map[string]any{"id": "c1", "type": 2, "organizationId": testOrgID, "name": name}},
		})
	})
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

func (a *account) server() Server {
	return Server{API: a.srv.URL + "/api", Identity: a.srv.URL + "/identity"}
}

func (a *account) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.t.Error(err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f := r.PostForm
	a.grants = append(a.grants, f.Get("grant_type"))
	switch f.Get("grant_type") {
	case "password":
		if f.Get("password") != a.wantHash {
			writeJSON(w, 400, map[string]any{"error": "invalid_grant", "ErrorModel": map[string]any{"Message": "Username or password is incorrect."}})
			return
		}
		if a.needTOTP && f.Get("twoFactorToken") != "123456" {
			writeJSON(w, 400, map[string]any{"TwoFactorProviders2": map[string]any{"0": nil}})
			return
		}
	case "refresh_token":
		if a.rejectRefr || f.Get("refresh_token") != a.refresh {
			writeJSON(w, 400, map[string]any{"error": "invalid_grant"})
			return
		}
	}
	a.refresh += "+"
	writeJSON(w, 200, map[string]any{"access_token": "at-" + a.refresh, "refresh_token": a.refresh, "expires_in": 3600, "token_type": "Bearer"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func login(a *account) LoginRequest {
	return LoginRequest{Server: a.server(), Email: "test@bitwarden.com", Password: "asdfasdfasdf"}
}

// A fresh directory starts logged out with a private device id that
// survives a reopen.
func TestOpenRegistersADevice(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, "lys-shell")
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.State != StateLoggedOut {
		t.Errorf("state = %v", st.State)
	}
	info, err := os.Stat(filepath.Join(dir, deviceFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("device file %v, %v", info, err)
	}
	again, err := Open(dir, "lys-shell")
	if err != nil || again.device.ID != s.device.ID || len(s.device.ID) != 36 {
		t.Errorf("device id %q then %q", s.device.ID, again.device.ID)
	}
}

// A login opens the vault and saves only the session: server, email,
// refresh token, client id; never the password, a key or an item.
func TestLoginOpensTheVault(t *testing.T) {
	a := newAccount(t)
	dir := t.TempDir()
	s, _ := Open(dir, "lys-shell")
	if err := s.Login(context.Background(), login(a)); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.State != StateUnlocked || st.Items != 1 || st.Err != nil || st.Email != "test@bitwarden.com" {
		t.Errorf("status = %+v", st)
	}
	if items := s.Items(); len(items) != 1 || items[0].Name != "Default collection" {
		t.Errorf("items = %+v", items)
	}
	raw, err := os.ReadFile(filepath.Join(dir, sessionFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"asdfasdfasdf", "Default collection", testUserKey[:20], "at-"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the session file holds %q", secret)
		}
	}
	if !strings.Contains(string(raw), a.refresh) {
		t.Errorf("the session file lacks the refresh token: %s", raw)
	}
	if info, _ := os.Stat(filepath.Join(dir, sessionFile)); info.Mode().Perm() != 0o600 {
		t.Errorf("session file mode %v", info.Mode().Perm())
	}
}

// A wrong password is refused by the server and opens nothing; a second
// factor comes back as TwoFactorRequiredError and the retry with the
// code opens the vault.
func TestLoginFailuresAndSecondFactor(t *testing.T) {
	a := newAccount(t)
	s, _ := Open(t.TempDir(), "lys-shell")
	bad := login(a)
	bad.Password = "nope"
	if err := s.Login(context.Background(), bad); !errors.Is(err, ErrInvalidCredentials) || s.Status().State != StateLoggedOut || s.Status().Err == nil {
		t.Errorf("wrong password = %v, status %+v", err, s.Status())
	}
	a.needTOTP = true
	var tf *TwoFactorRequiredError
	if err := s.Login(context.Background(), login(a)); !errors.As(err, &tf) || tf.Providers[0] != TwoFactorAuthenticator {
		t.Fatalf("no code = %v", err)
	}
	req := login(a)
	req.TwoFactor = &TwoFactor{Provider: TwoFactorAuthenticator, Code: "123456"}
	if err := s.Login(context.Background(), req); err != nil || s.Status().State != StateUnlocked || s.Status().Err != nil {
		t.Errorf("with the code = %v, status %+v", err, s.Status())
	}
}

// Lock wipes the keys and items and keeps the session; a restarted
// shell starts locked; unlocking refreshes the token (saving the
// rotated one), and a wrong master password leaves it locked.
func TestLockAndUnlock(t *testing.T) {
	a := newAccount(t)
	dir := t.TempDir()
	s, _ := Open(dir, "lys-shell")
	if err := s.Login(context.Background(), login(a)); err != nil {
		t.Fatal(err)
	}
	kr := s.kr
	s.Lock()
	if s.Status().State != StateLocked || s.Items() != nil || s.kr != nil || len(kr.Orgs) != 0 {
		t.Errorf("after Lock: status %+v, keyring %v", s.Status(), s.kr)
	}
	restarted, err := Open(dir, "lys-shell")
	if err != nil || restarted.Status().State != StateLocked {
		t.Fatalf("restart = %+v, %v", restarted.Status(), err)
	}
	if err := restarted.Unlock(context.Background(), "wrong"); !errors.Is(err, ErrWrongPassword) || restarted.Status().State != StateLocked {
		t.Errorf("wrong password = %v, status %+v", err, restarted.Status())
	}
	if err := restarted.Unlock(context.Background(), "asdfasdfasdf"); err != nil || restarted.Status().State != StateUnlocked {
		t.Fatalf("unlock = %v, status %+v", err, restarted.Status())
	}
	sess, _ := loadSession(filepath.Join(dir, sessionFile))
	if sess.RefreshToken != a.refresh {
		t.Errorf("saved refresh token %q, want the rotated %q", sess.RefreshToken, a.refresh)
	}
}

// A revoked refresh token logs out: the session file goes and the error
// says to log in again.
func TestRevokedRefreshLogsOut(t *testing.T) {
	a := newAccount(t)
	dir := t.TempDir()
	s, _ := Open(dir, "lys-shell")
	if err := s.Login(context.Background(), login(a)); err != nil {
		t.Fatal(err)
	}
	s.Lock()
	a.rejectRefr = true
	if err := s.Unlock(context.Background(), "asdfasdfasdf"); !errors.Is(err, ErrLoginRequired) {
		t.Errorf("unlock = %v", err)
	}
	if s.Status().State != StateLoggedOut {
		t.Errorf("state = %v", s.Status().State)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionFile)); !os.IsNotExist(err) {
		t.Errorf("session file survived: %v", err)
	}
}

// Sync picks up a change while unlocked, refreshing an expired access
// token first; while locked it is an error.
func TestServiceSync(t *testing.T) {
	a := newAccount(t)
	s, _ := Open(t.TempDir(), "lys-shell")
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	if err := s.Login(context.Background(), login(a)); err != nil {
		t.Fatal(err)
	}
	kr := s.kr
	a.mu.Lock()
	a.itemName = seal(t, kr.Orgs[testOrgID], []byte("Renamed"))
	a.mu.Unlock()
	clock = clock.Add(2 * time.Hour)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if items := s.Items(); len(items) != 1 || items[0].Name != "Renamed" {
		t.Errorf("after sync = %+v", items)
	}
	if last := a.grants[len(a.grants)-1]; last != "refresh_token" {
		t.Errorf("an expired token was not refreshed: grants %v", a.grants)
	}
	s.Lock()
	if err := s.Sync(context.Background()); err == nil {
		t.Error("a locked vault synced")
	}
}

// Logout forgets the session.
func TestLogout(t *testing.T) {
	a := newAccount(t)
	dir := t.TempDir()
	s, _ := Open(dir, "lys-shell")
	if err := s.Login(context.Background(), login(a)); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(); err != nil {
		t.Fatal(err)
	}
	if s.Status().State != StateLoggedOut || s.Items() != nil {
		t.Errorf("status %+v", s.Status())
	}
	if again, _ := Open(dir, "lys-shell"); again.Status().State != StateLoggedOut {
		t.Error("a reopened service still has the session")
	}
}
