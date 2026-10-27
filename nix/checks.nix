{ self, inputs, ... }:
{
  perSystem =
    { pkgs, lib, ... }:
    let
      pkg = self.packages.${pkgs.stdenv.hostPlatform.system}.lys-shell;
    in
    {
      checks =
        lib.optionalAttrs pkgs.stdenv.hostPlatform.isx86_64 {
          # The shell on a live Hyprland in a NixOS VM (needs KVM); see
          # tests/hyprland-vm.nix.
          lys-shell-hyprland-vm = pkgs.callPackage ../tests/hyprland-vm.nix { inherit self; };
          # The shell on lys-wm in a NixOS VM (needs KVM); see
          # tests/lys-wm-vm.nix.
          lys-shell-lys-wm-vm = pkgs.callPackage ../tests/lys-wm-vm.nix {
            inherit self;
            inherit (inputs) lys-wm;
          };
          # lys-login as the display manager in a NixOS VM (needs KVM); see
          # tests/login-vm.nix.
          lys-shell-login-vm = pkgs.callPackage ../tests/login-vm.nix {
            inherit self;
            inherit (inputs) lys-wm;
          };
        }
        // {
          # Every wrapped binary finds the Vulkan loader: lys-ui renders on
          # the GPU through it and silently stays on the CPU without it, and
          # NixOS has no global library path to fall back on.
          lys-shell-wrapper-vulkan = pkgs.runCommand "lys-shell-wrapper-vulkan" { } ''
            for bin in ${lib.concatMapStringsSep " " (b: "${pkg}/bin/${b}") pkg.wrapped}; do
              grep -q "${lib.getLib pkgs.vulkan-loader}/lib" "$bin" || { echo "$bin lacks the Vulkan loader on its library path" >&2; exit 1; }
            done
            touch $out
          '';
        };
    };
}
