// Package bitwarden is an in-process Bitwarden client: the key
// hierarchy (master key, user key, RSA private key, organization keys),
// the EncString envelope, TOTP codes, and the identity and sync API, so
// the shell reads a vault with no external daemon and wipes its keys on
// lock.
package bitwarden

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // RSA-OAEP-SHA1 is Bitwarden's organization-key wrapping (EncString type 4)
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/stubbedev/lys-shell/internal/intconv"
)

// KdfType names the master-password key derivation.
type KdfType int

// The KDFs the server reports at prelogin.
const (
	KdfPBKDF2   KdfType = 0
	KdfArgon2id KdfType = 1
)

// Kdf is an account's key derivation: its type and parameters, memory
// in MiB.
type Kdf struct {
	Type        KdfType
	Iterations  int
	Memory      int
	Parallelism int
}

// Bitwarden's floors: a server reporting weaker parameters is refused
// rather than trusted to derive a key.
const (
	minPBKDF2Iterations  = 5000
	minArgon2Iterations  = 2
	minArgon2MemoryMiB   = 16
	minArgon2Parallelism = 1
	masterKeyLen         = 32
	symmetricKeyLen      = 64
	serverAuthHashRounds = 1
	aesKeyLen            = 32
	hmacKeyLen           = 32
	hmacLen              = sha256.Size
	encStringIVLen       = aes.BlockSize
	redacted             = "[redacted]"
	stretchEncInfo       = "enc"
	stretchMacInfo       = "mac"
	encStringPartSep     = "|"
	encStringTypeSep     = "."
)

// ErrInsufficientKdf reports KDF parameters under Bitwarden's floors.
var ErrInsufficientKdf = errors.New("bitwarden: insufficient KDF parameters")

func (k Kdf) validate() error {
	switch k.Type {
	case KdfPBKDF2:
		if k.Iterations < minPBKDF2Iterations {
			return fmt.Errorf("%w: PBKDF2 with %d iterations", ErrInsufficientKdf, k.Iterations)
		}
	case KdfArgon2id:
		if k.Iterations < minArgon2Iterations || k.Memory < minArgon2MemoryMiB || k.Parallelism < minArgon2Parallelism {
			return fmt.Errorf("%w: Argon2id t=%d m=%dMiB p=%d", ErrInsufficientKdf, k.Iterations, k.Memory, k.Parallelism)
		}
	default:
		return fmt.Errorf("bitwarden: unknown KDF type %d", k.Type)
	}
	return nil
}

// masterKey is the KDF output: it authenticates to the server (through
// its hash) and, stretched, unwraps the user key. It never leaves the
// package.
type masterKey struct{ b []byte }

// deriveMasterKey derives the master key from the password and the
// account email, trimmed and lowercased as every Bitwarden client does.
func deriveMasterKey(password, email string, kdf Kdf) (*masterKey, error) {
	if err := kdf.validate(); err != nil {
		return nil, err
	}
	salt := []byte(strings.ToLower(strings.TrimSpace(email)))
	switch kdf.Type {
	case KdfPBKDF2:
		b, err := pbkdf2.Key(sha256.New, password, salt, kdf.Iterations, masterKeyLen)
		if err != nil {
			return nil, fmt.Errorf("bitwarden: PBKDF2: %w", err)
		}
		return &masterKey{b: b}, nil
	default:
		iterations, errT := intconv.Exact[uint32](kdf.Iterations)
		memoryMiB, errM := intconv.Exact[uint32](kdf.Memory)
		parallelism, errP := intconv.Exact[uint8](kdf.Parallelism)
		if err := errors.Join(errT, errM, errP); err != nil {
			return nil, fmt.Errorf("bitwarden: Argon2id t=%d m=%dMiB p=%d: %w", kdf.Iterations, kdf.Memory, kdf.Parallelism, err)
		}
		if memoryMiB > math.MaxUint32/1024 {
			return nil, fmt.Errorf("bitwarden: Argon2id memory of %dMiB exceeds %dMiB", kdf.Memory, math.MaxUint32/1024)
		}
		sum := sha256.Sum256(salt)
		b := argon2.IDKey([]byte(password), sum[:], iterations, memoryMiB*1024, parallelism, masterKeyLen)
		return &masterKey{b: b}, nil
	}
}

// serverHash is the master password hash the identity server checks
// (HashPurpose::ServerAuthorization): PBKDF2 of the master key salted
// with the password, one round, base64.
func (m *masterKey) serverHash(password string) (string, error) {
	h, err := pbkdf2.Key(sha256.New, string(m.b), []byte(password), serverAuthHashRounds, masterKeyLen)
	if err != nil {
		return "", fmt.Errorf("bitwarden: master password hash: %w", err)
	}
	return base64.StdEncoding.EncodeToString(h), nil
}

// stretch is the master key expanded into an encryption and a MAC key
// (HKDF-Expand with the key as the PRK), what wraps a modern user key.
func (m *masterKey) stretch() (SymmetricKey, error) {
	enc, err := hkdf.Expand(sha256.New, m.b, stretchEncInfo, aesKeyLen)
	if err != nil {
		return SymmetricKey{}, fmt.Errorf("bitwarden: stretch the master key: %w", err)
	}
	mac, err := hkdf.Expand(sha256.New, m.b, stretchMacInfo, hmacKeyLen)
	if err != nil {
		return SymmetricKey{}, fmt.Errorf("bitwarden: stretch the master key: %w", err)
	}
	return SymmetricKey{enc: enc, mac: mac}, nil
}

// legacy is the master key used directly as an AES-256-CBC key, what
// wraps a user key of the deprecated type 0.
func (m *masterKey) legacy() SymmetricKey { return SymmetricKey{enc: append([]byte(nil), m.b...)} }

func (m *masterKey) wipe() { wipe(m.b) }

// SymmetricKey is an AES-256-CBC key with its HMAC-SHA256 key; a legacy
// key has no MAC key and opens only type 0 EncStrings. It formats as
// [redacted].
type SymmetricKey struct{ enc, mac []byte }

// symmetricKeyFrom splits a decrypted 64-byte key into its halves.
func symmetricKeyFrom(b []byte) (SymmetricKey, error) {
	if len(b) != symmetricKeyLen {
		return SymmetricKey{}, fmt.Errorf("bitwarden: a symmetric key is %d bytes, got %d", symmetricKeyLen, len(b))
	}
	return SymmetricKey{enc: append([]byte(nil), b[:aesKeyLen]...), mac: append([]byte(nil), b[aesKeyLen:]...)}, nil
}

// String and GoString keep the key out of logs and %v output.
func (k SymmetricKey) String() string { return redacted }

// GoString is String for %#v.
func (k SymmetricKey) GoString() string { return redacted }

// Wipe zeroes the key material.
func (k SymmetricKey) Wipe() {
	wipe(k.enc)
	wipe(k.mac)
}

// EncType is an EncString's algorithm.
type EncType int

// The EncString types this client opens; Bitwarden defines others
// (the deprecated RSA+HMAC 5 and 6, COSE 7), which parse as unsupported.
const (
	EncAesCbc256B64           EncType = 0
	EncAesCbc256HmacSha256B64 EncType = 2
	EncRsa2048OaepSha256B64   EncType = 3
	EncRsa2048OaepSha1B64     EncType = 4
)

// ErrUnsupportedEncType reports an EncString of a type this client does
// not open.
var ErrUnsupportedEncType = errors.New("bitwarden: unsupported EncString type")

// EncString is Bitwarden's encrypted value, "type.part|part|...":
// AES-CBC (iv, data[, mac]) or RSA-OAEP (data).
type EncString struct {
	Type          EncType
	IV, Data, MAC []byte
}

// ParseEncString parses s, checking the part count and the IV and MAC
// lengths its type requires.
func ParseEncString(s string) (EncString, error) {
	head, body, ok := strings.Cut(s, encStringTypeSep)
	if !ok {
		return EncString{}, errors.New("bitwarden: EncString without a type")
	}
	t, err := strconv.Atoi(head)
	if err != nil {
		return EncString{}, fmt.Errorf("bitwarden: EncString type %q: %w", head, err)
	}
	parts := strings.Split(body, encStringPartSep)
	decode := func(want int) ([][]byte, error) {
		if len(parts) != want {
			return nil, fmt.Errorf("bitwarden: EncString type %d has %d parts, want %d", t, len(parts), want)
		}
		out := make([][]byte, want)
		for i, p := range parts {
			if out[i], err = base64.StdEncoding.DecodeString(p); err != nil {
				return nil, fmt.Errorf("bitwarden: EncString type %d part %d: %w", t, i, err)
			}
		}
		return out, nil
	}
	switch EncType(t) {
	case EncAesCbc256B64:
		p, err := decode(2)
		if err != nil {
			return EncString{}, err
		}
		if len(p[0]) != encStringIVLen {
			return EncString{}, fmt.Errorf("bitwarden: EncString IV is %d bytes, want %d", len(p[0]), encStringIVLen)
		}
		return EncString{Type: EncAesCbc256B64, IV: p[0], Data: p[1]}, nil
	case EncAesCbc256HmacSha256B64:
		p, err := decode(3)
		if err != nil {
			return EncString{}, err
		}
		if len(p[0]) != encStringIVLen || len(p[2]) != hmacLen {
			return EncString{}, fmt.Errorf("bitwarden: EncString IV %d and MAC %d bytes, want %d and %d", len(p[0]), len(p[2]), encStringIVLen, hmacLen)
		}
		return EncString{Type: EncAesCbc256HmacSha256B64, IV: p[0], Data: p[1], MAC: p[2]}, nil
	case EncRsa2048OaepSha256B64, EncRsa2048OaepSha1B64:
		p, err := decode(1)
		if err != nil {
			return EncString{}, err
		}
		return EncString{Type: EncType(t), Data: p[0]}, nil
	default:
		return EncString{}, fmt.Errorf("%w %d", ErrUnsupportedEncType, t)
	}
}

// ErrMAC reports an EncString whose MAC does not verify: the wrong key,
// or tampered data.
var ErrMAC = errors.New("bitwarden: EncString MAC mismatch")

// Decrypt opens an AES EncString. A MAC-carrying string needs a key with
// a MAC key and verifies before decrypting; a legacy type 0 string opens
// only with a legacy key, so a type 0 string can never stand in for an
// authenticated one.
func (k SymmetricKey) Decrypt(e EncString) ([]byte, error) {
	switch e.Type {
	case EncAesCbc256HmacSha256B64:
		if k.mac == nil {
			return nil, errors.New("bitwarden: an authenticated EncString needs a key with a MAC key")
		}
		m := hmac.New(sha256.New, k.mac)
		m.Write(e.IV)
		m.Write(e.Data)
		if subtle.ConstantTimeCompare(m.Sum(nil), e.MAC) != 1 {
			return nil, ErrMAC
		}
	case EncAesCbc256B64:
		if k.mac != nil {
			return nil, errors.New("bitwarden: an unauthenticated EncString is refused under a key with a MAC key")
		}
	default:
		return nil, fmt.Errorf("bitwarden: EncString type %d is not symmetric", e.Type)
	}
	return aesCBCDecrypt(k.enc, e.IV, e.Data)
}

// DecryptString opens s as a UTF-8 string.
func (k SymmetricKey) DecryptString(s string) (string, error) {
	e, err := ParseEncString(s)
	if err != nil {
		return "", err
	}
	b, err := k.Decrypt(e)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func aesCBCDecrypt(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("bitwarden: AES key: %w", err)
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("bitwarden: ciphertext of %d bytes is not whole blocks", len(data))
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("bitwarden: invalid PKCS#7 padding")
	}
	for _, b := range out[len(out)-pad:] {
		if int(b) != pad {
			return nil, errors.New("bitwarden: invalid PKCS#7 padding")
		}
	}
	return out[:len(out)-pad], nil
}

// decryptRSA opens an RSA-OAEP EncString with the account's private key.
func decryptRSA(priv *rsa.PrivateKey, e EncString) ([]byte, error) {
	switch e.Type {
	case EncRsa2048OaepSha256B64:
		return rsa.DecryptOAEP(sha256.New(), nil, priv, e.Data, nil)
	case EncRsa2048OaepSha1B64:
		return rsa.DecryptOAEP(sha1.New(), nil, priv, e.Data, nil) //nolint:gosec // type 4 is defined as OAEP with SHA-1
	default:
		return nil, fmt.Errorf("bitwarden: EncString type %d is not RSA", e.Type)
	}
}

// Keyring is an unlocked account's keys: the user key, the RSA private
// key, and each organization's key. Wipe zeroes them all.
type Keyring struct {
	User    SymmetricKey
	private *rsa.PrivateKey
	Orgs    map[string]SymmetricKey
}

// String keeps the keyring out of logs and %v output.
func (k *Keyring) String() string { return redacted }

// GoString is String for %#v.
func (k *Keyring) GoString() string { return redacted }

// UnlockInput is what unlocking needs: the master password, the account
// email and KDF, the master-key-wrapped user key, the user-key-wrapped
// private key, and the organization keys wrapped for the private key.
type UnlockInput struct {
	Password, Email string
	Kdf             Kdf
	UserKey         string
	PrivateKey      string
	OrgKeys         map[string]string
}

// ErrWrongPassword reports a master password that does not open the
// user key.
var ErrWrongPassword = errors.New("bitwarden: wrong master password")

// Unlock derives the master key and opens the key hierarchy. A user key
// that does not verify is ErrWrongPassword; any other key that does not
// open is an error naming it.
func Unlock(in UnlockInput) (*Keyring, error) {
	mk, err := deriveMasterKey(in.Password, in.Email, in.Kdf)
	if err != nil {
		return nil, err
	}
	defer mk.wipe()
	return unlockWith(mk, in.UserKey, in.PrivateKey, in.OrgKeys)
}

// unlockWith opens the hierarchy under an already derived master key.
func unlockWith(mk *masterKey, userKey, privateKey string, orgKeys map[string]string) (*Keyring, error) {
	user, err := unwrapUserKey(mk, userKey)
	if err != nil {
		return nil, err
	}
	kr := &Keyring{User: user, Orgs: map[string]SymmetricKey{}}
	if privateKey != "" {
		der, err := user.DecryptString(privateKey)
		if err != nil {
			kr.Wipe()
			return nil, fmt.Errorf("bitwarden: private key: %w", err)
		}
		key, err := x509.ParsePKCS8PrivateKey([]byte(der))
		wipe([]byte(der))
		if err != nil {
			kr.Wipe()
			return nil, fmt.Errorf("bitwarden: private key: %w", err)
		}
		rk, ok := key.(*rsa.PrivateKey)
		if !ok {
			kr.Wipe()
			return nil, fmt.Errorf("bitwarden: private key is %T, want RSA", key)
		}
		kr.private = rk
	}
	if err := kr.SetOrgKeys(orgKeys); err != nil {
		kr.Wipe()
		return nil, err
	}
	return kr, nil
}

// SetOrgKeys replaces the organization keys with orgKeys, each opened
// with the private key: a sync after a membership change brings new
// ones and drops old ones. On an error the previous keys stay.
func (k *Keyring) SetOrgKeys(orgKeys map[string]string) error {
	next := make(map[string]SymmetricKey, len(orgKeys))
	fail := func(err error) error {
		for _, o := range next {
			o.Wipe()
		}
		return err
	}
	for id, wrapped := range orgKeys {
		if k.private == nil {
			return fail(errors.New("bitwarden: organization keys without a private key"))
		}
		e, err := ParseEncString(wrapped)
		if err != nil {
			return fail(fmt.Errorf("bitwarden: organization %s key: %w", id, err))
		}
		raw, err := decryptRSA(k.private, e)
		if err != nil {
			return fail(fmt.Errorf("bitwarden: organization %s key: %w", id, err))
		}
		org, err := symmetricKeyFrom(raw)
		wipe(raw)
		if err != nil {
			return fail(fmt.Errorf("bitwarden: organization %s key: %w", id, err))
		}
		next[id] = org
	}
	for _, o := range k.Orgs {
		o.Wipe()
	}
	k.Orgs = next
	return nil
}

// unwrapUserKey opens the user key: a type 2 wrapping with the
// stretched master key, a legacy type 0 with the master key itself.
func unwrapUserKey(mk *masterKey, wrapped string) (SymmetricKey, error) {
	e, err := ParseEncString(wrapped)
	if err != nil {
		return SymmetricKey{}, fmt.Errorf("bitwarden: user key: %w", err)
	}
	var key SymmetricKey
	switch e.Type {
	case EncAesCbc256HmacSha256B64:
		if key, err = mk.stretch(); err != nil {
			return SymmetricKey{}, err
		}
	case EncAesCbc256B64:
		key = mk.legacy()
	default:
		return SymmetricKey{}, fmt.Errorf("bitwarden: user key EncString type %d", e.Type)
	}
	defer key.Wipe()
	raw, err := key.Decrypt(e)
	if errors.Is(err, ErrMAC) {
		return SymmetricKey{}, ErrWrongPassword
	}
	if err != nil {
		// A legacy wrapping has no MAC: a wrong password shows as bad
		// padding or a key of the wrong length.
		if e.Type == EncAesCbc256B64 {
			return SymmetricKey{}, ErrWrongPassword
		}
		return SymmetricKey{}, fmt.Errorf("bitwarden: user key: %w", err)
	}
	defer wipe(raw)
	uk, err := symmetricKeyFrom(raw)
	if err != nil && e.Type == EncAesCbc256B64 {
		return SymmetricKey{}, ErrWrongPassword
	}
	return uk, err
}

// Wipe zeroes every key; the keyring is unusable after.
func (k *Keyring) Wipe() {
	k.User.Wipe()
	for id, o := range k.Orgs {
		o.Wipe()
		delete(k.Orgs, id)
	}
	if k.private != nil {
		k.private.D.SetInt64(0)
		for _, p := range k.private.Primes {
			p.SetInt64(0)
		}
		k.private = nil
	}
}

// wipe zeroes b. Go's collector may have copied a buffer before; this
// clears the copy the client still holds.
func wipe(b []byte) { clear(b) }
