{ inputs, ... }:
let
  # The Go shell, built with the toolchain go.mod pins. The overlay builds it
  # against the consumer's nixpkgs, so the pin is applied per package set.
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
        # The Go rewrite on gelm: the package the modules and the overlay
        # install.
        wayle = wayleFor pkgs;
        default = wayle;

        # The Rust shell (GTK4 + Relm4), kept buildable during the rewrite.
        wayle-rust = pkgs.callPackage ./wayle-rust.nix { craneLib = inputs.crane.mkLib pkgs; };

        # The cached 591-crate dependency layer. Not useful to install — it
        # exists so CI can build and push it to the xilo cache as its own store
        # path, which is what lets a fresh machine (or a fresh CI runner) skip
        # the ~30 min deps compile instead of relying on a GitHub Actions
        # /nix/store snapshot.
        wayle-deps = wayle-rust.cargoArtifacts;
      };
    };

  # Adds `wayle` to a nixpkgs instance: `nixpkgs.overlays = [ wayle.overlays.default ];`
  flake.overlays.default = _final: prev: {
    wayle = wayleFor prev;
  };
}
