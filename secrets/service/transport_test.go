package service

import (
	"crypto/aes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"math/big"
	"strings"
	"testing"
)

// clientDH is the client half, written from the spec the way
// python-secretstorage does it, independent of negotiateDH.
func clientDH(t *testing.T) (priv *big.Int, public []byte) {
	t.Helper()
	priv, err := rand.Int(rand.Reader, dhPrime)
	if err != nil {
		t.Fatal(err)
	}
	return priv, new(big.Int).Exp(big.NewInt(2), priv, dhPrime).Bytes()
}

func clientKey(t *testing.T, priv *big.Int, server []byte) []byte {
	t.Helper()
	shared := new(big.Int).Exp(new(big.Int).SetBytes(server), priv, dhPrime).Bytes()
	padded := append(make([]byte, dhPrimeLen-len(shared)), shared...)
	key, err := hkdf.Key(sha256.New, padded, make([]byte, sha256.Size), "", aes.BlockSize)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestDHAgreesWithAnIndependentClient(t *testing.T) {
	priv, public := clientDH(t)
	server, tr, err := negotiateDH(public)
	if err != nil {
		t.Fatal(err)
	}
	client := aesTransport{key: clientKey(t, priv, server)}
	for _, secret := range []string{"", "x", "exactly sixteen!", strings.Repeat("long secret ", 20)} {
		params, value, err := tr.encode([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		if len(params) != aes.BlockSize || len(value)%aes.BlockSize != 0 || len(value) <= len(secret)-aes.BlockSize {
			t.Fatalf("%q: params %d value %d bytes", secret, len(params), len(value))
		}
		got, err := client.decode(params, value)
		if err != nil || string(got) != secret {
			t.Errorf("client decodes %q, %v; want %q", got, err, secret)
		}
		params, value, _ = client.encode([]byte(secret))
		if back, err := tr.decode(params, value); err != nil || string(back) != secret {
			t.Errorf("server decodes %q, %v; want %q", back, err, secret)
		}
	}
}

func TestDHRejectsBadInput(t *testing.T) {
	for _, peer := range [][]byte{{}, {1}, new(big.Int).Sub(dhPrime, big.NewInt(1)).Bytes(), dhPrime.Bytes()} {
		if _, _, err := negotiateDH(peer); err == nil {
			t.Errorf("public key %x accepted", peer)
		}
	}
	_, public := clientDH(t)
	_, tr, _ := negotiateDH(public)
	for _, tc := range []struct {
		name          string
		params, value []byte
	}{
		{"short IV", make([]byte, 8), make([]byte, 16)},
		{"empty", make([]byte, 16), nil},
		{"partial block", make([]byte, 16), make([]byte, 20)},
	} {
		if _, err := tr.decode(tc.params, tc.value); err == nil {
			t.Errorf("%s accepted", tc.name)
		}
	}
	if _, err := (plainTransport{}).decode([]byte{1}, []byte("x")); err == nil {
		t.Error("plain secret with parameters accepted")
	}
}

func FuzzAESDecode(f *testing.F) {
	key := make([]byte, aes.BlockSize)
	f.Add(make([]byte, 16), make([]byte, 32))
	f.Fuzz(func(t *testing.T, params, value []byte) {
		_, _ = aesTransport{key: key}.decode(params, value)
	})
}
