_:
let
  # Built with the toolchain go.mod pins. The overlay builds it against the
  # consumer's nixpkgs, so the pin is applied per package set.
  wayleFor =
    pkgs:
    pkgs.callPackage ./wayle.nix {
      buildGoModule = pkgs.buildGoModule.override { go = pkgs.go_1_27; };
    };
in
{
  perSystem =
    { pkgs, ... }:
    {
      packages = rec {
        wayle = wayleFor pkgs;
        default = wayle;
      };
    };

  # Adds `wayle` to a nixpkgs instance: `nixpkgs.overlays = [ wayle.overlays.default ];`
  flake.overlays.default = _final: prev: {
    wayle = wayleFor prev;
  };
}
