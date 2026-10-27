package service

import (
	"bytes"
	"crypto/aes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math/big"
	mrand "math/rand/v2"
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
	server, tr, err := negotiateDH(public, rand.Reader)
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
		if _, _, err := negotiateDH(peer, rand.Reader); err == nil {
			t.Errorf("public key %x accepted", peer)
		}
	}
	_, public := clientDH(t)
	_, tr, _ := negotiateDH(public, rand.Reader)
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

// unpaddedKey is the client half the way libsecret on GnuTLS derives it:
// the shared secret without its leading zero bytes.
func unpaddedKey(t *testing.T, priv *big.Int, server []byte) []byte {
	t.Helper()
	shared := new(big.Int).Exp(new(big.Int).SetBytes(server), priv, dhPrime).Bytes()
	key, err := hkdf.Key(sha256.New, shared, nil, "", aes.BlockSize)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// A first private key whose shared secret starts with a zero byte is
// drawn again: clients that pad the secret and clients that strip it
// then derive the same key. The seed below makes the first draw such a
// key for this client; without the redraw the two derivations differ.
func TestDHSecretIsTheSameBytesPaddedOrNot(t *testing.T) {
	client := new(big.Int).SetBytes(bytes.Repeat([]byte{0x5a}, 100))
	public := new(big.Int).Exp(big.NewInt(2), client, dhPrime).Bytes()
	pMinus1 := new(big.Int).Sub(dhPrime, big.NewInt(1))
	firstDrawShort := func(seed uint64) bool {
		priv, err := rand.Int(seeded(seed), pMinus1)
		if err != nil {
			t.Fatal(err)
		}
		priv.Add(priv, big.NewInt(1))
		return new(big.Int).Exp(new(big.Int).SetBytes(public), priv, dhPrime).BitLen() <= 8*(dhPrimeLen-1)
	}
	seed := uint64(0)
	for !firstDrawShort(seed) {
		seed++
	}
	server, tr, err := negotiateDH(public, seeded(seed))
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string][]byte{"padded": clientKey(t, client, server), "stripped": unpaddedKey(t, client, server)} {
		params, value, err := tr.encode([]byte("s3cret"))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := (aesTransport{key: key}).decode(params, value); err != nil || string(got) != "s3cret" {
			t.Errorf("a %s-secret client decodes %q, %v", name, got, err)
		}
	}
}

func seeded(seed uint64) io.Reader {
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:], seed)
	return mrand.NewChaCha8(key)
}

func FuzzAESDecode(f *testing.F) {
	key := make([]byte, aes.BlockSize)
	f.Add(make([]byte, 16), make([]byte, 32))
	f.Fuzz(func(t *testing.T, params, value []byte) {
		_, _ = aesTransport{key: key}.decode(params, value)
	})
}
