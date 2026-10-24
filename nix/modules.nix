{ self, ... }:
{
  flake = {
    # NixOS: `imports = [ lys-shell.nixosModules.default ]; programs.lys-shell.enable = true;`
    nixosModules.default = import ./nixos-module.nix self;

    # home-manager: `imports = [ lys-shell.homeManagerModules.default ]; programs.lys-shell.enable = true;`
    homeManagerModules.default = import ./hm-module.nix self;
  };
}
