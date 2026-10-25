package config

import "github.com/stubbedev/lys-shell/service/bitwarden"

// BitwardenServer is where the account lives, resolved at load so a
// mistyped URL is a diagnostic for its field: `bitwarden.com`,
// `bitwarden.eu`, or a self-hosted server's (or Vaultwarden's) base
// URL, e.g. `https://vault.example.com`.
type BitwardenServer struct {
	src    string
	server bitwarden.Server
}

// ParseBitwardenServer resolves one server setting.
func ParseBitwardenServer(s string) (BitwardenServer, error) {
	server, err := bitwarden.ParseServer(s)
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

// Server is the resolved endpoints.
func (b BitwardenServer) Server() bitwarden.Server { return b.server }

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
// The shell keeps only the session (the server, the email and the
// refresh token) under `$XDG_STATE_HOME/lys-shell/bitwarden`, never the
// master password, a key or an item. Copied secrets are marked so
// clipboard managers, the shell's own history included, do not keep
// them.
type BitwardenConfig struct {
	// Server the login form logs in to. A logged-in account keeps the
	// server it logged in to.
	Server BitwardenServer `cfg:"server"`
	// Email the login form starts with.
	Email string `cfg:"email"`
	// Lock the vault after this many minutes without a copy. 0 keeps it
	// open until locked by hand.
	LockAfter uint32 `cfg:"lock-after"`
	// Clear a copied secret from the clipboard after this many seconds,
	// unless something else was copied since. 0 leaves it there.
	ClipboardClear uint32 `cfg:"clipboard-clear"`
	// Sync the open vault every this many minutes. 0 syncs only on
	// unlock and by hand.
	SyncInterval uint32 `cfg:"sync-interval"`
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
	return BitwardenConfig{
		Server:         mustBitwardenServer(bitwarden.CloudUS),
		Email:          "",
		LockAfter:      15,
		ClipboardClear: 30,
		SyncInterval:   30,
		Format:         "{{ count }}",
		IconLocked:     "ld-lock-symbolic",
		IconUnlocked:   "ld-lock-open-symbolic",
		BorderShow:     false,
		BorderColor:    mustColor("blue"),
		IconShow:       true,
		IconColor:      mustColor("auto"),
		IconBgColor:    mustColor("blue"),
		LabelShow:      false,
		LabelColor:     mustColor("blue"),
		LabelMaxLength: 0,
		ButtonBgColor:  mustColor("bg-surface-elevated"),
		LeftClick:      ParseClickAction("dropdown:bitwarden"),
		RightClick:     ClickAction{},
		MiddleClick:    ClickAction{},
		ScrollUp:       ClickAction{},
		ScrollDown:     ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c BitwardenConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}
