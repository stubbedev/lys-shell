# The shell on a live Hyprland (#33): a NixOS VM whose virtio-gpu card
# Hyprland takes master on (aquamarine cannot run without a DRM node,
# and the host's belongs to the developer's session). The shell is the
# flake's package, installed and wired by the flake's NixOS module, and
# started from the Hyprland config the way a user's session does.
#
# The script asserts what the compositor reports (the bar's layer on
# every output with its exclusive zone, the launcher and popup layers
# coming and going, the session lock taking the outputs and releasing
# them on a typed password, the lock surviving a shell restart) and
# leaves a screenshot of every state in the result for review.
#
# Build: nix build .#checks.<system>.lys-shell-hyprland-vm -L
{
  pkgs,
  lib,
  self,
}:

let
  user = "alice";
  password = "lys-shell-vm";
  uid = 1000;
  runtimeDir = "/run/user/${toString uid}";

  hyprConf = pkgs.writeText "hyprland-vm.conf" ''
    monitor = Virtual-1, 1280x800, 0x0, 1
    monitor = HEADLESS-2, 1024x768, 1280x0, 1
    monitor = , preferred, auto, 1

    animations {
        enabled = 0
    }
    input {
        kb_layout = us,de
        repeat_delay = 5000
    }
    decoration {
        rounding = 0
        shadow {
            enabled = false
        }
    }
    misc {
        disable_splash_rendering = true
        force_default_wallpaper = 0
        allow_session_lock_restore = true
    }
    debug {
        disable_logs = false
    }

    workspace = 1, monitor:Virtual-1
    workspace = 2, monitor:Virtual-1
    workspace = 3, monitor:HEADLESS-2

    submap = resize
    binde = , right, resizeactive, 10 0
    bind = , escape, submap, reset
    submap = reset

    exec-once = lys-shell shell > /tmp/lys/shell.log 2>&1
  '';

  shellConfig = (pkgs.formats.yaml { }).generate "config.yaml" {
    lock = {
      enabled = true;
      pam-service = "lys-shell";
    };
    bar.layout = [
      {
        monitor = "*";
        left = [
          "hyprland-workspaces"
          "window-title"
        ];
        center = [ "clock" ];
        right = [
          "keybind-mode"
          "keyboard-input"
          "notifications"
        ];
      }
    ];
  };
in
pkgs.testers.nixosTest {
  name = "lys-shell-hyprland-vm";

  nodes.machine =
    { ... }:
    {
      imports = [ self.nixosModules.default ];

      programs.lys-shell = {
        enable = true;
        lock.enable = true;
      };

      virtualisation.graphics = true;
      virtualisation.cores = 4;
      virtualisation.memorySize = 4096;
      virtualisation.resolution = {
        x = 1280;
        y = 800;
      };
      # One virtio-gpu card; -vga none leaves Hyprland exactly that
      # card. A second output is hotplugged at runtime as a Hyprland
      # headless output.
      virtualisation.qemu.options = [
        "-vga none"
        "-device virtio-gpu-pci"
      ];

      # A real login session, as a desktop has one: getty logs the user
      # in on tty1 (logind session, seat, user bus) and the login shell
      # execs Hyprland. The lock's relock-after-restart keys on that
      # session's LockedHint.
      services.getty.autologinUser = user;
      environment.loginShellInit = ''
        if [ "$(tty)" = /dev/tty1 ]; then
          exec Hyprland > /tmp/lys/hyprland.log 2>&1
        fi
      '';
      environment.sessionVariables.LIBGL_ALWAYS_SOFTWARE = "1";
      systemd.tmpfiles.rules = [
        "d /tmp/lys 0777 root root -"
        "d /home/${user}/.config 0755 ${user} users -"
        "d /home/${user}/.config/hypr 0755 ${user} users -"
        "d /home/${user}/.config/lys-shell 0755 ${user} users -"
        "L+ /home/${user}/.config/hypr/hyprland.conf - - - - ${hyprConf}"
        "C /home/${user}/.config/lys-shell/config.yaml 0644 ${user} users - ${shellConfig}"
      ];
      hardware.graphics.enable = true;
      hardware.graphics.extraPackages = [ pkgs.mesa ];
      fonts.packages = [ pkgs.inter ];

      users.users.${user} = {
        isNormalUser = true;
        uid = lib.mkForce uid;
        inherit password;
        extraGroups = [
          "video"
          "render"
          "input"
        ];
      };

      environment.systemPackages = [
        pkgs.hyprland
        pkgs.foot
        pkgs.libnotify
        pkgs.dbus
      ];
    };

  testScript = ''
    import json
    import time

    start_all()
    machine.wait_for_unit("multi-user.target")
    machine.succeed("test -e /dev/dri/card0")

    def wait(what, check, tries=60, pause=1.0):
        for _ in range(tries):
            if check():
                return
            time.sleep(pause)
        print(machine.execute("cat /tmp/lys/shell.log")[1])
        print(machine.execute("tail -n 80 ${runtimeDir}/hypr/*/hyprland.log")[1])
        raise Exception("timed out waiting for " + what)

    wait("Hyprland's socket", lambda: machine.execute("ls ${runtimeDir}/hypr/*/.socket.sock")[0] == 0, tries=90)
    sig = machine.succeed("ls ${runtimeDir}/hypr").split()[0]
    env = (
        f"XDG_RUNTIME_DIR=${runtimeDir} HYPRLAND_INSTANCE_SIGNATURE={sig} "
        "DBUS_SESSION_BUS_ADDRESS=unix:path=${runtimeDir}/bus"
    )

    def hypr(args):
        return machine.succeed(f"su - ${user} -c '{env} hyprctl {args}'")

    def hypr_json(args):
        return json.loads(hypr(f"-j {args}"))

    def namespaces():
        found = []
        for mon in hypr_json("layers").values():
            for level in mon["levels"].values():
                found += [layer["namespace"] for layer in level]
        return found

    def shell(args):
        return machine.succeed(
            f"su - ${user} -c '{env} WAYLAND_DISPLAY=$(basename $(ls ${runtimeDir}/wayland-* | head -1)) "
            f"lys-shell {args}'"
        )

    def shell_bg(args, log):
        machine.succeed(
            f"su - ${user} -c '{env} WAYLAND_DISPLAY=$(basename $(ls ${runtimeDir}/wayland-* | head -1)) "
            f"lys-shell {args} > /tmp/lys/{log}.log 2>&1 &'"
        )

    def outputs():
        return sorted(m["name"] for m in hypr_json("monitors"))

    def bars_on(names):
        return all(f"lys-shell-bar-{n}" in namespaces() for n in names)

    # The bar maps one layer per output and reserves its height.
    wait("the first output", lambda: outputs() == ["Virtual-1"])
    wait("a bar on the output", lambda: bars_on(["Virtual-1"]), tries=90)

    # A hotplugged output gets its own bar, and loses it on unplug.
    hypr("output create headless HEADLESS-2")
    wait("the hotplugged output", lambda: "HEADLESS-2" in outputs())
    names = outputs()
    wait("a bar on every output", lambda: bars_on(names), tries=60)
    for m in hypr_json("monitors"):
        if m["reserved"][1] <= 0:
            raise Exception(f"{m['name']} reserves no space for the bar: {m['reserved']}")
    machine.screenshot("01-bar")

    # Workspaces: windows on 1 and 3, the view on 2, a special
    # workspace toggled over the first output.
    hypr('dispatch exec "[workspace 1 silent] foot"')
    hypr('dispatch exec "[workspace 3 silent] foot"')
    wait("two windows", lambda: len(hypr_json("clients")) == 2, tries=60)
    hypr("dispatch workspace 2")
    time.sleep(1)
    machine.screenshot("02-workspace-2")
    hypr("dispatch workspace 1")
    hypr("dispatch togglespecialworkspace scratch")
    time.sleep(1)
    machine.screenshot("03-special-workspace")
    hypr("dispatch togglespecialworkspace scratch")

    # The keyboard layout and keybind mode modules follow the
    # compositor.
    hypr("switchxkblayout all next")
    hypr("dispatch submap resize")
    time.sleep(1)
    machine.screenshot("04-layout-de-and-resize-submap")
    hypr("dispatch submap reset")
    hypr("switchxkblayout all prev")

    # The launcher maps its layer and Escape closes it.
    shell_bg("launcher -show drun", "launcher")
    wait("the launcher", lambda: "lys-shell-launcher" in namespaces())
    time.sleep(1)
    machine.screenshot("05-launcher")
    machine.send_key("esc")
    wait("the launcher to close", lambda: "lys-shell-launcher" not in namespaces())

    # A notification pops up on the focused output.
    machine.succeed(
        f"su - ${user} -c '{env} "
        "notify-send -a Firefox \"Download complete\" \"report.pdf finished downloading\"'"
    )
    wait("the notification popup", lambda: "lys-shell-notification-popup" in namespaces())
    time.sleep(1)
    machine.screenshot("06-notification")

    # The session lock covers every output and a typed password lifts it.
    def logged(log, line):
        return line in machine.execute(f"cat /tmp/lys/{log}.log")[1]

    def count(log, line):
        return machine.execute(f"cat /tmp/lys/{log}.log")[1].count(line)

    shell("lock")
    wait("the session lock", lambda: logged("shell", "lock: session locked (2 monitors)"))
    time.sleep(1)
    machine.screenshot("07-locked")
    machine.send_chars("${password}\n")
    wait("the unlock", lambda: logged("shell", "lock: session unlocked"))
    machine.screenshot("08-unlocked")

    # A shell that dies while locked leaves the compositor locked
    # (allow_session_lock_restore) and logind's LockedHint set; the
    # restarted shell re-acquires the lock and a password lifts it.
    shell("lock")
    wait("the second lock", lambda: count("shell", "lock: session locked") == 2)
    machine.succeed("pkill -KILL -f 'lys-shell shell'")
    time.sleep(2)
    machine.screenshot("09-locked-shell-dead")
    hypr('dispatch exec "lys-shell shell > /tmp/lys/shell2.log 2>&1"')
    wait("the restarted shell to re-acquire the lock", lambda: logged("shell2", "re-acquiring") and logged("shell2", "lock: session locked"))
    time.sleep(1)
    machine.screenshot("10-relocked-after-restart")
    machine.send_chars("${password}\n")
    wait("the unlock after the restart", lambda: logged("shell2", "lock: session unlocked"))
    wait("the bar after the restart", lambda: bars_on(names))
    machine.screenshot("11-unlocked-after-restart")

    hypr("output remove HEADLESS-2")
    wait("the unplugged output's bar to go", lambda: "lys-shell-bar-HEADLESS-2" not in namespaces())
    if not bars_on(["Virtual-1"]):
        raise Exception("unplugging an output took the remaining bar with it")

    print(machine.execute("cat /tmp/lys/shell.log /tmp/lys/shell2.log")[1])
  '';
}
