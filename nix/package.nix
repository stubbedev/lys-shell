{ inputs, ... }:
let
  lysShellFor =
    pkgs:
    pkgs.callPackage ./lys-shell.nix {
      buildGoModule = pkgs.buildGoModule.override {
        go = inputs.devtools.packages.${pkgs.stdenv.hostPlatform.system}.go;
      };
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
