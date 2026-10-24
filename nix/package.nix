_:
let
  # Built with the toolchain go.mod pins. The overlay builds it against the
  # consumer's nixpkgs, so the pin is applied per package set.
  lysShellFor =
    pkgs:
    pkgs.callPackage ./lys-shell.nix {
      buildGoModule = pkgs.buildGoModule.override { go = pkgs.go_1_27; };
    };
in
{
  perSystem =
    { pkgs, ... }:
    {
      packages = rec {
        lys-shell = lysShellFor pkgs;
        default = lys-shell;
      };
    };

  # Adds `lys-shell` to a nixpkgs instance: `nixpkgs.overlays = [ lys-shell.overlays.default ];`
  flake.overlays.default = _final: prev: {
    lys-shell = lysShellFor prev;
  };
}
