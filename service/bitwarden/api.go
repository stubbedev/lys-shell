package bitwarden

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Server is where an account lives: its API and identity endpoints.
type Server struct {
	API, Identity string
}

// The official clouds, by the names ParseServer takes.
const (
	CloudUS = "bitwarden.com"
	CloudEU = "bitwarden.eu"
)

var clouds = map[string]Server{
	CloudUS: {API: "https://api.bitwarden.com", Identity: "https://identity.bitwarden.com"},
	CloudEU: {API: "https://api.bitwarden.eu", Identity: "https://identity.bitwarden.eu"},
}

// ParseServer resolves where an account lives: an official cloud by
// its name, or a self-hosted server (or Vaultwarden) by its http(s)
// base URL, its API under /api and its identity server under
// /identity.
func ParseServer(s string) (Server, error) {
	if cloud, ok := clouds[s]; ok {
		return cloud, nil
	}
	u, err := url.Parse(strings.TrimRight(s, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return Server{}, fmt.Errorf("bitwarden: server %q is neither %s, %s nor an http(s) base URL", s, CloudUS, CloudEU)
	}
	b := u.String()
	return Server{API: b + "/api", Identity: b + "/identity"}, nil
}

// Device is how this client registers with the server: its stable
// identifier and the name the account's device list shows.
type Device struct {
	ID, Name string
}

// The client identity: the desktop client id and Bitwarden's
// LinuxDesktop device type, the category this client is.
const (
	clientID          = "desktop"
	deviceTypeLinux   = "8"
	httpTimeout       = 30 * time.Second
	maxResponseBytes  = 64 << 20
	newDeviceMessage  = "new device verification required"
	tokenScope        = "api offline_access" //nolint:gosec // an OAuth scope, not a credential
	apiKeyScope       = "api"
	formContentType   = "application/x-www-form-urlencoded; charset=utf-8"
	jsonContentType   = "application/json"
	headerDeviceType  = "Device-Type"
	headerClientName  = "Bitwarden-Client-Name"
	headerAuthEmail   = "Auth-Email"
	headerContentType = "Content-Type"
	headerAccept      = "Accept"
	headerAuth        = "Authorization"
)

// Client talks to one server as one device. Every request carries the
// caller's context and a timeout.
type Client struct {
	server Server
	device Device
	http   *http.Client
}

// NewClient is a client for server as device.
func NewClient(server Server, device Device) *Client {
	return &Client{server: server, device: device, http: &http.Client{Timeout: httpTimeout}}
}

// APIError is a server answer the flow does not handle: its status and
// the server's message.
type APIError struct {
	Op      string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("bitwarden: %s: HTTP %d", e.Op, e.Status)
	}
	return fmt.Sprintf("bitwarden: %s: HTTP %d: %s", e.Op, e.Status, e.Message)
}

// Retryable reports a rate limit or a server-side failure, worth trying
// again later; a client error is not.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= http.StatusInternalServerError
}

// TwoFactorProvider is a second factor the server offers.
type TwoFactorProvider int

// The providers this client completes; the server may list others
// (Duo, WebAuthn, YubiKey) that the API key login gets around.
const (
	TwoFactorAuthenticator TwoFactorProvider = 0
	TwoFactorEmail         TwoFactorProvider = 1
)

// TwoFactorRequiredError asks for a second factor: the providers the
// account has.
type TwoFactorRequiredError struct{ Providers []TwoFactorProvider }

func (e *TwoFactorRequiredError) Error() string {
	return fmt.Sprintf("bitwarden: two-factor authentication required (providers %v)", e.Providers)
}

// ErrNewDeviceVerification asks for the code the server emailed for a
// login from a device it has not seen.
var ErrNewDeviceVerification = errors.New("bitwarden: new device verification required")

// ErrInvalidCredentials reports a login the server refused.
var ErrInvalidCredentials = errors.New("bitwarden: invalid credentials")

// Prelogin is an account's key derivation and its master-password salt.
type Prelogin struct {
	Kdf  Kdf
	Salt string
}

// Prelogin asks how the account derives its master key. Older servers
// (Vaultwarden among them) answer only the original endpoint, without a
// salt: then the salt is the email.
func (c *Client) Prelogin(ctx context.Context, email string) (Prelogin, error) {
	body, err := json.Marshal(map[string]string{"email": email})
	if err != nil {
		return Prelogin{}, fmt.Errorf("bitwarden: prelogin request: %w", err)
	}
	var current struct {
		KdfSettings struct {
			KdfType     KdfType
			Iterations  int
			Memory      *int
			Parallelism *int
		}
		Salt string
	}
	status, err := c.postJSON(ctx, "prelogin", c.server.Identity+"/accounts/prelogin/password", body, &current)
	if err == nil {
		k := current.KdfSettings
		p := Prelogin{Kdf: Kdf{Type: k.KdfType, Iterations: k.Iterations, Memory: deref(k.Memory), Parallelism: deref(k.Parallelism)}, Salt: current.Salt}
		if p.Salt == "" {
			p.Salt = email
		}
		return p, nil
	}
	if status != http.StatusNotFound {
		return Prelogin{}, err
	}
	var legacy struct {
		Kdf            KdfType
		KdfIterations  int
		KdfMemory      *int
		KdfParallelism *int
	}
	if _, err := c.postJSON(ctx, "prelogin", c.server.Identity+"/accounts/prelogin", body, &legacy); err != nil {
		return Prelogin{}, err
	}
	return Prelogin{Kdf: Kdf{Type: legacy.Kdf, Iterations: legacy.KdfIterations, Memory: deref(legacy.KdfMemory), Parallelism: deref(legacy.KdfParallelism)}, Salt: email}, nil
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// Token is a login's answer: the access and refresh tokens, when the
// access token expires, and the account's wrapped keys.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expires      time.Time
	Key          string
	PrivateKey   string
}

// TwoFactor is a second factor answering a TwoFactorRequiredError.
type TwoFactor struct {
	Provider TwoFactorProvider
	Code     string
}

// PasswordLogin exchanges the master password hash for tokens. A second
// factor or a new device's emailed code, when the server asks, comes as
// TwoFactorRequiredError or ErrNewDeviceVerification; retry with it.
func (c *Client) PasswordLogin(ctx context.Context, email, passwordHash string, tf *TwoFactor, newDeviceOTP string, now time.Time) (Token, error) {
	form := c.tokenForm()
	form.Set("grant_type", "password")
	form.Set("username", email)
	form.Set("password", passwordHash)
	form.Set("scope", tokenScope)
	form.Set("client_id", clientID)
	if tf != nil {
		form.Set("twoFactorToken", tf.Code)
		form.Set("twoFactorProvider", strconv.Itoa(int(tf.Provider)))
		form.Set("twoFactorRemember", "0")
	}
	if newDeviceOTP != "" {
		form.Set("newDeviceOtp", newDeviceOTP)
	}
	return c.token(ctx, "login", form, base64.RawURLEncoding.EncodeToString([]byte(email)), now)
}

// APIKeyLogin logs in with the account's personal API key: no second
// factor or device check, for the factors this client does not speak.
func (c *Client) APIKeyLogin(ctx context.Context, id, secret string, now time.Time) (Token, error) {
	form := c.tokenForm()
	form.Set("grant_type", "client_credentials")
	form.Set("scope", apiKeyScope)
	form.Set("client_id", id)
	form.Set("client_secret", secret)
	return c.token(ctx, "API key login", form, "", now)
}

// Refresh trades the refresh token for a new access token, under the
// client id that logged in.
func (c *Client) Refresh(ctx context.Context, refreshToken, loginClientID string, now time.Time) (Token, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", loginClientID)
	form.Set("refresh_token", refreshToken)
	t, err := c.token(ctx, "refresh", form, "", now)
	if err == nil && t.RefreshToken == "" {
		t.RefreshToken = refreshToken
	}
	return t, err
}

func (c *Client) tokenForm() url.Values {
	form := url.Values{}
	form.Set("deviceType", deviceTypeLinux)
	form.Set("deviceIdentifier", c.device.ID)
	form.Set("deviceName", c.device.Name)
	return form
}

// token posts a /connect/token form and reads the answer or the
// challenge in it.
func (c *Client) token(ctx context.Context, op string, form url.Values, authEmail string, now time.Time) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.server.Identity+"/connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("bitwarden: %s request: %w", op, err)
	}
	req.Header.Set(headerContentType, formContentType)
	req.Header.Set(headerAccept, jsonContentType)
	req.Header.Set(headerDeviceType, deviceTypeLinux)
	req.Header.Set(headerClientName, clientID)
	if authEmail != "" {
		req.Header.Set(headerAuthEmail, authEmail)
	}
	status, raw, err := c.do(req, op)
	if err != nil {
		return Token{}, err
	}
	var body struct {
		AccessToken         string `json:"access_token"`
		RefreshToken        string `json:"refresh_token"`
		ExpiresIn           int    `json:"expires_in"`
		Key                 string
		PrivateKey          string
		TwoFactorProviders2 map[string]json.RawMessage
		ErrorModel          struct{ Message string }
		Error               string `json:"error"`
		ErrorDescription    string `json:"error_description"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return Token{}, fmt.Errorf("bitwarden: %s: HTTP %d with an unreadable body: %w", op, status, err)
		}
	}
	switch {
	case status == http.StatusOK:
		if body.AccessToken == "" {
			return Token{}, fmt.Errorf("bitwarden: %s: the answer has no access token", op)
		}
		return Token{
			AccessToken: body.AccessToken, RefreshToken: body.RefreshToken,
			Expires: now.Add(time.Duration(body.ExpiresIn) * time.Second),
			Key:     body.Key, PrivateKey: body.PrivateKey,
		}, nil
	case status == http.StatusBadRequest && len(body.TwoFactorProviders2) > 0:
		e := &TwoFactorRequiredError{}
		for k := range body.TwoFactorProviders2 {
			if p, err := strconv.Atoi(k); err == nil {
				e.Providers = append(e.Providers, TwoFactorProvider(p))
			}
		}
		slices.Sort(e.Providers)
		return Token{}, e
	case status == http.StatusBadRequest && strings.EqualFold(body.ErrorModel.Message, newDeviceMessage):
		return Token{}, ErrNewDeviceVerification
	case status == http.StatusBadRequest && body.Error == "invalid_grant":
		return Token{}, fmt.Errorf("%w: %s", ErrInvalidCredentials, firstNonEmpty(body.ErrorModel.Message, body.ErrorDescription))
	default:
		return Token{}, &APIError{Op: op, Status: status, Message: firstNonEmpty(body.ErrorModel.Message, body.ErrorDescription, body.Error)}
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// SendLoginEmail asks the server to email the login's second-factor
// code.
func (c *Client) SendLoginEmail(ctx context.Context, email, passwordHash string) error {
	body, err := json.Marshal(map[string]string{"email": email, "masterPasswordHash": passwordHash, "deviceIdentifier": c.device.ID})
	if err != nil {
		return fmt.Errorf("bitwarden: login email request: %w", err)
	}
	_, err = c.postJSON(ctx, "send the login email", c.server.API+"/two-factor/send-email-login", body, nil)
	return err
}

// Sync fetches the account: its profile with the wrapped keys, folders,
// collections and ciphers.
func (c *Client) Sync(ctx context.Context, accessToken string) (SyncResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.server.API+"/sync?excludeDomains=true", nil)
	if err != nil {
		return SyncResponse{}, fmt.Errorf("bitwarden: sync request: %w", err)
	}
	req.Header.Set(headerAuth, "Bearer "+accessToken)
	req.Header.Set(headerAccept, jsonContentType)
	req.Header.Set(headerDeviceType, deviceTypeLinux)
	req.Header.Set(headerClientName, clientID)
	status, raw, err := c.do(req, "sync")
	if err != nil {
		return SyncResponse{}, err
	}
	if status != http.StatusOK {
		return SyncResponse{}, &APIError{Op: "sync", Status: status, Message: errorMessage(raw)}
	}
	var s SyncResponse
	if err := json.Unmarshal(raw, &s); err != nil {
		return SyncResponse{}, fmt.Errorf("bitwarden: sync: unreadable answer: %w", err)
	}
	return s, nil
}

// postJSON posts body and decodes a 200's JSON into out (nil to ignore
// it), returning the status so a caller can branch on a 404.
func (c *Client) postJSON(ctx context.Context, op, endpoint string, body []byte, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return 0, fmt.Errorf("bitwarden: %s request: %w", op, err)
	}
	req.Header.Set(headerContentType, jsonContentType)
	req.Header.Set(headerAccept, jsonContentType)
	req.Header.Set(headerDeviceType, deviceTypeLinux)
	req.Header.Set(headerClientName, clientID)
	status, raw, err := c.do(req, op)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return status, &APIError{Op: op, Status: status, Message: errorMessage(raw)}
	}
	if out == nil {
		return status, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return status, fmt.Errorf("bitwarden: %s: unreadable answer: %w", op, err)
	}
	return status, nil
}

func (c *Client) do(req *http.Request, op string) (int, []byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("bitwarden: %s: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("bitwarden: %s: read the answer: %w", op, err)
	}
	return resp.StatusCode, raw, nil
}

// errorMessage is the message in an error body, "" when there is none.
func errorMessage(raw []byte) string {
	var body struct {
		Message    string
		ErrorModel struct{ Message string }
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return firstNonEmpty(body.ErrorModel.Message, body.Message)
}
