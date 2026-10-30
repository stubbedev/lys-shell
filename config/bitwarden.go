package config

import "github.com/stubbedev/lys-secrets/vault"

// BitwardenServer is where the account lives, resolved at load so a
// mistyped URL is a diagnostic for its field: `bitwarden.com`,
// `bitwarden.eu`, or a self-hosted server's (or Vaultwarden's) base
// URL, e.g. `https://vault.example.com`.
type BitwardenServer struct {
	src    string
	server vault.Server
}

// ParseBitwardenServer resolves one server setting.
func ParseBitwardenServer(s string) (BitwardenServer, error) {
	server, err := vault.ParseServer(s)
	if err != nil {
		return BitwardenServer{}, err
	}
	return BitwardenServer{src: s, server: server}, nil
}

// mustBitwardenServer builds a schema-default server; a bad literal
// panics at init.
func mustBitwardenServer(s string) BitwardenServer {
	b, err := ParseBitwardenServer(s)
	if err != nil {
		panic(err)
	}
	return b
}

// String is the setting as written.
func (b BitwardenServer) String() string { return b.src }

// Server is the parsed server.
func (b BitwardenServer) Server() vault.Server { return b.server }

// UnmarshalConfig implements Unmarshaler.
func (b *BitwardenServer) UnmarshalConfig(v any) error {
	s, ok := v.(string)
	if !ok {
		return invalidType(v, "a string")
	}
	parsed, err := ParseBitwardenServer(s)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (b BitwardenServer) MarshalConfig() any { return b.src }

func (BitwardenServer) configSchema(*schemaGen) Schema {
	return Schema{"type": "string", "description": typeDoc(typeOf[BitwardenServer]())}
}

// BitwardenConfig is the `modules.bitwarden` section.
//
// Bitwarden vault: the bar shows whether it is locked; the dropdown logs
// in, unlocks, searches the vault and copies an item's username,
// password or one-time code.
//
// The vault lives in lys-secrets (`dev.stubbe.lys.Vault1`), the one
// process holding its keys; the shell shows it and copies from it. The
// idle lock and the sync interval are lys-secrets' settings
// (`--vault-lock-after`, `--vault-sync-interval`). Copied secrets are
// marked so clipboard managers, the shell's own history included, do
// not keep them.
//
// The launcher's `vault` mode (`lys-shell launcher -show vault`) searches
// the open vault from anywhere: Enter copies an item's password (or its
// first field), Shift+Enter lists its fields to pick one. A locked vault
// asks for the master password in the launcher's masked input, and a
// protected item asks again before a secret is copied.
//
// The dropdown's generator (its header's shuffle button) lists the
// `generator-passwords` and `generator-passphrases` presets; picking one
// copies a new password or passphrase, made in the shell, as a secret.
// Passphrases draw from the EFF large wordlist (CC BY 3.0 US, by the
// Electronic Frontier Foundation).
//
// The dropdown's Sends (its header's paper plane) lists the vault's
// Bitwarden Sends, deletes them, and creates a text Send deleted after
// an hour, a day, a week or 31 days, optionally limited in opens and
// behind a password; its link, which carries the Send's key, is copied
// as a secret.
type BitwardenConfig struct {
	// Server the login form logs in to. A logged-in account keeps the
	// server it logged in to.
	Server BitwardenServer `cfg:"server"`
	// Email the login form starts with.
	Email string `cfg:"email"`
	// Lock the vault when the session locks.
	LockWithSession bool `cfg:"lock-with-session"`
	// Unlock a locked vault with the password that unlocks the lock
	// screen, once per unlock and never retried. Only useful when the
	// master password is the login password; each wrong guess costs
	// lys-secrets a full key derivation.
	UnlockWithSession bool `cfg:"unlock-with-session"`
	// Clear a copied secret from the clipboard after this many seconds,
	// unless something else was copied since. 0 leaves it there.
	ClipboardClear uint32 `cfg:"clipboard-clear"`
	// Vault folder whose logins answer NetworkManager's requests for
	// Wi-Fi, 802.1X and VPN secrets while the vault is open, before the
	// shell asks. The one login named exactly like the connection
	// profile answers: its password for the password, PSK or secret,
	// its username for the username, and a custom field of a key's name
	// for that key (`pin`, `domain`, `private-key-password`). A login
	// behind the master-password reprompt never answers, and secrets
	// NetworkManager rejected are asked for again. Empty answers nothing
	// from the vault.
	NetworkFolder string `cfg:"network-folder"`
	// Password presets the dropdown's generator offers. Generating
	// copies a new password as a secret, cleared after
	// `clipboard-clear`.
	GeneratorPasswords []BitwardenPasswordPreset `cfg:"generator-passwords"`
	// Passphrase presets the dropdown's generator offers, of words from
	// the EFF large wordlist. Generating copies a new passphrase as a
	// secret, cleared after `clipboard-clear`.
	GeneratorPassphrases []BitwardenPassphrasePreset `cfg:"generator-passphrases"`
	// Format string for the label.
	//
	// ## Placeholders
	//
	// - `{{ count }}` - Number of items in the open vault (empty while
	//   locked)
	//
	// ## Examples
	//
	// - `"{{ count }}"` - "142"
	Format string `cfg:"format"`
	// Icon while the vault is locked or logged out.
	IconLocked string `cfg:"icon-locked"`
	// Icon while the vault is open.
	IconUnlocked string `cfg:"icon-unlocked"`
	// Display border around button.
	BorderShow bool `cfg:"border-show"`
	// Border color token.
	BorderColor ColorValue `cfg:"border-color"`
	// Display module icon.
	IconShow bool `cfg:"icon-show"`
	// Icon foreground color. Auto selects based on variant for contrast.
	IconColor ColorValue `cfg:"icon-color"`
	// Icon container background color token.
	IconBgColor ColorValue `cfg:"icon-bg-color"`
	// Display label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click. Defaults to opening the vault dropdown.
	LeftClick ClickAction `cfg:"left-click"`
	// Action on right click.
	RightClick ClickAction `cfg:"right-click"`
	// Action on middle click.
	MiddleClick ClickAction `cfg:"middle-click"`
	// Action on scroll up.
	ScrollUp ClickAction `cfg:"scroll-up"`
	// Action on scroll down.
	ScrollDown ClickAction `cfg:"scroll-down"`
}

// DefaultsBitwarden returns the schema defaults.
func DefaultsBitwarden() BitwardenConfig {
	password, passphrase := DefaultsBitwardenPasswordPreset(), DefaultsBitwardenPassphrasePreset()
	password.Name, passphrase.Name = "Password", "Passphrase"
	return BitwardenConfig{
		Server:               mustBitwardenServer(vault.CloudUS),
		Email:                "",
		LockWithSession:      true,
		UnlockWithSession:    false,
		ClipboardClear:       30,
		NetworkFolder:        "",
		GeneratorPasswords:   []BitwardenPasswordPreset{password},
		GeneratorPassphrases: []BitwardenPassphrasePreset{passphrase},
		Format:               "{{ count }}",
		IconLocked:           "ld-lock-symbolic",
		IconUnlocked:         "ld-lock-open-symbolic",
		BorderShow:           false,
		BorderColor:          mustColor("blue"),
		IconShow:             true,
		IconColor:            mustColor("auto"),
		IconBgColor:          mustColor("blue"),
		LabelShow:            false,
		LabelColor:           mustColor("blue"),
		LabelMaxLength:       0,
		ButtonBgColor:        mustColor("bg-surface-elevated"),
		LeftClick:            ParseClickAction("dropdown:bitwarden"),
		RightClick:           ClickAction{},
		MiddleClick:          ClickAction{},
		ScrollUp:             ClickAction{},
		ScrollDown:           ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c BitwardenConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
