# Secrets (keyring)

`lys-secrets` serves the freedesktop [Secret Service](https://specifications.freedesktop.org/secret-service-spec/latest/)
(`org.freedesktop.secrets`), so a lys desktop needs no gnome-keyring,
KeePassXC or KWallet for app secrets. libsecret (`secret-tool`), Chromium and
Electron apps, git's `git-credential-libsecret`, python-secretstorage and the
portal's per-app secrets for sandboxed apps all keep their secrets in it.

It is a small process of its own (`lys-secrets`), separate from the shell:
it links no UI and asks for passwords through the shell's dialogs.

## Enabling it

Only one program can own `org.freedesktop.secrets`. When another provider
already owns it, `lys-secrets` logs which one and exits, so it never takes
the name from gnome-keyring.

- **NixOS:** `programs.lys-shell.secrets.enable = true;` installs the D-Bus
  activation file and a `lys-secrets` user service started with the
  graphical session. It cannot be combined with
  `services.gnome.gnome-keyring.enable` (evaluation fails with a message).
- **home-manager:** `programs.lys-shell.secrets.enable = true;` does the same
  at the user level. Do not run gnome-keyring's secrets component as well.
- **Arch, Debian, Fedora:** the package installs the `lys-secrets` user unit
  but no activation file. Disable gnome-keyring's secrets component, then
  `systemctl --user enable --now lys-secrets`. To have clients start it on
  demand too, copy `/usr/share/lys-shell/org.freedesktop.secrets.lys-secrets.service`
  to `~/.local/share/dbus-1/services/org.freedesktop.secrets.service`.

## Unlocking

The `login` collection is encrypted with your session password:

- **The lock screen** hands the password it just verified to `lys-secrets`
  over a socket only your user can connect to
  (`$XDG_RUNTIME_DIR/lys-secrets/control`), never over D-Bus. The first
  unlock creates the `login` collection with that password and makes it the
  default.
- **A login screen** can pass it at startup instead:
  `lys-secrets --unlock-fd N` reads the password from an inherited descriptor.
- **Anything else** (a restart, another collection, a locked keyring) asks
  through the shell's password dialog when an app needs a secret. A wrong
  password asks again; cancelling leaves the collection locked.

If `lys-secrets` is not running, the lock screen unlocks a running
gnome-keyring instead, as before.

Changing your login password does not re-encrypt the collection: a
`ChangeLock` request (for example from Seahorse) asks for the new password.

## Access

Every program running as you on your session bus may read an unlocked
collection, as with gnome-keyring. Sandboxed (Flatpak) apps get their secret
through the portal, which identifies them, and their master secrets live in
the `login` collection too.

## Storage

Each collection is one file, `$XDG_DATA_HOME/lys-secrets/<id>.keyring`
(mode 0600), written atomically. Its key is derived from the password with
Argon2id (3 passes over 64 MiB), and the contents are encrypted with
XChaCha20-Poly1305. Only the item ids and SHA-256 hashes of their attributes
are readable without the password, so a locked collection can tell a client
that a matching item exists and needs an unlock; labels, attribute values
and secrets are encrypted. While a collection is unlocked, each secret stays
encrypted in memory and is decrypted only for the request that reads it. The
key lives outside the Go heap, locked against swap where the memlock limit
allows, and the process is not dumpable. The format is described in
`secrets/keyring/collection.go`.

Existing gnome-keyring keyrings are not imported.
