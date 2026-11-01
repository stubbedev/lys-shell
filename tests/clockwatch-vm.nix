# internal/clockwatch against a real kernel: a NixOS VM whose clock is
# stepped and whose kernel suspends and resumes through /sys/power/pm_test.
# Those tests move the system clock and sleep the machine, so nothing else
# may run them.
#
# Build: nix build .#checks.<system>.lys-shell-clockwatch-vm -L
{
  pkgs,
  lib,
  self,
}:

let
  pkg = self.packages.${pkgs.stdenv.hostPlatform.system}.lys-shell;
  probe = pkg.overrideAttrs (_: {
    pname = "lys-shell-clockwatch-test";
    buildPhase = ''
      runHook preBuild
      go test -c -o clockwatch.test ./internal/clockwatch
      runHook postBuild
    '';
    installPhase = ''
      install -Dm0755 clockwatch.test $out/bin/clockwatch.test
    '';
    postInstall = "";
    postFixup = "";
    doCheck = false;
  });
in
pkgs.testers.runNixOSTest {
  name = "lys-shell-clockwatch";
  nodes.machine = {
    # A virtio-fs mount refuses to suspend, so the store comes from an image
    # and nothing is shared with the host.
    virtualisation = {
      useNixStoreImage = true;
      mountHostNixStore = false;
      sharedDirectories = lib.mkForce { };
    };
  };
  testScript = ''
    machine.wait_for_unit("multi-user.target")
    probe = "${probe}/bin/clockwatch.test -test.v -test.count=1"
    machine.succeed(f"LYS_CLOCKWATCH_VM=1 {probe} -test.run 'TestAStepOfTheClockIsAJump' >&2", timeout=120)
    machine.succeed(f"LYS_CLOCKWATCH_VM=1 {probe} -test.run 'TestAResumeIsAJump' >&2", timeout=180)
  '';
}
