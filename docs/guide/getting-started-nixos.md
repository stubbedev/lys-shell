---
title: NixOS
---

# NixOS

lys-shell is installed from this repository's flake. It exports `nixosModules.default`, `homeManagerModules.default`, `packages.${system}.default` and `overlays.default` (which adds `pkgs.lys-shell`).

```nix
# flake.nix
{
  inputs.lys-shell.url = "github:stubbedev/lys-shell";
}
```

## NixOS module

```nix
{ inputs, ... }:
{
  imports = [ inputs.lys-shell.nixosModules.default ];
  programs.lys-shell.enable = true;
}
```

| Option | Default | Effect |
| --- | --- | --- |
| `programs.lys-shell.enable` | `false` | Install lys-shell. |
| `programs.lys-shell.package` | the flake's package | Package to install. |
| `programs.lys-shell.settingsApp.enable` | `true` | Also install lys-settings. Without it, `panel settings`, the dashboard and the launcher have no settings app to open. |
| `programs.lys-shell.settingsApp.package` | lys-settings' flake package | Package to install. |
| `programs.lys-shell.systemd.enable` | `false` | Run `lys-shell shell` as a systemd user service. |
| `programs.lys-shell.systemd.target` | `"graphical-session.target"` | Target the service binds to. |
| `programs.lys-shell.portal.enable` | `true` | Make lys-shell the xdg-desktop-portal backend for every interface (through `xdg.portal`, with `mkDefault` routing). |
| `programs.lys-shell.autoInstallDependencies` | `false` | Enable UPower, BlueZ and GeoClue (allowing lys-shell to locate the night light schedule), and install wl-clipboard and xdg-utils. NetworkManager, power-profiles-daemon and PipeWire are left to you. |

## home-manager module

```nix
{ inputs, ... }:
{
  imports = [ inputs.lys-shell.homeManagerModules.default ];
  programs.lys-shell = {
    enable = true;
    autoInstallDependencies = true;
    settings = {
      bar.layout = [
        {
          monitor = "*";
          left = [ "dashboard" ];
          center = [ "clock" ];
          right = [ "battery" "volume" "systray" ];
        }
      ];
      modules.weather = {
        location = "Denver";
        units = "imperial";
      };
      styling.theme-provider = "wallust";
    };
  };
}
```

| Option | Default | Effect |
| --- | --- | --- |
| `enable`, `package`, `settingsApp.*`, `systemd.target` | as above | As in the NixOS module. |
| `systemd.enable` | `true` | Run `lys-shell shell` as a systemd user service. Turn off if your compositor starts the shell. |
| `settings` | `{}` | Written to `$XDG_CONFIG_HOME/lys-shell/config.yaml`. Empty leaves the file unmanaged. |
| `autoInstallDependencies` | `false` | Install the user-space tools the config needs: the `styling.theme-provider` tool (matugen, wallust or pywal), wl-clipboard, xdg-utils, and notmuch when `mail` is in the layout. |
| `portal.enable` | `false` | Install the portal backend at user level (portal and D-Bus files, a user service, a `portals.conf` routing every interface to lys-shell). Use this outside NixOS; on NixOS use the system option. |

System services (NetworkManager, BlueZ, UPower, power-profiles-daemon, GeoClue, PipeWire) are not installed by home-manager.

## Lock screen

[lys-login](https://github.com/stubbedev/lys-login) has its own flake and NixOS module. `programs.lys-login.enable` puts it on the `PATH`. `programs.lys-login.lock.enable` declares a PAM service for the unlock; point [`login.lock.pam-service`](/config/login) at it.

## Converting settings to Nix

lys-settings and `lys-shell config set` write to `~/.config/lys-shell/runtime.yaml`. To turn `config.yaml` and `runtime.yaml` into a Nix expression for `settings`, using [yq](https://github.com/mikefarah/yq) (`nixpkgs#yq-go`):

```sh
cd ~/.config/lys-shell
nix-instantiate --eval --strict \
  --argstr json "$(yq eval-all -o json '. as $f ireduce ({}; . * $f)' config.yaml runtime.yaml)" \
  --expr '{ json }: builtins.fromJSON json' | nixfmt
```

With only one of the files, use `yq -o json config.yaml` in its place.
