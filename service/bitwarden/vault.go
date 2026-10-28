package bitwarden

import (
	"slices"
	"strings"
)

// SyncResponse is /sync's answer, the encrypted account.
type SyncResponse struct {
	Profile     SyncProfile
	Folders     []SyncFolder
	Collections []SyncCollection
	Ciphers     []SyncCipher
}

// SyncProfile carries the wrapped keys: the user key under the master
// key, the private key under the user key, each organization's key
// under the private key.
type SyncProfile struct {
	Email         string
	Key           string
	PrivateKey    string
	Organizations []SyncOrganization
}

// SyncOrganization is a membership: the organization and its wrapped
// key.
type SyncOrganization struct {
	ID, Name, Key string
}

// SyncFolder is a folder, its name encrypted under the user key.
type SyncFolder struct {
	ID, Name string
}

// SyncCollection is an organization collection, its name encrypted
// under the organization key.
type SyncCollection struct {
	ID, OrganizationID, Name string
}

// SyncCipher is one encrypted vault item.
type SyncCipher struct {
	ID             string
	OrganizationID string
	FolderID       string
	Type           ItemType
	Name           string
	Notes          string
	Favorite       bool
	Key            string
	Reprompt       int
	DeletedDate    *string
	Login          *SyncLogin
	Card           *SyncCard
	Identity       *SyncIdentity
	Fields         []SyncField
}

// SyncLogin is a login's encrypted fields.
type SyncLogin struct {
	Username, Password, Totp string
	Uris                     []struct{ URI string }
}

// SyncCard is a card's encrypted fields.
type SyncCard struct {
	CardholderName, Brand, Number, ExpMonth, ExpYear, Code string
}

// SyncIdentity is an identity's encrypted fields this client shows.
type SyncIdentity struct {
	Title, FirstName, MiddleName, LastName, Email, Phone, Username, Company string
}

// SyncField is a custom field; its type says whether the value is
// hidden (1) or plain (0, 2 boolean, 3 linked).
type SyncField struct {
	Name, Value string
	Type        int
}

// ItemType is a cipher's kind.
type ItemType int

// The cipher types.
const (
	ItemLogin      ItemType = 1
	ItemSecureNote ItemType = 2
	ItemCard       ItemType = 3
	ItemIdentity   ItemType = 4
	ItemSSHKey     ItemType = 5
)

const hiddenFieldType = 1

// Item is a decrypted vault item. Undecryptable marks one any of whose
// fields failed to open (a key it was not shared with, an EncString
// type this client does not speak); the rest still shows.
type Item struct {
	ID            string
	Type          ItemType
	Name          string
	Notes         string
	Folder        string
	Organization  string
	Favorite      bool
	Reprompt      bool
	Username      string
	Password      string
	TOTP          string
	URIs          []string
	Card          Card
	Identity      Identity
	Fields        []Field
	Undecryptable bool
}

// Card is a decrypted card.
type Card struct {
	Cardholder, Brand, Number, ExpMonth, ExpYear, Code string
}

// Identity is a decrypted identity's shown fields.
type Identity struct {
	Title, First, Middle, Last, Email, Phone, Username, Company string
}

// Field is a decrypted custom field.
type Field struct {
	Name, Value string
	Hidden      bool
}

// Vault is the decrypted account: its items, favorites first then by
// name, and folder names by id. Deleted items (the trash) are left out.
type Vault struct {
	Items   []Item
	Folders map[string]string
}

// Wipe drops the plaintext; the vault is empty after.
func (v *Vault) Wipe() {
	clear(v.Items)
	v.Items = nil
	clear(v.Folders)
}

// DecryptVault opens a sync under the keyring. A field that does not
// open marks its item Undecryptable rather than failing the sync.
func DecryptVault(kr *Keyring, s SyncResponse) Vault {
	v := Vault{Folders: make(map[string]string, len(s.Folders))}
	for _, f := range s.Folders {
		name, ok := open(kr.User, f.Name)
		if !ok {
			name = f.ID
		}
		v.Folders[f.ID] = name
	}
	for _, c := range s.Ciphers {
		if c.DeletedDate != nil {
			continue
		}
		v.Items = append(v.Items, decryptCipher(kr, c, v.Folders))
	}
	slices.SortStableFunc(v.Items, func(a, b Item) int {
		if a.Favorite != b.Favorite {
			if a.Favorite {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return v
}

// decryptCipher opens one item under its organization's key or the
// user key, and under its own item key when it has one.
func decryptCipher(kr *Keyring, c SyncCipher, folders map[string]string) Item {
	it := Item{ID: c.ID, Type: c.Type, Favorite: c.Favorite, Reprompt: c.Reprompt != 0, Folder: folders[c.FolderID], Organization: c.OrganizationID}
	key := kr.User
	if c.OrganizationID != "" {
		org, ok := kr.Orgs[c.OrganizationID]
		if !ok {
			it.Name, it.Undecryptable = c.ID, true
			return it
		}
		key = org
	}
	if c.Key != "" {
		e, err := ParseEncString(c.Key)
		if err != nil {
			it.Name, it.Undecryptable = c.ID, true
			return it
		}
		raw, err := key.Decrypt(e)
		if err != nil {
			it.Name, it.Undecryptable = c.ID, true
			return it
		}
		itemKey, err := symmetricKeyFrom(raw)
		wipe(raw)
		if err != nil {
			it.Name, it.Undecryptable = c.ID, true
			return it
		}
		defer itemKey.Wipe()
		key = itemKey
	}
	field := func(s string) string {
		if s == "" {
			return ""
		}
		v, ok := open(key, s)
		if !ok {
			it.Undecryptable = true
		}
		return v
	}
	it.Name = field(c.Name)
	if it.Name == "" {
		it.Name = c.ID
	}
	it.Notes = field(c.Notes)
	if l := c.Login; l != nil {
		it.Username, it.Password, it.TOTP = field(l.Username), field(l.Password), field(l.Totp)
		for _, u := range l.Uris {
			if s := field(u.URI); s != "" {
				it.URIs = append(it.URIs, s)
			}
		}
	}
	if cd := c.Card; cd != nil {
		it.Card = Card{Cardholder: field(cd.CardholderName), Brand: field(cd.Brand), Number: field(cd.Number), ExpMonth: field(cd.ExpMonth), ExpYear: field(cd.ExpYear), Code: field(cd.Code)}
	}
	if id := c.Identity; id != nil {
		it.Identity = Identity{Title: field(id.Title), First: field(id.FirstName), Middle: field(id.MiddleName), Last: field(id.LastName), Email: field(id.Email), Phone: field(id.Phone), Username: field(id.Username), Company: field(id.Company)}
	}
	for _, f := range c.Fields {
		it.Fields = append(it.Fields, Field{Name: field(f.Name), Value: field(f.Value), Hidden: f.Type == hiddenFieldType})
	}
	return it
}

// open decrypts one EncString field, false when it does not open.
func open(key SymmetricKey, s string) (string, bool) {
	v, err := key.DecryptString(s)
	return v, err == nil
}

// Match reports whether the item matches every word of query, in its
// name, username, URIs or folder, ignoring case.
func (it Item) Match(query string) bool {
	hay := strings.ToLower(strings.Join(append([]string{it.Name, it.Username, it.Folder}, it.URIs...), "\n"))
	for w := range strings.FieldsSeq(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// NetworkLogin is the login stored for the network connection named
// name: the one login item in folder with that exact name. None, two of
// them, or one protected by the master-password reprompt or not fully
// decrypted is no answer: a network secret is sent without asking, so
// it must be unambiguous and unprotected.
func NetworkLogin(items []Item, folder, name string) (Item, bool) {
	var found Item
	matches := 0
	for _, it := range items {
		if it.Type == ItemLogin && it.Folder == folder && it.Name == name {
			found = it
			matches++
		}
	}
	if matches != 1 || found.Reprompt || found.Undecryptable {
		return Item{}, false
	}
	return found, true
}
