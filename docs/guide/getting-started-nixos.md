---
title: Getting started on NixOS
---

# Getting started on NixOS

lys-shell is not packaged in nixpkgs; install it from this repository's flake.

## Install from this flake

Add the flake as an input and use the modules it exports.

```nix
# flake.nix
{
  inputs.lys-shell.url = "github:stubbedev/lys-shell";
}
```

NixOS:

```nix
{ inputs, ... }:
{
  imports = [ inputs.lys-shell.nixosModules.default ];
  programs.lys-shell.enable = true;
  # optional: run `lys-shell shell` as a user service in the graphical session
  programs.lys-shell.systemd.enable = true;
  # optional: enable backing services (UPower, BlueZ) + wl-clipboard/xdg-utils
  programs.lys-shell.autoInstallDependencies = true;
}
```

home-manager:

```nix
{ inputs, ... }:
{
  imports = [ inputs.lys-shell.homeManagerModules.default ];
  programs.lys-shell = {
    enable = true; # installs lys-shell + lys-settings, runs it as a user service
    # install user-space soft deps implied by the config (theme-provider tool,
    # wl-clipboard, xdg-utils)
    autoInstallDependencies = true;
    settings = {
      # written to ~/.config/lys-shell/config.toml
      bar.layout = [
        {
          monitor = "*";
          center = [ "clock" ];
          right = [ "battery" "volume" "systray" ];
        }
      ];
    };
  };
}
```

You can also use the package directly
(`inputs.lys-shell.packages.${system}.default`) or apply
`inputs.lys-shell.overlays.default` to get `pkgs.lys-shell`.

> Soft dependencies (NetworkManager, bluez, upower, power-profiles-daemon,
> pipewire/wireplumber, and theming tools like wallust/matugen) are not pulled
> in automatically — enable the ones your config uses.

## home-manager usage

```nix
# put this directly into your home-manager config or into a home-manager import
{
  programs.lys-shell = {
    enable = true;

    # Whether to automatically install soft dependencies used by lys-shell that
    # will be required based on your config (theme-provider tool, wl-clipboard,
    # xdg-utils, and notmuch when the mail module is used).
    autoInstallDependencies = true;

    # tip: you can automatically translate your TOML config to Nix by running
    # nix-instantiate --eval --expr 'builtins.fromTOML (builtins.readFile ./config.toml)' | nixfmt
    settings = {
      bar = {
        layout = [
          # add more attribute sets with different monitors if lys-shell should
          # have different layouts on each
          {
            monitor = "DP-1"; # replace "DP-1" with "*" for all monitors
            show = true;
            center = [
              "clock"
              "weather"
            ];
            left = [ "dashboard" ];
            right = [ "volume" ];
          } # this is a 'list' of 'attribute sets', no semi-colons after the closing braces needed
        ];
      };
      modules = {
        clock = {
          format = "%H:%M:%S";
          dropdown-show-seconds = false;
        };
        weather = {
          location = "Denver";
          units = "imperial";
        };
      };
      osd = {
        monitor = "DP-1";
      };
      styling = {
        palette = {
          bg = "#282a36";
          blue = "#8be9fd";
          # ...
        };
        # wallust is installed automatically when this is set and
        # autoInstallDependencies = true (likewise matugen / pywal)
        theme-provider = "wallust";
      };
      # the following wallpaper options can be omitted if you're not using
      # lys-shell's built-in wallpaper engine. Setting cycling-directory enables
      # cycling through that folder.
      wallpaper = {
        cycling-directory = "/home/horsey/Pictures/Backgrounds/1/";
        cycling-mode = "shuffle";
      };
    };
  };
}
```

## Settings GUI

```sh
lys-shell panel settings
```

This launches `lys-settings`, which edits the same config the shell reads. Changes apply live. Anything the GUI doesn't cover can still be edited by hand in `config.toml`.

> [!WARNING]
> If you configured theming using matugen/wallust/pywal, do not forget to install these dependencies! With `autoInstallDependencies = true` the home-manager module installs the matching theme-provider tool for you.

After configuring lys-shell using GUI, there should be a new `.config/lys-shell/runtime.toml` file. To automatically convert it to Nix, run
```sh
cd ~/.config/lys-shell
nix-instantiate --eval --expr '(builtins.fromTOML (builtins.readFile ./config.toml)) // (builtins.fromTOML (builtins.readFile ./runtime.toml))' | nixfmt
```
If one of those files does not exist run the following command, replacing `config.toml` with `runtime.toml` if appropriate.
```sh
nix-instantiate --eval --expr 'builtins.fromTOML (builtins.readFile ./config.toml)' | nixfmt
```

Then you can copy-paste this into the `programs.lys-shell.settings` option provided by the home-manager module.

## Configuration

If you want to edit the raw `config.toml`, refer to the [Editing config](/guide/editing-config) page (note that Tombi doesn't work when config is symlinked to `/nix/store`, you need to create a normal file first).
