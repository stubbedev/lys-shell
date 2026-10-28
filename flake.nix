{
  description = "lys-shell — a Wayland desktop shell";

  # Always use the self-hosted xilo cache for lys-shell builds (CI, releases, and
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
    # The dev shell's GPU driver setup (lib.gpuDriverEnv), shared with
    # lys-lib's and lys-ui's shells; the Go code comes through go.mod.
    lys-lib = {
      url = "github:stubbedev/lys-lib";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-parts.follows = "flake-parts";
    };
    # The Secret Service the Hyprland VM check unlocks through the lock
    # screen and the shell's password dialog; the Go client comes through
    # go.mod.
    lys-secrets = {
      url = "github:stubbedev/lys-secrets";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-parts.follows = "flake-parts";
    };
    # The lock screen the VM checks lock the session with; its own
    # lys-shell input is this flake.
    lys-login = {
      url = "github:stubbedev/lys-login";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-parts.follows = "flake-parts";
      inputs.lys-shell.follows = "";
      inputs.lys-wm.follows = "lys-wm";
    };
    # The compositor the lys-wm VM check runs the shell on.
    lys-wm = {
      url = "github:stubbedev/lys-wm";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-parts.follows = "flake-parts";
      inputs.lys-ui.inputs.flake-parts.follows = "flake-parts";
    };
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
        ./nix/checks.nix
      ];
    };
}
