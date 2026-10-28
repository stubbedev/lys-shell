package bitwarden

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238's default TOTP algorithm
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stubbedev/lys-lib/intconv"
)

// TOTPAlgorithm is a code's HMAC; Steam is SHA-1 with Steam's alphabet.
type TOTPAlgorithm int

// The algorithms an otpauth URI or a steam:// key names.
const (
	TOTPSHA1 TOTPAlgorithm = iota
	TOTPSHA256
	TOTPSHA512
	TOTPSteam
)

const (
	base32Chars       = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	steamChars        = "23456789BCDFGHJKMNPQRTVWXY"
	defaultDigits     = 6
	steamDigits       = 5
	maxDigits         = 10
	defaultPeriod     = 30
	otpauthScheme     = "otpauth://"
	steamScheme       = "steam://"
	base32BitsPerChar = 5
)

// TOTP is a parsed authenticator key: the secret and the code's shape.
type TOTP struct {
	Secret    []byte
	Algorithm TOTPAlgorithm
	Digits    int
	Period    int
	Issuer    string
	Account   string
}

// ErrMissingSecret reports an otpauth URI without a secret.
var ErrMissingSecret = errors.New("bitwarden: TOTP without a secret")

// ParseTOTP reads a vault item's TOTP field as Bitwarden's clients do: an
// otpauth:// URI, a steam:// key, or a bare base32 secret, all
// case-insensitive.
func ParseTOTP(key string) (TOTP, error) {
	key = strings.ToLower(key)
	switch {
	case strings.HasPrefix(key, otpauthScheme):
		u, err := url.Parse(key)
		if err != nil {
			return TOTP{}, errors.New("bitwarden: invalid otpauth URI")
		}
		label := strings.TrimPrefix(u.Path, "/")
		t := TOTP{Algorithm: TOTPSHA1, Digits: defaultDigits, Period: defaultPeriod, Account: label}
		if issuer, account, ok := strings.Cut(label, ":"); ok {
			t.Issuer, t.Account = strings.TrimSpace(issuer), strings.TrimSpace(account)
		}
		q := u.Query()
		switch q.Get("algorithm") {
		case "sha256":
			t.Algorithm = TOTPSHA256
		case "sha512":
			t.Algorithm = TOTPSHA512
		}
		if d, err := strconv.Atoi(q.Get("digits")); err == nil {
			t.Digits = min(max(d, 0), maxDigits)
		}
		if p, err := strconv.Atoi(q.Get("period")); err == nil {
			t.Period = max(p, 1)
		}
		if issuer := q.Get("issuer"); issuer != "" {
			t.Issuer = issuer
		}
		secret := q.Get("secret")
		if secret == "" {
			return TOTP{}, ErrMissingSecret
		}
		t.Secret = decodeBase32(secret)
		return t, nil
	case strings.HasPrefix(key, steamScheme):
		return TOTP{Secret: decodeBase32(strings.TrimPrefix(key, steamScheme)), Algorithm: TOTPSteam, Digits: steamDigits, Period: defaultPeriod}, nil
	default:
		return TOTP{Secret: decodeBase32(key), Algorithm: TOTPSHA1, Digits: defaultDigits, Period: defaultPeriod}, nil
	}
}

// decodeBase32 is Bitwarden's lenient decoder: characters outside the
// alphabet (padding, typos, separators) are dropped and trailing bits
// short of a byte are ignored, so a key that a strict decoder rejects
// still yields the code Bitwarden's apps show.
func decodeBase32(s string) []byte {
	var out []byte
	var acc uint
	var bits int
	for _, c := range strings.ToUpper(s) {
		i := strings.IndexRune(base32Chars, c)
		if i < 0 {
			continue
		}
		acc = acc<<base32BitsPerChar | uint(i)
		bits += base32BitsPerChar
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits&0xff))
			acc &= 1<<bits - 1
		}
	}
	return out
}

// Code is the code at now and how long it stays valid.
func (t TOTP) Code(now time.Time) (string, time.Duration) {
	period := int64(t.Period)
	unix := now.Unix()
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], intconv.Clamp[uint64](unix/period))
	m := hmac.New(t.hash(), t.Secret)
	m.Write(counter[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	remaining := time.Duration(period-unix%period) * time.Second
	if t.Algorithm == TOTPSteam {
		var b strings.Builder
		for range t.Digits {
			b.WriteByte(steamChars[bin%uint32(len(steamChars))])
			bin /= uint32(len(steamChars))
		}
		return b.String(), remaining
	}
	code := strconv.FormatUint(uint64(bin), 10)
	if len(code) > t.Digits {
		code = code[len(code)-t.Digits:]
	}
	return strings.Repeat("0", t.Digits-len(code)) + code, remaining
}

func (t TOTP) hash() func() hash.Hash {
	switch t.Algorithm {
	case TOTPSHA256:
		return sha256.New
	case TOTPSHA512:
		return sha512.New
	default:
		return sha1.New
	}
}
