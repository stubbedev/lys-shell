{
  description = "wayle — a Wayland desktop shell (Go on gelm; the Rust + GTK4 + Relm4 shell kept buildable during the rewrite)";

  # Always use the self-hosted xilo cache for wayle builds (CI, releases, and
  # local dev via `nix build`). Read access is public; pushes are CI/release
  # only (XILO_TOKEN). Users are prompted before an untrusted flake's config
  # is honoured, or add the key to trusted-public-keys in nix.conf.
  nixConfig = {
    extra-substituters = [ "https://nix.stubbe.dev/c/default/default" ];
    extra-trusted-public-keys = [ "default:6uWvXutL9cXjV3lii+Ur5ff+ArQoG4kMBKNXWrIxhHg=" ];
  };

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };
    # crane builds the 591 deps as a separate, Cargo.lock-keyed derivation so a
    # source edit only recompiles wayle's own crates. See nix/wayle-rust.nix.
    crane.url = "github:ipetkov/crane";
  };

  outputs =
    inputs@{ flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      imports = [
        ./nix/package.nix
        ./nix/shell.nix
        ./nix/modules.nix
      ];
    };
}
