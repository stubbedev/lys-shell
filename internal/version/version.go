// Package version is lys-shell's identity: the name its per-user
// directories are kept under, and the release version `lys-shell
// --version` and the notification server report and nix/lys-shell.nix
// reads for the package version. `just release-*` bumps it.
package version

// Name is lys-shell's name, the directory it keeps its state, cache and
// data in under each XDG base directory.
const Name = "lys-shell"

// Version is the lys-shell release version.
const Version = "0.1.0"
