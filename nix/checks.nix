{ self, ... }:
{
  perSystem =
    { pkgs, lib, ... }:
    {
      checks = lib.optionalAttrs pkgs.stdenv.hostPlatform.isx86_64 {
        # The shell on a live Hyprland in a NixOS VM (needs KVM); see
        # tests/hyprland-vm.nix.
        lys-shell-hyprland-vm = pkgs.callPackage ../tests/hyprland-vm.nix { inherit self; };
      };
    };
}
