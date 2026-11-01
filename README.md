<p align="center">
  <img src="docs/public/lys-shell.svg" width="160" alt="lys-shell">
</p>

<h1 align="center">lys-shell</h1>

<p align="center">
  A complete Wayland desktop shell in pure Go: bar, notifications, OSD, launcher, wallpaper, and a desktop portal.
</p>

<p align="center">
  <a href="https://github.com/stubbedev/lys-shell/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/stubbedev/lys-shell/ci.yml?branch=master&style=for-the-badge&label=CI" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue?style=for-the-badge" alt="MIT license"></a>
  <a href="https://stubbedev.github.io/lys-shell/"><img src="https://img.shields.io/badge/docs-stubbedev.github.io-informational?style=for-the-badge" alt="Documentation"></a>
</p>

<p align="center">
  <img src="docs/public/lys-shell-preview.png" alt="lys-shell on a desktop">
</p>

lys-shell runs on any compositor with `wlr-layer-shell`. It is built on [lys-ui](https://github.com/stubbedev/lys-ui), and does not need [lys-wm](https://github.com/stubbedev/lys-wm).

## Install

[Releases](https://github.com/stubbedev/lys-shell/releases) carry a `.deb`, a Fedora `.rpm` and a tarball, for x86_64 and aarch64. The flake ships the package, a NixOS module and a home-manager module; see the [NixOS guide](https://stubbedev.github.io/lys-shell/guide/getting-started-nixos).

To build from source, with Go and [just](https://github.com/casey/just) installed:

```sh
git clone https://github.com/stubbedev/lys-shell
cd lys-shell
just build
sudo install -Dm755 dist/lys-shell dist/lys-settings dist/lys-lock -t /usr/local/bin
lys-shell icons setup
```

[Getting started](https://stubbedev.github.io/lys-shell/guide/getting-started) lists the runtime dependencies for each distribution.

## Usage

```sh
lys-shell panel start     # run the shell in the background
lys-shell panel settings  # open the settings GUI
```

lys-shell reads `~/.config/lys-shell/config.yaml` and applies changes on save:

```yaml
bar:
  layout:
    - monitor: "*"
      left: [dashboard, hyprland-workspaces]
      center: [clock]
      right: [volume, network, bluetooth, battery]
```

## Documentation

The guides, the CLI and the config reference are at <https://stubbedev.github.io/lys-shell/>. For development, see [CLAUDE.md](CLAUDE.md).

## The lys family

[lys-wm](https://stubbedev.github.io/lys-wm/) (compositor), [lys-ui](https://stubbedev.github.io/lys-ui/) (widget toolkit), [lys-lib](https://stubbedev.github.io/lys-lib/) (building blocks), [lys-login](https://stubbedev.github.io/lys-login/) (display manager and lock screen), [lys-splash](https://stubbedev.github.io/lys-splash/) (boot splash), [lys-polkit](https://stubbedev.github.io/lys-polkit/) (polkit agent) and [lys-secrets](https://stubbedev.github.io/lys-secrets/) (Secret Service).

## Credits

lys-shell began as a fork of [wayle-rs/wayle](https://github.com/wayle-rs/wayle) and is now an independent Go implementation that shares none of its code; its configuration format and much of its design come from there.

## License

[MIT](LICENSE)

The passphrase generator embeds the words of the EFF large wordlist by the Electronic Frontier Foundation, under the Creative Commons Attribution 3.0 United States License; see [eff_large_wordlist.NOTICE](service/bitwarden/generator/eff_large_wordlist.NOTICE).
