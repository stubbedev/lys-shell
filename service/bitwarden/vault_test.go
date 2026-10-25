package bitwarden

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"slices"
	"testing"
)

// seal encrypts plain as a type 2 EncString under key, as Bitwarden's
// clients do; decryption is pinned to Bitwarden's vectors, so the tests
// may encrypt their fixtures.
func seal(t *testing.T, key SymmetricKey, plain []byte) string {
	t.Helper()
	block, err := aes.NewCipher(key.enc)
	if err != nil {
		t.Fatal(err)
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(append([]byte(nil), plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := bytes.Repeat([]byte{7}, aes.BlockSize)
	out := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
	m := hmac.New(sha256.New, key.mac)
	m.Write(iv)
	m.Write(out)
	b64 := base64.StdEncoding.EncodeToString
	return "2." + b64(iv) + "|" + b64(out) + "|" + b64(m.Sum(nil))
}

func testKey(fill byte) SymmetricKey {
	k, _ := symmetricKeyFrom(bytes.Repeat([]byte{fill}, 64))
	return k
}

// Items open under the user key, an organization's key, or their own
// item key; deleted items are left out; favorites sort first, then by
// name; folder names resolve.
func TestDecryptVault(t *testing.T) {
	user, org, item := testKey(1), testKey(2), testKey(3)
	kr := &Keyring{User: user, Orgs: map[string]SymmetricKey{"o1": org}}
	s := func(k SymmetricKey, v string) string { return seal(t, k, []byte(v)) }
	deleted := "2026-01-01"
	sync := SyncResponse{
		Folders: []SyncFolder{{ID: "f1", Name: s(user, "Work")}},
		Ciphers: []SyncCipher{
			{ID: "c1", Type: ItemLogin, FolderID: "f1", Name: s(user, "zeta mail"), Login: &SyncLogin{Username: s(user, "ada"), Password: s(user, "hunter2"), Totp: s(user, "JBSWY3DPEHPK3PXP"), Uris: []struct{ URI string }{{URI: s(user, "https://mail.example.com")}}}},
			{ID: "c2", Type: ItemLogin, OrganizationID: "o1", Favorite: true, Name: s(org, "Shared VPN"), Login: &SyncLogin{Password: s(org, "s3cret")}},
			{ID: "c3", Type: ItemSecureNote, Key: s(user, string(bytes.Repeat([]byte{3}, 64))), Name: s(item, "alpha note"), Notes: s(item, "body")},
			{ID: "c4", Type: ItemLogin, DeletedDate: &deleted, Name: s(user, "gone")},
			{ID: "c5", Type: ItemCard, Name: s(user, "Visa"), Card: &SyncCard{Number: s(user, "4111111111111111"), Code: s(user, "123")}, Fields: []SyncField{{Name: s(user, "pin"), Value: s(user, "0000"), Type: 1}}},
		},
	}
	v := DecryptVault(kr, sync)
	var names []string
	for _, it := range v.Items {
		names = append(names, it.Name)
		if it.Undecryptable {
			t.Errorf("%s is undecryptable", it.Name)
		}
	}
	if want := []string{"Shared VPN", "alpha note", "Visa", "zeta mail"}; !slices.Equal(names, want) {
		t.Errorf("items = %v, want %v (favorite first, then by name, trash left out)", names, want)
	}
	mail := v.Items[3]
	if mail.Username != "ada" || mail.Password != "hunter2" || mail.TOTP != "JBSWY3DPEHPK3PXP" || mail.Folder != "Work" || len(mail.URIs) != 1 {
		t.Errorf("login = %+v", mail)
	}
	if v.Items[1].Notes != "body" {
		t.Errorf("item-key note = %+v", v.Items[1])
	}
	if card := v.Items[2]; card.Card.Number != "4111111111111111" || card.Card.Code != "123" || len(card.Fields) != 1 || !card.Fields[0].Hidden || card.Fields[0].Value != "0000" {
		t.Errorf("card = %+v", card)
	}
	v.Wipe()
	if len(v.Items) != 0 || len(v.Folders) != 0 {
		t.Error("Wipe kept plaintext")
	}
}

// A field under the wrong key, or an item of an organization whose key
// is missing, marks the item undecryptable and keeps the rest of the
// sync.
func TestUndecryptableItems(t *testing.T) {
	user, other := testKey(1), testKey(9)
	kr := &Keyring{User: user, Orgs: map[string]SymmetricKey{}}
	v := DecryptVault(kr, SyncResponse{Ciphers: []SyncCipher{
		{ID: "ok", Type: ItemLogin, Name: seal(t, user, []byte("fine")), Login: &SyncLogin{Password: seal(t, other, []byte("x"))}},
		{ID: "orphan", Type: ItemLogin, OrganizationID: "missing", Name: seal(t, user, []byte("n"))},
		{ID: "garbled", Type: ItemLogin, Name: "9.what"},
	}})
	if len(v.Items) != 3 {
		t.Fatalf("items = %d, want all three kept", len(v.Items))
	}
	for _, it := range v.Items {
		if !it.Undecryptable {
			t.Errorf("%s decrypted", it.ID)
		}
	}
	for _, it := range v.Items {
		if it.ID == "ok" && (it.Name != "fine" || it.Password != "") {
			t.Errorf("partly decryptable item = %+v", it)
		}
	}
}

// Search matches every word against the name, username, URIs and
// folder, ignoring case.
func TestItemMatch(t *testing.T) {
	it := Item{Name: "Mail", Username: "ada@example.com", URIs: []string{"https://mail.example.com"}, Folder: "Work"}
	for q, want := range map[string]bool{"": true, "mail": true, "MAIL work": true, "ada example": true, "bank": false, "mail bank": false} {
		if got := it.Match(q); got != want {
			t.Errorf("Match(%q) = %v", q, got)
		}
	}
}
