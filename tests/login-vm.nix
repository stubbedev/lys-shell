# lys-login as the display manager (#109): a NixOS VM boots into the
# greeter, shown in a lys-wm kiosk by `lys-login daemon` with neither
# greetd nor cage. The script logs a user in with a wrong and then the
# right password, checks the session logind registered (the user, the
# wayland type, the user class, the seat and the VT), and that the
# greeter comes back once the session ends.
#
# Build: nix build .#checks.<system>.lys-shell-login-vm -L
{
  pkgs,
  lib,
  self,
  lys-wm,
}:

let
  user = "alice";
  password = "lys-login-vm";

  # The session the greeter offers: it records what it runs as and with,
  # then ends, which hands the VT back to the greeter.
  session = pkgs.writeShellScript "lys-login-vm-session" ''
    echo "$(id -un) $XDG_SESSION_TYPE $XDG_SESSION_CLASS $XDG_SEAT $XDG_VTNR $XDG_SESSION_ID" > /tmp/lys-login/session
    loginctl show-session "$XDG_SESSION_ID" -p Class -p Type -p Seat -p VTNr -p Name > /tmp/lys-login/logind
    sleep 5
  '';
in
pkgs.testers.nixosTest {
  name = "lys-shell-login-vm";

  nodes.machine =
    { config, ... }:
    {
      imports = [
        self.nixosModules.default
        lys-wm.nixosModules.default
      ];

      programs.lys-wm.enable = true;
      programs.lys-shell = {
        enable = true;
        greeter = {
          enable = true;
          displayManager = "lys-login";
          compositorPackage = config.programs.lys-wm.package;
          session = {
            dirs = [ ];
            x11Dirs = [ ];
            command = "${session}";
          };
        };
      };

      virtualisation.graphics = true;
      virtualisation.cores = 4;
      virtualisation.memorySize = 2048;
      virtualisation.qemu.options = [
        "-vga none"
        "-device virtio-gpu-pci"
      ];
      environment.sessionVariables.LIBGL_ALWAYS_SOFTWARE = "1";
      hardware.graphics.enable = true;
      hardware.graphics.extraPackages = [ pkgs.mesa ];
      fonts.packages = [ pkgs.inter ];
      systemd.tmpfiles.rules = [ "d /tmp/lys-login 0777 root root -" ];

      users.users.${user} = {
        isNormalUser = true;
        inherit password;
      };
    };

  testScript = ''
    import time

    start_all()
    machine.wait_for_unit("lys-login.service")

    def greeter_up():
        return machine.execute("pgrep -u lys-greeter -f 'lys-login greet'")[0] == 0

    machine.wait_until_succeeds("pgrep -u lys-greeter -f 'lys-login greet'", timeout=90)
    # Neither greetd nor cage takes part.
    for absent in ("greetd", "cage"):
        if machine.execute(f"pgrep -x {absent}")[0] == 0:
            raise Exception(f"{absent} runs")
    machine.wait_until_succeeds("loginctl list-sessions --no-legend | grep -q lys-greeter", timeout=30)
    time.sleep(3)
    machine.screenshot("01-greeter")

    # A wrong password leaves the greeter up and starts nothing.
    machine.send_chars("${user}\n")
    time.sleep(1)
    machine.send_chars("wrong-password\n")
    time.sleep(4)
    machine.screenshot("02-wrong-password")
    if machine.execute("test -e /tmp/lys-login/session")[0] == 0:
        raise Exception("a wrong password started a session")
    if not greeter_up():
        raise Exception("a wrong password took the greeter down")

    # The username stays; the right password logs in.
    machine.send_chars("${password}\n")
    machine.wait_until_succeeds("test -s /tmp/lys-login/logind", timeout=60)
    seen = machine.succeed("cat /tmp/lys-login/session").split()
    if seen[:5] != ["${user}", "wayland", "user", "seat0", "1"]:
        raise Exception(f"the session ran as {seen}")
    logind = machine.succeed("cat /tmp/lys-login/logind")
    for want in ("Class=user", "Type=wayland", "Seat=seat0", "VTNr=1", "Name=${user}"):
        if want not in logind:
            raise Exception(f"logind's session lacks {want}:\n{logind}")
    machine.screenshot("03-session")

    # When the session ends the greeter comes back at once, on its
    # first try.
    machine.wait_until_succeeds("pgrep -u lys-greeter -f 'lys-login greet'", timeout=30)
    time.sleep(2)
    machine.screenshot("04-greeter-again")
    journal = machine.succeed("journalctl -u lys-login --no-pager")
    if "dm: greeter: the greeter ended" in journal:
        raise Exception("a greeter died and was restarted:\n" + journal[-4000:])
    if "dm: greeter connection:" in journal:
        raise Exception("a greeter connection failed:\n" + journal[-4000:])
    print(journal[-4000:])
  '';
}
