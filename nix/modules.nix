{ self, ... }:
{
  flake = {
    # NixOS: `imports = [ wayle.nixosModules.default ]; programs.wayle.enable = true;`
    nixosModules.default = import ./nixos-module.nix self;

    # home-manager: `imports = [ wayle.homeManagerModules.default ]; programs.wayle.enable = true;`
    homeManagerModules.default = import ./hm-module.nix self;
  };
}
