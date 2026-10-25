package bitwarden

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// Bitwarden's SDK vectors at 2023-01-01T00:00:00Z: bare, padded and
// sanitized base32, Steam keys, and keys of odd lengths the lenient
// decoder still reads.
func TestTOTPMatchesBitwarden(t *testing.T) {
	at := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	for key, want := range map[string]string{
		"WQIQ25BRKZYCJVYP": "194506",
		"wqiq25brkzycjvyp": "194506",
		"PIUDISEQYA":       "829846",
		"PIUDISEQYA======": "829846",
		"PIUD1IS!EQYA=":    "829846",
		"steam://HXDMVJECJJWSRB3HWIZR4IFUGFTMXBOZ":                             "7W6CJ",
		"StEam://HXDMVJECJJWSRB3HWIZR4IFUGFTMXBOZ":                             "7W6CJ",
		"steam://ABCD123":                                                      "N26DF",
		"ddfdf":                                                                "932653",
		"HJSGFJHDFDJDJKSDFD":                                                   "000034",
		"xvdsfasdfasdasdghsgsdfg":                                              "403786",
		"KAKFJWOSFJ12NWL":                                                      "093430",
		"otpauth://totp/test-account?secret=WQIQ25BRKZYCJVYP":                  "194506",
		"OTPauth://totp/test-account?secret=WQIQ25BRKZYCJVYP":                  "194506",
		"otpauth://totp/test-account?secret=WQIQ25BRKZYCJVYP&period=60":        "730364",
		"otpauth://totp/test-account?secret=WQIQ25BRKZYCJVYP&algorithm=SHA256": "842615",
	} {
		tp, err := ParseTOTP(key)
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if code, _ := tp.Code(at); code != want {
			t.Errorf("%s = %s, want %s", key, code, want)
		}
	}
	if got := decodeBase32("ABCD123"); !bytes.Equal(got, []byte{0, 68, 61}) {
		t.Errorf("decode ABCD123 = %v", got)
	}
}

// RFC 6238's reference vectors for the three hashes, at eight digits.
func TestTOTPRFC6238(t *testing.T) {
	at := time.Unix(59, 0)
	for _, c := range []struct {
		alg    TOTPAlgorithm
		secret string
		want   string
	}{
		{TOTPSHA1, "12345678901234567890", "94287082"},
		{TOTPSHA256, "12345678901234567890123456789012", "46119246"},
		{TOTPSHA512, "1234567890123456789012345678901234567890123456789012345678901234", "90693936"},
	} {
		code, left := TOTP{Secret: []byte(c.secret), Algorithm: c.alg, Digits: 8, Period: 30}.Code(at)
		if code != c.want || left != time.Second {
			t.Errorf("alg %d = %s (%v left), want %s with 1s left", c.alg, code, left, c.want)
		}
	}
}

// The label names the account and its issuer; the issuer parameter wins
// over the label's; an otpauth URI without a secret is an error.
func TestTOTPLabelAndErrors(t *testing.T) {
	tp, err := ParseTOTP("otpauth://totp/test-issuer:test-account@example.com?secret=WQIQ25BRKZYCJVYP&issuer=other-test-issuer")
	if err != nil || tp.Account != "test-account@example.com" || tp.Issuer != "other-test-issuer" {
		t.Errorf("label = %+v, %v", tp, err)
	}
	tp, _ = ParseTOTP("otpauth://totp/test-account@example.com?secret=WQIQ25BRKZYCJVYP")
	if tp.Account != "test-account@example.com" || tp.Issuer != "" {
		t.Errorf("no issuer = %+v", tp)
	}
	if _, err := ParseTOTP("otpauth://totp/x?period=30"); !errors.Is(err, ErrMissingSecret) {
		t.Errorf("no secret = %v", err)
	}
}
