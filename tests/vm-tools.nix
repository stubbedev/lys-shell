{ pkgs, self }:
{
  vmclick = self.packages.${pkgs.stdenv.hostPlatform.system}.lys-shell.overrideAttrs {
    pname = "vmclick";
    subPackages = [ "internal/tools/vmclick" ];
    postInstall = "";
    postFixup = "";
  };

  portalClient = pkgs.writers.writePython3Bin "portal-client" {
    libraries = [ pkgs.python3Packages.jeepney ];
    flakeIgnore = [ "E501" ];
  } (builtins.readFile ./portal-client.py);
}
