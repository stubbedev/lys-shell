### lys-shell Configuration - Bitwarden Module

## Bitwarden Module Configuration

settings-modules-bitwarden-server = Server
    .description = bitwarden.com, bitwarden.eu, or a self-hosted server's base URL. A logged-in account keeps the server it logged in to

settings-modules-bitwarden-email = Email
    .description = Email the login form starts with

settings-modules-bitwarden-lock-with-session = Lock With the Session
    .description = Lock the vault when the session locks

settings-modules-bitwarden-unlock-with-session = Unlock With the Session
    .description = Unlock a locked vault with the lock screen's password, when it is the master password

settings-modules-bitwarden-clipboard-clear = Clear Clipboard After (seconds)
    .description = Clear a copied secret after this many seconds unless something else was copied. 0 leaves it

settings-modules-bitwarden-network-folder = Network Folder
    .description = Vault folder whose logins, named like the connection, answer Wi-Fi and VPN password requests while the vault is open. Empty answers none

settings-modules-bitwarden-portal-folder = Portal Secrets Folder
    .description = Vault folder sandboxed apps' keyring keys are kept in, after you allow each. Empty keeps them in the keyring only

settings-modules-bitwarden-generator-passwords = Password Presets
    .description = Random-character passwords the dropdown's generator copies

settings-modules-bitwarden-generator-passphrases = Passphrase Presets
    .description = Passphrases of words from the EFF large wordlist the dropdown's generator copies

enum-password-charset-lowercase = Lowercase (a-z)
enum-password-charset-uppercase = Uppercase (A-Z)
enum-password-charset-digits = Digits (0-9)
enum-password-charset-symbols = Symbols (!@#$%^&*)

settings-modules-bitwarden-format = Format
    .description = Label format string. Placeholders: {"{{ count }}"} (items in the open vault)

settings-modules-bitwarden-icon-locked = Locked Icon
    .description = Icon while the vault is locked or logged out

settings-modules-bitwarden-icon-unlocked = Unlocked Icon
    .description = Icon while the vault is open

settings-modules-bitwarden-border-show = Show Border
    .description = Display border around button

settings-modules-bitwarden-border-color = Border Color
    .description = Border color token

settings-modules-bitwarden-icon-show = Show Icon
    .description = Display module icon

settings-modules-bitwarden-icon-color = Icon Color
    .description = Icon foreground color

settings-modules-bitwarden-icon-bg-color = Icon Background
    .description = Icon container background color

settings-modules-bitwarden-label-show = Show Label
    .description = Display label

settings-modules-bitwarden-label-color = Label Color
    .description = Label text color

settings-modules-bitwarden-label-max-length = Label Max Length
    .description = Max characters before truncation

settings-modules-bitwarden-button-bg-color = Button Background
    .description = Button background color

settings-modules-bitwarden-left-click = Left Click
    .description = Action on left click. Defaults to opening the vault dropdown

settings-modules-bitwarden-right-click = Right Click
    .description = Shell command on right click

settings-modules-bitwarden-middle-click = Middle Click
    .description = Shell command on middle click

settings-modules-bitwarden-scroll-up = Scroll Up
    .description = Shell command on scroll up

settings-modules-bitwarden-scroll-down = Scroll Down
    .description = Shell command on scroll down
