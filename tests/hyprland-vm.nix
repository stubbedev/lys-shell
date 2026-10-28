# The shell on a live Hyprland (#33): a NixOS VM whose virtio-gpu card
# Hyprland takes master on (aquamarine cannot run without a DRM node,
# and the host's belongs to the developer's session). The shell is the
# flake's package, installed and wired by the flake's NixOS module, and
# started from the Hyprland config the way a user's session does.
#
# The script asserts what the compositor reports (the bar's layer on
# every output with its exclusive zone, the launcher and popup layers
# coming and going, the OSD and popups on the configured output, the
# portal's answers - screenshot, colour pick, a global shortcut bound
# and activated through the compositor - the night light applied
# from the bar through hyprland-ctm-control, the session lock taking the outputs and releasing them
# on a typed password, a dead lock client replaced, the lock surviving a
# shell restart, lys-secrets'
# login collection unlocked by that password and, after a restart,
# through the shell's own password dialog) and leaves a
# screenshot of every state in the result for review.
#
# Build: nix build .#checks.<system>.lys-shell-hyprland-vm -L
{
  pkgs,
  lib,
  self,
  lys-secrets,
  lys-login,
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

    exec-once = dbus-update-activation-environment --systemd WAYLAND_DISPLAY XDG_CURRENT_DESKTOP HYPRLAND_INSTANCE_SIGNATURE && systemctl --user start hyprland-vm-session.target
    exec-once = lys-shell shell > /tmp/lys/shell.log 2>&1
  '';

  inherit (import ./vm-tools.nix { inherit pkgs self; }) vmclick portalClient testApp;

  shellConfig = (pkgs.formats.yaml { }).generate "config.yaml" {
    login.lock = {
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
          "nightlight"
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
      imports = [
        self.nixosModules.default
        lys-secrets.nixosModules.default
        lys-login.nixosModules.default
      ];

      programs.lys-shell.enable = true;
      programs.lys-login = {
        enable = true;
        lock.enable = true;
      };
      services.lys-secrets.enable = true;

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

      # The session target a compositor starts once its environment is
      # in systemd, so graphical-session.target (and with it the portal
      # frontend) comes up as on a desktop.
      systemd.user.targets.hyprland-vm-session = {
        bindsTo = [ "graphical-session.target" ];
        wants = [ "graphical-session-pre.target" ];
        after = [ "graphical-session-pre.target" ];
      };

      # The screencast stream is a PipeWire node.
      services.pipewire.enable = true;

      environment.systemPackages = [
        portalClient
        testApp
        vmclick
        pkgs.hyprland
        pkgs.foot
        pkgs.libnotify
        pkgs.dbus
        pkgs.libsecret
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
        print(machine.execute("tail -n 40 /tmp/lys/*.json; cat /tmp/lys/*.log | grep -v aquamarine | tail -n 80")[1])
        print(machine.execute("journalctl _UID=${toString uid} --no-pager | grep -v aquamarine | grep -i 'lys\\|portal' | tail -n 60")[1])
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

    # A dropdown opens on a real click, through the compositor's
    # virtual pointer, which spans the whole output layout.
    def click(x, y):
        mons = hypr_json("monitors")
        width = max(m["x"] + m["width"] for m in mons)
        height = max(m["y"] + m["height"] for m in mons)
        machine.succeed(
            f"su - ${user} -c '{env} WAYLAND_DISPLAY=$(basename $(ls ${runtimeDir}/wayland-* | head -1)) "
            f"vmclick {x} {y} {width} {height}'"
        )

    def layer(namespace):
        for mon in hypr_json("layers").values():
            for level in mon["levels"].values():
                for found in level:
                    if found["namespace"] == namespace:
                        return found
        raise Exception(f"no {namespace} layer")

    click(640, 22)
    time.sleep(2)
    machine.screenshot("06b-calendar-dropdown")
    machine.send_key("esc")
    time.sleep(1)

    # The portal on Hyprland: an application's screenshot comes back as
    # a file, and a screencast request raises the share picker over the
    # windows hyprland_toplevel_export lists.
    machine.succeed(f"su - ${user} -c '{env} portal-client screenshot > /tmp/lys/shot.json 2>&1 &'")
    # The frontend asks the user once whether an app may screenshot
    # without a dialog of its own: the shell's consent prompt, granted
    # with its Allow button, the last one at the bottom right.
    wait("the screenshot consent dialog", lambda: "lys-shell-portal-dialog" in namespaces())
    time.sleep(1)
    machine.screenshot("06c-screenshot-consent")
    dialog = layer("lys-shell-portal-dialog")
    click(dialog["x"] + dialog["w"] - 60, dialog["y"] + dialog["h"] - 32)
    wait("the screenshot answer", lambda: machine.execute("grep -q code /tmp/lys/shot.json")[0] == 0, tries=30)
    shot = json.loads(machine.succeed("cat /tmp/lys/shot.json"))
    uri = shot["results"]["uri"]
    if not uri.startswith("file://"):
        raise Exception(f"screenshot portal answered {shot}")
    machine.succeed(f"test -s '{uri[len('file://'):]}'")

    machine.succeed(f"su - ${user} -c '{env} portal-client screencast > /tmp/lys/cast.json 2>&1 &'")
    wait("the share picker", lambda: "lys-shell-share-picker" in namespaces())
    time.sleep(2)
    machine.screenshot("06c-share-picker")
    machine.send_key("esc")
    wait("the cancelled screencast", lambda: machine.execute("grep -q code /tmp/lys/cast.json")[0] == 0)
    print(machine.succeed("cat /tmp/lys/cast.json"))

    # The region overlay and the colour picker freeze the output and
    # close on Escape.
    shell_bg("screenshot region", "region")
    wait("the region overlay", lambda: "lys-shell-region-overlay" in namespaces())
    time.sleep(1)
    machine.screenshot("06d-region-overlay")
    machine.send_key("esc")
    wait("the region overlay to close", lambda: "lys-shell-region-overlay" not in namespaces())

    # The OSD and a notification popup map on the focused output.
    def monitors_of(namespace):
        return [
            name
            for name, mon in hypr_json("layers").items()
            for level in mon["levels"].values()
            if any(found["namespace"] == namespace for found in level)
        ]

    # The OSD and notification popups land on the primary output
    # whichever output has the focus, and on the connector the config
    # names once it names one.
    def toast_and_notify(word):
        shell(f"toast --duration 4000 {word}")
        machine.succeed(f"su - ${user} -c '{env} notify-send {word}'")

    hypr("dispatch focusmonitor HEADLESS-2")
    toast_and_notify("Primary")
    wait("the OSD", lambda: "lys-shell-osd" in namespaces())
    wait("the popup", lambda: "lys-shell-notification-popup" in namespaces())
    if monitors_of("lys-shell-osd") != ["Virtual-1"] or "Virtual-1" not in monitors_of("lys-shell-notification-popup"):
        raise Exception(f"default placement: OSD on {monitors_of('lys-shell-osd')}, popups on {monitors_of('lys-shell-notification-popup')}, want the primary Virtual-1")
    machine.screenshot("06e-osd-and-popup-on-primary")
    wait("the OSD to time out", lambda: "lys-shell-osd" not in namespaces(), tries=15)
    shell("config set osd.monitor HEADLESS-2")
    shell("config set modules.notifications.popup-monitor HEADLESS-2")
    toast_and_notify("Configured")
    wait("the OSD on the configured output", lambda: monitors_of("lys-shell-osd") == ["HEADLESS-2"])
    wait("a popup on the configured output", lambda: "HEADLESS-2" in monitors_of("lys-shell-notification-popup"))
    machine.screenshot("06e-osd-and-popup-on-configured-output")
    shell("config reset osd.monitor")
    shell("config reset modules.notifications.popup-monitor")
    hypr("dispatch focusmonitor Virtual-1")

    # The colour picker answers the portal's PickColor with the colour
    # under the click.
    machine.succeed(f"su - ${user} -c '{env} portal-client pickcolor > /tmp/lys/color.json 2>&1 &'")
    wait("the colour picker", lambda: "lys-shell-color-picker" in namespaces())
    time.sleep(1)
    machine.screenshot("06f-color-picker")
    click(400, 400)
    wait("the picked colour", lambda: machine.execute("grep -q code /tmp/lys/color.json")[0] == 0, tries=30)
    picked = json.loads(machine.succeed("cat /tmp/lys/color.json"))
    color = picked["results"].get("color")
    if picked["code"] != 0 or not color or len(color) != 3:
        raise Exception(f"PickColor answered {picked}")

    # A global shortcut: the app binds it through the portal, the user
    # allows it, the compositor lists it, and its trigger reaches the
    # app as Activated.
    machine.succeed(f"su - ${user} -c '{env} portal-client globalshortcuts > /tmp/lys/shortcuts.json 2>&1 &'")
    wait("the shortcut consent dialog", lambda: "lys-shell-portal-dialog" in namespaces())
    time.sleep(1)
    machine.screenshot("06g-shortcut-consent")
    dialog = layer("lys-shell-portal-dialog")
    click(dialog["x"] + dialog["w"] - 60, dialog["y"] + dialog["h"] - 32)
    wait("the bound shortcut", lambda: machine.execute("grep -q bound /tmp/lys/shortcuts.json")[0] == 0, tries=30)
    bound = json.loads(machine.succeed("head -n1 /tmp/lys/shortcuts.json"))
    if bound["bound"] != 0:
        raise Exception(f"BindShortcuts answered {bound}")
    listed = [line.split(" -> ")[0].strip() for line in hypr("globalshortcuts").splitlines() if "lys-vm-test" in line]
    if len(listed) != 1:
        raise Exception(f"the compositor lists {listed} for the bound shortcut")
    hypr(f"dispatch global {listed[0]}")
    wait("the shortcut's activation", lambda: machine.execute("grep -q activated /tmp/lys/shortcuts.json")[0] == 0, tries=30)

    def logged(log, line):
        return line in machine.execute(f"cat /tmp/lys/{log}.log")[1]

    # The night light, the bar's last module, warms the outputs through
    # the compositor's CTM protocol on a click, with no hyprsunset
    # installed, and returns them to normal on the next.
    if machine.execute("command -v hyprsunset")[0] == 0:
        raise Exception("hyprsunset is installed; the night light must not need it")
    click(1262, 22)
    wait("the night light on", lambda: logged("shell", "nightlight: on at 5000K 100% through hyprland_ctm_control_manager_v1"), tries=30)
    machine.screenshot("06h-nightlight-on")
    click(1262, 22)
    wait("the night light off", lambda: logged("shell", "nightlight: off"), tries=30)
    if logged("shell", "another program controls the display colours"):
        raise Exception("the compositor refused the night light")

    # The session lock covers every output and a typed password lifts it.

    def count(log, line):
        return machine.execute(f"cat /tmp/lys/{log}.log")[1].count(line)

    shell("lock")
    wait("the session lock", lambda: logged("shell", "lock: session locked (2 monitors)"))
    time.sleep(1)
    machine.screenshot("07-locked")
    machine.send_chars("${password}\n")
    wait("the unlock", lambda: logged("shell", "lock: session unlocked"))
    machine.screenshot("08-unlocked")

    # lys-secrets serves the Secret Service from the session start, and
    # the password the lock screen verified created and unlocked its login
    # collection: a store and lookup need no prompt.
    def secret_tool(args, stdin=""):
        return machine.execute(f"su - ${user} -c 'printf %s {stdin} | env {env} timeout 20 secret-tool {args}'")

    wait("lys-secrets running", lambda: machine.execute(f"su - ${user} -c '{env} systemctl --user is-active lys-secrets'")[0] == 0)
    status, _ = secret_tool("store --label=vm vm-test 1", "vm-secret")
    if status != 0:
        raise Exception("secret-tool store failed after the lock screen unlock")
    if secret_tool("lookup vm-test 1")[1] != "vm-secret":
        raise Exception("secret-tool lookup did not return the stored secret")
    if machine.execute("ls /home/${user}/.local/share/lys-secrets/login.keyring")[0] != 0:
        raise Exception("no login collection on disk")
    if machine.execute("grep -c vm-secret /home/${user}/.local/share/lys-secrets/login.keyring")[0] == 0:
        raise Exception("the secret is stored in the clear")

    # Restarted, lys-secrets starts locked; a lookup asks through the
    # shell's password dialog, where the session password unlocks it.
    machine.succeed(f"su - ${user} -c '{env} systemctl --user restart lys-secrets'")
    wait("lys-secrets restarted", lambda: machine.execute(f"su - ${user} -c '{env} systemctl --user is-active lys-secrets'")[0] == 0)
    machine.succeed(f"su - ${user} -c '{env} timeout 60 secret-tool lookup vm-test 1 > /tmp/lys/lookup.txt 2>&1 &'")
    wait("the keyring password dialog", lambda: "lys-shell-portal-dialog" in namespaces())
    time.sleep(1)
    machine.screenshot("08b-keyring-unlock-dialog")
    machine.send_chars("${password}\n")
    wait("the lookup through the dialog", lambda: machine.execute("cat /tmp/lys/lookup.txt")[1] == "vm-secret", tries=30)

    # A lock client that dies leaves the compositor locked
    # (allow_session_lock_restore) and logind's LockedHint set: the shell
    # starts lys-login again, which re-acquires the lock.
    shell("lock")
    wait("the second lock", lambda: count("shell", "lock: session locked") == 2)
    machine.succeed("pkill -KILL -f 'lys-login lock'")
    wait("the shell to re-acquire the lock", lambda: logged("shell", "still locked; re-acquiring") and count("shell", "lock: session locked") == 3)
    time.sleep(1)
    machine.screenshot("09-relocked-after-the-lock-client-died")
    # A shell restarted while locked leaves the lock to the lys-login
    # holding it, which outlives the shell.
    machine.succeed("pkill -KILL -f 'lys-shell shell'")
    hypr('dispatch exec "lys-shell shell > /tmp/lys/shell2.log 2>&1"')
    wait("the restarted shell to find the lock held", lambda: logged("shell2", "re-acquiring") and logged("shell2", "another lys-login already holds the session locked"))
    machine.screenshot("10-locked-after-a-shell-restart")
    machine.send_chars("${password}\n")
    wait("the unlock after the restart", lambda: count("shell", "lock: session unlocked") == 2)
    wait("the bar after the restart", lambda: bars_on(names))
    machine.screenshot("11-unlocked-after-restart")

    hypr("output remove HEADLESS-2")
    wait("the unplugged output's bar to go", lambda: "lys-shell-bar-HEADLESS-2" not in namespaces())
    if not bars_on(["Virtual-1"]):
        raise Exception("unplugging an output took the remaining bar with it")

    print(machine.execute("cat /tmp/lys/shell.log /tmp/lys/shell2.log")[1])
  '';
}
