package bitwarden

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Vectors from Bitwarden's SDK tests (bitwarden-crypto master_key.rs and
// kdf.rs, bitwarden-core test_accounts.rs, bitwarden-collections): this
// client must derive and open exactly what Bitwarden's clients do.

func TestMasterKeyDerivation(t *testing.T) {
	pb, err := deriveMasterKey("67t9b5g67$%Dh89n", "test_key", Kdf{Type: KdfPBKDF2, Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{31, 79, 104, 226, 150, 71, 177, 90, 194, 80, 172, 209, 17, 129, 132, 81, 138, 167, 69, 167, 254, 149, 2, 27, 39, 197, 64, 42, 22, 195, 86, 75}
	if !bytes.Equal(pb.b, want) {
		t.Errorf("PBKDF2 master key = %v", pb.b)
	}
	ar, err := deriveMasterKey("67t9b5g67$%Dh89n", "test_key", Kdf{Type: KdfArgon2id, Iterations: 4, Memory: 32, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	want = []byte{207, 240, 225, 177, 162, 19, 163, 76, 98, 106, 179, 175, 224, 9, 17, 240, 20, 147, 237, 47, 246, 150, 141, 184, 62, 225, 131, 242, 51, 53, 225, 242}
	if !bytes.Equal(ar.b, want) {
		t.Errorf("Argon2id master key = %v", ar.b)
	}
}

// The login hash, with the email trimmed and lowercased whatever its
// case or whitespace.
func TestServerHash(t *testing.T) {
	for _, email := range []string{"test@bitwarden.com", "TEST@bitwarden.com", " test@bitwarden.com"} {
		mk, err := deriveMasterKey("asdfasdf", email, Kdf{Type: KdfPBKDF2, Iterations: 100000})
		if err != nil {
			t.Fatal(err)
		}
		if h, _ := mk.serverHash("asdfasdf"); h != "wmyadRMyBZOH7P/a/ucTCbSghKgdzDpPqUnu/DAVtSw=" {
			t.Errorf("hash for %q = %s", email, h)
		}
	}
	mk, err := deriveMasterKey("asdfasdf", "test_salt", Kdf{Type: KdfArgon2id, Iterations: 4, Memory: 32, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := mk.serverHash("asdfasdf"); h != "PR6UjYmjmppTYcdyTiNbAhPJuQQOmynKbdEl1oyi/iQ=" {
		t.Errorf("Argon2id hash = %s", h)
	}
}

// Parameters under Bitwarden's floors are refused, not derived.
func TestWeakKdfRefused(t *testing.T) {
	for _, k := range []Kdf{
		{Type: KdfPBKDF2, Iterations: 4999},
		{Type: KdfArgon2id, Iterations: 1, Memory: 16, Parallelism: 1},
		{Type: KdfArgon2id, Iterations: 2, Memory: 15, Parallelism: 1},
		{Type: KdfArgon2id, Iterations: 2, Memory: 16, Parallelism: 0},
		{Type: KdfType(7), Iterations: 600000},
	} {
		if _, err := deriveMasterKey("p", "e", k); err == nil {
			t.Errorf("%+v derived a key", k)
		}
	}
}

const (
	testUserKey    = "2.Q/2PhzcC7GdeiMHhWguYAQ==|GpqzVdr0go0ug5cZh1n+uixeBC3oC90CIe0hd/HWA/pTRDZ8ane4fmsEIcuc8eMKUt55Y2q/fbNzsYu41YTZzzsJUSeqVjT8/iTQtgnNdpo=|dwI+uyvZ1h/iZ03VQ+/wrGEFYVewBUUl/syYgjsNMbE="
	testPrivateKey = "2.yN7l00BOlUE0Sb0M//Q53w==|EwKG/BduQRQ33Izqc/ogoBROIoI5dmgrxSo82sgzgAMIBt3A2FZ9vPRMY+GWT85JiqytDitGR3TqwnFUBhKUpRRAq4x7rA6A1arHrFp5Tp1p21O3SfjtvB3quiOKbqWk6ZaU1Np9HwqwAecddFcB0YyBEiRX3VwF2pgpAdiPbSMuvo2qIgyob0CUoC/h4Bz1be7Qa7B0Xw9/fMKkB1LpOm925lzqosyMQM62YpMGkjMsbZz0uPopu32fxzDWSPr+kekNNyLt9InGhTpxLmq1go/pXR2uw5dfpXc5yuta7DB0EGBwnQ8Vl5HPdDooqOTD9I1jE0mRyuBpWTTI3FRnu3JUh3rIyGBJhUmHqGZvw2CKdqHCIrQeQkkEYqOeJRJVdBjhv5KGJifqT3BFRwX/YFJIChAQpebNQKXe/0kPivWokHWwXlDB7S7mBZzhaAPidZvnuIhalE2qmTypDwHy22FyqV58T8MGGMchcASDi/QXI6kcdpJzPXSeU9o+NC68QDlOIrMVxKFeE7w7PvVmAaxEo0YwmuAzzKy9QpdlK0aab/xEi8V4iXj4hGepqAvHkXIQd+r3FNeiLfllkb61p6WTjr5urcmDQMR94/wYoilpG5OlybHdbhsYHvIzYoLrC7fzl630gcO6t4nM24vdB6Ymg9BVpEgKRAxSbE62Tqacxqnz9AcmgItb48NiR/He3n3ydGjPYuKk/ihZMgEwAEZvSlNxYONSbYrIGDtOY+8Nbt6KiH3l06wjZW8tcmFeVlWv+tWotnTY9IqlAfvNVTjtsobqtQnvsiDjdEVtNy/s2ci5TH+NdZluca2OVEr91Wayxh70kpM6ib4UGbfdmGgCo74gtKvKSJU0rTHakQ5L9JlaSDD5FamBRyI0qfL43Ad9qOUZ8DaffDCyuaVyuqk7cz9HwmEmvWU3VQ+5t06n/5kRDXttcw8w+3qClEEdGo1KeENcnXCB32dQe3tDTFpuAIMLqwXs6FhpawfZ5kPYvLPczGWaqftIs/RXJ/EltGc0ugw2dmTLpoQhCqrcKEBDoYVk0LDZKsnzitOGdi9mOWse7Se8798ib1UsHFUjGzISEt6upestxOeupSTOh0v4+AjXbDzRUyogHww3V+Bqg71bkcMxtB+WM+pn1XNbVTyl9NR040nhP7KEf6e9ruXAtmrBC2ah5cFEpLIot77VFZ9ilLuitSz+7T8n1yAh1IEG6xxXxninAZIzi2qGbH69O5RSpOJuJTv17zTLJQIIc781JwQ2TTwTGnx5wZLbffhCasowJKd2EVcyMJyhz6ru0PvXWJ4hUdkARJs3Xu8dus9a86N8Xk6aAPzBDqzYb1vyFIfBxP0oO8xFHgd30Cgmz8UrSE3qeWRrF8ftrI6xQnFjHBGWD/JWSvd6YMcQED0aVuQkuNW9ST/DzQThPzRfPUoiL10yAmV7Ytu4fR3x2sF0Yfi87YhHFuCMpV/DsqxmUizyiJuD938eRcH8hzR/VO53Qo3UIsqOLcyXtTv6THjSlTopQ+JOLOnHm1w8dzYbLN44OG44rRsbihMUQp+wUZ6bsI8rrOnm9WErzkbQFbrfAINdoCiNa6cimYIjvvnMTaFWNymqY1vZxGztQiMiHiHYwTfwHTXrb9j0uPM=|09J28iXv9oWzYtzK2LBT6Yht4IT4MijEkk0fwFdrVQ4="
	testOrgKey     = "4.rY01mZFXHOsBAg5Fq4gyXuklWfm6mQASm42DJpx05a+e2mmp+P5W6r54WU2hlREX0uoTxyP91bKKwickSPdCQQ58J45LXHdr9t2uzOYyjVzpzebFcdMw1eElR9W2DW8wEk9+mvtWvKwu7yTebzND+46y1nRMoFydi5zPVLSlJEf81qZZ4Uh1UUMLwXz+NRWfixnGXgq2wRq1bH0n3mqDhayiG4LJKgGdDjWXC8W8MMXDYx24SIJrJu9KiNEMprJE+XVF9nQVNijNAjlWBqkDpsfaWTUfeVLRLctfAqW1blsmIv4RQ91PupYJZDNc8nO9ZTF3TEVM+2KHoxzDJrLs2Q=="
	testOrgName    = "2.EI9Km5BfrIqBa1W+WCccfA==|laWxNnx+9H3MZww4zm7cBSLisjpi81zreaQntRhegVI=|x42+qKFf5ga6DIL0OW5pxCdLrC/gm8CXJvf3UASGteI="
	testOrgID      = "1bc9ac1e-f5aa-45f2-94bf-b181009709b8"
)

// The whole hierarchy of the test@bitwarden.com account: the master
// password opens the user key, the user key the RSA private key, the
// private key the organization key, and that key a collection name.
func TestUnlockOpensTheHierarchy(t *testing.T) {
	kr, err := Unlock(UnlockInput{
		Password: "asdfasdfasdf", Email: "test@bitwarden.com",
		Kdf:     Kdf{Type: KdfPBKDF2, Iterations: 600000},
		UserKey: testUserKey, PrivateKey: testPrivateKey,
		OrgKeys: map[string]string{testOrgID: testOrgKey},
	})
	if err != nil {
		t.Fatal(err)
	}
	name, err := kr.Orgs[testOrgID].DecryptString(testOrgName)
	if err != nil || name != "Default collection" {
		t.Fatalf("collection name = %q, %v", name, err)
	}
	kr.Wipe()
	if len(kr.Orgs) != 0 || kr.private != nil || !bytes.Equal(kr.User.enc, make([]byte, 32)) {
		t.Error("Wipe left key material")
	}
}

// A wrong master password is ErrWrongPassword, for the modern wrapping
// (its MAC fails) and the legacy one (no MAC: its padding or length).
func TestWrongPassword(t *testing.T) {
	_, err := Unlock(UnlockInput{Password: "wrong", Email: "test@bitwarden.com", Kdf: Kdf{Type: KdfPBKDF2, Iterations: 600000}, UserKey: testUserKey})
	if !errors.Is(err, ErrWrongPassword) {
		t.Errorf("modern = %v", err)
	}
	legacy := "0.8UClLa8IPE1iZT7chy5wzQ==|6PVfHnVk5S3XqEtQemnM5yb4JodxmPkkWzmDRdfyHtjORmvxqlLX40tBJZ+CKxQWmS8tpEB5w39rbgHg/gqs0haGdZG4cPbywsgGzxZ7uNI="
	kr, err := Unlock(UnlockInput{Password: "asdfasdfasdf", Email: "legacy@bitwarden.com", Kdf: Kdf{Type: KdfPBKDF2, Iterations: 600000}, UserKey: legacy})
	if err != nil {
		t.Fatalf("legacy user key: %v", err)
	}
	wantEnc := []byte{12, 95, 151, 203, 37, 4, 236, 67, 137, 97, 90, 58, 6, 127, 242, 28, 209, 168, 125, 29, 118, 24, 213, 44, 117, 202, 2, 115, 132, 165, 125, 148}
	if !bytes.Equal(kr.User.enc, wantEnc) {
		t.Errorf("legacy user key enc = %v", kr.User.enc)
	}
	if _, err := Unlock(UnlockInput{Password: "nope", Email: "legacy@bitwarden.com", Kdf: Kdf{Type: KdfPBKDF2, Iterations: 600000}, UserKey: legacy}); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("legacy wrong password = %v", err)
	}
}

// A tampered byte fails the MAC instead of decrypting to garbage, and a
// legacy unauthenticated string is refused under a key with a MAC key.
func TestDecryptRefusesTamperingAndDowngrade(t *testing.T) {
	kr, err := Unlock(UnlockInput{Password: "asdfasdfasdf", Email: "test@bitwarden.com", Kdf: Kdf{Type: KdfPBKDF2, Iterations: 600000}, UserKey: testUserKey, PrivateKey: testPrivateKey, OrgKeys: map[string]string{testOrgID: testOrgKey}})
	if err != nil {
		t.Fatal(err)
	}
	org := kr.Orgs[testOrgID]
	e, err := ParseEncString(testOrgName)
	if err != nil {
		t.Fatal(err)
	}
	e.Data[0] ^= 1
	if _, err := org.Decrypt(e); !errors.Is(err, ErrMAC) {
		t.Errorf("tampered = %v, want ErrMAC", err)
	}
	if _, err := org.Decrypt(EncString{Type: EncAesCbc256B64, IV: make([]byte, 16), Data: make([]byte, 16)}); err == nil {
		t.Error("a type 0 string opened under an authenticated key")
	}
	if s := org.String() + kr.String(); strings.Contains(s, "0x") || !strings.Contains(s, "redacted") {
		t.Errorf("keys format as %q", s)
	}
}

// Malformed strings are errors naming the problem; an unknown type is
// ErrUnsupportedEncType.
func TestParseEncString(t *testing.T) {
	for _, s := range []string{
		"2.AAECAw==|Y3Q=|AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"2.AAECAwQFBgcICQoLDA0ODw==|Y3Q=|AAECAw==",
		"0.AAECAw==|Y3Q=",
		"2.!!!!|Y3Q=|AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"2.AAECAwQFBgcICQoLDA0ODw==|Y3Q=",
		"nodot",
		"x.abc",
	} {
		if _, err := ParseEncString(s); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
	if _, err := ParseEncString("7.abc"); !errors.Is(err, ErrUnsupportedEncType) {
		t.Errorf("type 7 = %v", err)
	}
	if e, err := ParseEncString(testOrgName); err != nil || e.Type != EncAesCbc256HmacSha256B64 || len(e.IV) != 16 || len(e.MAC) != 32 {
		t.Errorf("a valid string = %+v, %v", e, err)
	}
}
