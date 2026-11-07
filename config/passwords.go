package config

// PasswordsConfig is the passwords section: how lys-settings' Passwords
// page shows what the keyring holds.
//
// The Passwords page over the keyring.
type PasswordsConfig struct {
	// Minutes after which revealing a secret asks for the collection's
	// password again, counted from when the page last saw it unlocked; 0
	// never asks again.
	RevealRepromptMinutes uint32 `cfg:"reveal-reprompt-minutes"`
}

// DefaultsPasswords returns the schema defaults.
func DefaultsPasswords() PasswordsConfig { return PasswordsConfig{} }
