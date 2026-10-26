# CLAUDE.md

## Build and test

The nix dev shell pins the Go toolchain `go.mod` names and carries the lint
stack, fonts, and the sway, dbus, pipewire and xdg-desktop-portal the tests
start private instances of:

```sh
nix develop -c just build   # static binaries into dist/
nix develop -c just test    # go test ./...
nix develop -c just vet     # go vet ./...
nix develop -c just lint    # golangci-lint fmt --diff + golangci-lint run
nix develop -c just check   # vet + lint + test + race + schema-check + build, run before every release
just docker check           # any recipe in the dev shell inside a container, away from the desktop session
just hyprland-vm            # the shell on a live Hyprland in a NixOS VM (KVM), screenshots in ./result
```

After a `go.mod` change (a lys-ui bump), refresh `nix/lys-shell.nix`'s
`vendorHash` with `just vendor-hash`.

## Tests define behavior

Every feature and every bug fix lands with tests, and each behavior gets both
assertions:

- **positive** — the behavior holds when it should (the session stays open, the
  flag is parsed, the row is emitted).
- **negative** — it does *not* hold when it should not, and the failure mode is
  the defined one (the socket closes only on exit, a bad value errors instead of
  silently defaulting, the ignored flag is ignored).

A fix without a test that fails before it is not a fix. Prove the test catches
the regression by reverting the change and watching it fail.

Tests live in `_test.go` files next to the code they cover. Contracts across
processes run the real binary or a private bus (see
`cmd/lys-shell/launcher_session_test.go` for the launcher socket lifetime,
`internal/dbustest` for private session buses, and `portal/frontend_test.go`
for the real xdg-desktop-portal frontend in front of the backend). The CLI's
help, errors and completions are pinned byte for byte by the goldens under
`cmd/lys-shell/testdata/clap`; a deliberate CLI change edits them in the same
commit.
