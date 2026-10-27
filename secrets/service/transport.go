package service

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// The transport algorithms OpenSession negotiates.
const (
	algPlain = "plain"
	algDH    = "dh-ietf1024-sha256-aes128-cbc-pkcs7"
)

// dhPrime is the 1024-bit MODP group of RFC 2409 §6.2 (Oakley group 2),
// generator 2, the group the spec names for its DH transport.
var dhPrime, _ = new(big.Int).SetString(
	"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1"+
		"29024E088A67CC74020BBEA63B139B22514A08798E3404DD"+
		"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245"+
		"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED"+
		"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE65381"+
		"FFFFFFFFFFFFFFFF", 16)

var dhGenerator = big.NewInt(2)

const dhPrimeLen = 128

// transport encrypts secrets on their way to a client and decrypts them
// on the way in. plain is the identity.
type transport interface {
	encode(secret []byte) (params, value []byte, err error)
	decode(params, value []byte) ([]byte, error)
}

type plainTransport struct{}

func (plainTransport) encode(secret []byte) ([]byte, []byte, error) {
	return []byte{}, bytes.Clone(secret), nil
}

func (plainTransport) decode(params, value []byte) ([]byte, error) {
	if len(params) != 0 {
		return nil, fmt.Errorf("a plain session's secret has %d bytes of parameters, want none", len(params))
	}
	return bytes.Clone(value), nil
}

// aesTransport is the DH session: AES-128-CBC with PKCS#7 padding under
// the HKDF-SHA256 of the shared secret, a fresh IV per secret as its
// parameters.
type aesTransport struct{ key []byte }

// maxDHTries bounds the private keys negotiateDH draws; each is
// rejected with probability 1/256.
const maxDHTries = 64

// negotiateDH answers a client's public key: the server's public key to
// send back, and the session's transport. The shared secret is stretched
// with HKDF-SHA256 (no salt, no info) to an AES-128 key.
//
// Clients disagree on the secret's encoding: libsecret built on libgcrypt,
// gnome-keyring and python-secretstorage pad it to the prime's length,
// libsecret built on GnuTLS drops its leading zero bytes. The private key
// is drawn until the secret has no leading zero byte, where the two
// encodings are the same bytes, so every client derives this key.
func negotiateDH(peer []byte, random io.Reader) (public []byte, t transport, err error) {
	y := new(big.Int).SetBytes(peer)
	pMinus1 := new(big.Int).Sub(dhPrime, big.NewInt(1))
	if y.Cmp(big.NewInt(1)) <= 0 || y.Cmp(pMinus1) >= 0 {
		return nil, nil, errors.New("the client's DH public key is out of range")
	}
	var priv, k *big.Int
	for range maxDHTries {
		if priv, err = rand.Int(random, pMinus1); err != nil {
			return nil, nil, fmt.Errorf("DH private key: %w", err)
		}
		priv.Add(priv, big.NewInt(1))
		k = new(big.Int).Exp(y, priv, dhPrime)
		if k.BitLen() > 8*(dhPrimeLen-1) {
			break
		}
		k = nil
	}
	if k == nil {
		return nil, nil, fmt.Errorf("no DH private key in %d draws gives a full-length secret", maxDHTries)
	}
	public = new(big.Int).Exp(dhGenerator, priv, dhPrime).Bytes()
	shared := k.FillBytes(make([]byte, dhPrimeLen))
	defer clear(shared)
	key, err := hkdf.Key(sha256.New, shared, nil, "", aes.BlockSize)
	if err != nil {
		return nil, nil, fmt.Errorf("HKDF: %w", err)
	}
	return public, aesTransport{key: key}, nil
}

func (a aesTransport) encode(secret []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return nil, nil, err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, nil, fmt.Errorf("IV: %w", err)
	}
	pad := aes.BlockSize - len(secret)%aes.BlockSize
	buf := make([]byte, len(secret)+pad)
	copy(buf, secret)
	for i := len(secret); i < len(buf); i++ {
		buf[i] = byte(pad)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(buf, buf)
	return iv, buf, nil
}

func (a aesTransport) decode(params, value []byte) ([]byte, error) {
	if len(params) != aes.BlockSize {
		return nil, fmt.Errorf("the secret's IV is %d bytes, want %d", len(params), aes.BlockSize)
	}
	if len(value) == 0 || len(value)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("the secret's ciphertext is %d bytes, not a whole number of blocks", len(value))
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return nil, err
	}
	buf := bytes.Clone(value)
	cipher.NewCBCDecrypter(block, params).CryptBlocks(buf, buf)
	pad := int(buf[len(buf)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(buf) {
		clear(buf)
		return nil, errors.New("the secret's padding is invalid")
	}
	for _, b := range buf[len(buf)-pad:] {
		if int(b) != pad {
			clear(buf)
			return nil, errors.New("the secret's padding is invalid")
		}
	}
	return buf[:len(buf)-pad], nil
}
