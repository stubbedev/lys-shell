package bitwarden

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/internal/atomicfile"
	"github.com/stubbedev/lys-shell/internal/feed"
)

// State is where the vault stands.
type State int

// The vault states: no account, an account whose keys are not in
// memory, and an open vault.
const (
	StateLoggedOut State = iota
	StateLocked
	StateUnlocked
)

// Status is what the shell shows: the state, the account, whether a
// sync runs, when the last one landed, and the last operation's error
// (nil once one succeeds).
type Status struct {
	State    State
	Email    string
	Syncing  bool
	LastSync time.Time
	Err      error
	Items    int
}

// session is what survives a restart: the server, the account, the
// refresh token and the client id it was issued to. No key material and
// no vault data; the KDF comes from a fresh prelogin at every unlock.
type session struct {
	Server       Server
	Email        string
	RefreshToken string
	ClientID     string
}

const (
	sessionFile     = "session.json"
	deviceFile      = "device-id"
	privateFileMode = 0o600
	privateDirMode  = 0o700
	refreshMargin   = time.Minute
)

// ErrLoginRequired reports a session that can no longer refresh (an API
// key login, which gets no refresh token, or a token the server
// revoked): log in again.
var ErrLoginRequired = errors.New("bitwarden: login required")

// LoginRequest is a password login: the server, the account, the master
// password, and the second factor or new device code when a previous
// attempt asked for one.
type LoginRequest struct {
	Server       Server
	Email        string
	Password     string
	TwoFactor    *TwoFactor
	NewDeviceOTP string
}

// APIKeyRequest is an API key login; the master password still opens
// the keys.
type APIKeyRequest struct {
	Server           Server
	Email            string
	ClientID, Secret string
	Password         string
}

// Service owns the vault: the session, the keys while unlocked, and the
// decrypted items. Operations run one at a time; no lock is held across
// a network call except the operation lock that serializes them.
type Service struct {
	dir       string
	now       func() time.Time
	newClient func(Server, Device) *Client
	device    Device

	op sync.Mutex

	mu        sync.Mutex
	sess      *session
	access    string
	accessExp time.Time
	kr        *Keyring
	vault     Vault
	status    Status
	ticks     *feed.Tick
}

// Open loads the session in dir (created 0700), registering a device id
// on first use: an account there starts locked, none logged out.
func Open(dir, deviceName string) (*Service, error) {
	if err := os.MkdirAll(dir, privateDirMode); err != nil {
		return nil, fmt.Errorf("bitwarden: state directory %s: %w", dir, err)
	}
	id, err := deviceID(filepath.Join(dir, deviceFile))
	if err != nil {
		return nil, err
	}
	s := &Service{dir: dir, now: time.Now, newClient: NewClient, device: Device{ID: id, Name: deviceName}, ticks: feed.NewTick()}
	sess, err := loadSession(filepath.Join(dir, sessionFile))
	if err != nil {
		return nil, err
	}
	if sess != nil {
		s.sess = sess
		s.status = Status{State: StateLocked, Email: sess.Email}
	}
	return s, nil
}

// deviceID reads the device id at path, minting and saving a random
// UUID the first time.
func deviceID(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the client's own state file
	if err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("bitwarden: device id %s: %w", path, err)
	}
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", fmt.Errorf("bitwarden: device id: %w", err)
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	if err := atomicfile.Write(path, []byte(id+"\n"), privateFileMode); err != nil {
		return "", fmt.Errorf("bitwarden: save the device id: %w", err)
	}
	return id, nil
}

func loadSession(path string) (*session, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the client's own state file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bitwarden: session %s: %w", path, err)
	}
	var s session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("bitwarden: session %s: %w", path, err)
	}
	return &s, nil
}

func (s *Service) saveSession(sess *session) error {
	b, err := json.Marshal(sess) //nolint:gosec // the refresh token is the session; it is written 0600 and is all that persists
	if err != nil {
		return fmt.Errorf("bitwarden: session: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(s.dir, sessionFile), b, privateFileMode); err != nil {
		return fmt.Errorf("bitwarden: save the session: %w", err)
	}
	return nil
}

// Status is the latest status.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// session is the current session.
func (s *Service) session() *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess
}

// Items is the open vault's items, nil while locked.
func (s *Service) Items() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Item(nil), s.vault.Items...)
}

// Subscribe ticks after every status or vault change; stop unregisters.
func (s *Service) Subscribe() (<-chan struct{}, func()) { return s.ticks.Subscribe() }

// update applies f under the data lock and announces the change.
func (s *Service) update(f func()) {
	s.mu.Lock()
	f()
	s.mu.Unlock()
	feed.Notify(s.ticks)
}

// fail records err as the last error and returns it.
func (s *Service) fail(err error) error {
	s.update(func() { s.status.Err = err; s.status.Syncing = false })
	return err
}

// Login logs in with the master password and opens the vault. A second
// factor or a new device's code comes back as TwoFactorRequiredError or
// ErrNewDeviceVerification: ask for it and call Login again with it.
func (s *Service) Login(ctx context.Context, req LoginRequest) error {
	s.op.Lock()
	defer s.op.Unlock()
	c := s.newClient(req.Server, s.device)
	pre, err := c.Prelogin(ctx, req.Email)
	if err != nil {
		return s.fail(err)
	}
	mk, err := deriveMasterKey(req.Password, pre.Salt, pre.Kdf)
	if err != nil {
		return s.fail(err)
	}
	defer mk.wipe()
	hash, err := mk.serverHash(req.Password)
	if err != nil {
		return s.fail(err)
	}
	tok, err := c.PasswordLogin(ctx, req.Email, hash, req.TwoFactor, req.NewDeviceOTP, s.now())
	if err != nil {
		return s.fail(err)
	}
	return s.open(ctx, c, &session{Server: req.Server, Email: req.Email, RefreshToken: tok.RefreshToken, ClientID: clientID}, tok, mk)
}

// SendLoginEmail asks the server to email the second-factor code for a
// login that needs it.
func (s *Service) SendLoginEmail(ctx context.Context, req LoginRequest) error {
	s.op.Lock()
	defer s.op.Unlock()
	c := s.newClient(req.Server, s.device)
	pre, err := c.Prelogin(ctx, req.Email)
	if err != nil {
		return s.fail(err)
	}
	mk, err := deriveMasterKey(req.Password, pre.Salt, pre.Kdf)
	if err != nil {
		return s.fail(err)
	}
	defer mk.wipe()
	hash, err := mk.serverHash(req.Password)
	if err != nil {
		return s.fail(err)
	}
	if err := c.SendLoginEmail(ctx, req.Email, hash); err != nil {
		return s.fail(err)
	}
	return nil
}

// LoginAPIKey logs in with the personal API key and opens the vault
// with the master password. The server issues no refresh token for it:
// once the access token expires, a locked vault needs the key again.
func (s *Service) LoginAPIKey(ctx context.Context, req APIKeyRequest) error {
	s.op.Lock()
	defer s.op.Unlock()
	c := s.newClient(req.Server, s.device)
	tok, err := c.APIKeyLogin(ctx, req.ClientID, req.Secret, s.now())
	if err != nil {
		return s.fail(err)
	}
	pre, err := c.Prelogin(ctx, req.Email)
	if err != nil {
		return s.fail(err)
	}
	mk, err := deriveMasterKey(req.Password, pre.Salt, pre.Kdf)
	if err != nil {
		return s.fail(err)
	}
	defer mk.wipe()
	return s.open(ctx, c, &session{Server: req.Server, Email: req.Email, RefreshToken: tok.RefreshToken, ClientID: req.ClientID}, tok, mk)
}

// Unlock opens a locked vault with the master password: a fresh token,
// a sync, the keys. A wrong password leaves it locked; a session that
// can no longer refresh logs out with ErrLoginRequired.
func (s *Service) Unlock(ctx context.Context, password string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		return s.fail(ErrLoginRequired)
	}
	c := s.newClient(sess.Server, s.device)
	tok, err := s.freshToken(ctx, c, sess)
	if err != nil {
		return s.fail(err)
	}
	if sess = s.session(); sess == nil {
		return s.fail(ErrLoginRequired)
	}
	pre, err := c.Prelogin(ctx, sess.Email)
	if err != nil {
		return s.fail(err)
	}
	mk, err := deriveMasterKey(password, pre.Salt, pre.Kdf)
	if err != nil {
		return s.fail(err)
	}
	defer mk.wipe()
	return s.open(ctx, c, sess, tok, mk)
}

// open syncs under tok, opens the keys with the master key, and makes
// the vault current; the session is saved only once the vault opened.
func (s *Service) open(ctx context.Context, c *Client, sess *session, tok Token, mk *masterKey) error {
	s.update(func() { s.status.Syncing = true })
	sync, err := c.Sync(ctx, tok.AccessToken)
	if err != nil {
		return s.fail(err)
	}
	kr, err := unlockWith(mk, sync.Profile.Key, sync.Profile.PrivateKey, orgKeys(sync.Profile))
	if err != nil {
		return s.fail(err)
	}
	if tok.RefreshToken != "" {
		sess.RefreshToken = tok.RefreshToken
	}
	if err := s.saveSession(sess); err != nil {
		kr.Wipe()
		return s.fail(err)
	}
	v := DecryptVault(kr, sync)
	now := s.now()
	s.update(func() {
		s.wipeLocked()
		s.sess, s.kr, s.vault = sess, kr, v
		s.access, s.accessExp = tok.AccessToken, tok.Expires
		s.status = Status{State: StateUnlocked, Email: sess.Email, LastSync: now, Items: len(v.Items)}
	})
	return nil
}

func orgKeys(p SyncProfile) map[string]string {
	m := make(map[string]string, len(p.Organizations))
	for _, o := range p.Organizations {
		if o.Key != "" {
			m[o.ID] = o.Key
		}
	}
	return m
}

// freshToken is the held access token while it lasts, else a refreshed
// one; a revoked refresh token, or none (an API key login), logs out.
func (s *Service) freshToken(ctx context.Context, c *Client, sess *session) (Token, error) {
	s.mu.Lock()
	access, exp := s.access, s.accessExp
	s.mu.Unlock()
	if access != "" && s.now().Add(refreshMargin).Before(exp) {
		return Token{AccessToken: access, Expires: exp}, nil
	}
	if sess.RefreshToken == "" {
		return Token{}, errors.Join(ErrLoginRequired, s.logout())
	}
	tok, err := c.Refresh(ctx, sess.RefreshToken, sess.ClientID, s.now())
	if errors.Is(err, ErrInvalidCredentials) {
		return Token{}, errors.Join(fmt.Errorf("%w: %w", ErrLoginRequired, err), s.logout())
	}
	if err != nil {
		return Token{}, err
	}
	// The server rotates the refresh token: the new one is the only
	// one that works from here, whatever happens to this operation.
	next := *sess
	next.RefreshToken = tok.RefreshToken
	if next != *sess {
		if err := s.saveSession(&next); err != nil {
			return Token{}, err
		}
	}
	s.mu.Lock()
	if s.sess == sess {
		s.sess = &next
	}
	s.access, s.accessExp = tok.AccessToken, tok.Expires
	s.mu.Unlock()
	return tok, nil
}

// Sync refetches the open vault: new and changed items, organization
// memberships, folder names.
func (s *Service) Sync(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	sess, kr := s.sess, s.kr
	s.mu.Unlock()
	if kr == nil {
		return s.fail(errors.New("bitwarden: sync needs an open vault"))
	}
	c := s.newClient(sess.Server, s.device)
	s.update(func() { s.status.Syncing = true })
	tok, err := s.freshToken(ctx, c, sess)
	if err != nil {
		return s.fail(err)
	}
	if sess = s.session(); sess == nil {
		return s.fail(ErrLoginRequired)
	}
	sync, err := c.Sync(ctx, tok.AccessToken)
	if err != nil {
		return s.fail(err)
	}
	s.mu.Lock()
	if s.kr != kr {
		// Locked or logged out while the sync ran: drop the answer.
		s.mu.Unlock()
		return nil
	}
	err = kr.SetOrgKeys(orgKeys(sync.Profile))
	s.mu.Unlock()
	if err != nil {
		return s.fail(err)
	}
	v := DecryptVault(kr, sync)
	now := s.now()
	s.update(func() {
		if s.kr != kr {
			return
		}
		s.vault.Wipe()
		s.sess, s.vault = sess, v
		s.access, s.accessExp = tok.AccessToken, tok.Expires
		s.status = Status{State: StateUnlocked, Email: sess.Email, LastSync: now, Items: len(v.Items)}
	})
	return nil
}

// Lock wipes the keys and the plaintext; the session stays for Unlock.
func (s *Service) Lock() {
	s.update(func() {
		s.wipeLocked()
		if s.sess != nil {
			s.status = Status{State: StateLocked, Email: s.sess.Email}
		}
	})
}

// Logout locks and forgets the session.
func (s *Service) Logout() error { return s.logout() }

func (s *Service) logout() error {
	s.update(func() {
		s.wipeLocked()
		s.sess = nil
		s.status = Status{State: StateLoggedOut}
	})
	err := os.Remove(filepath.Join(s.dir, sessionFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("bitwarden: remove the session: %w", err)
	}
	return nil
}

// wipeLocked drops the keys, the plaintext and the access token; the
// caller holds mu.
func (s *Service) wipeLocked() {
	if s.kr != nil {
		s.kr.Wipe()
		s.kr = nil
	}
	s.vault.Wipe()
	s.access, s.accessExp = "", time.Time{}
}
