{
  pkgs,
  lib,
  self,
  lys-wm,
}:

let
  user = "alice";
  password = "lys-shell-vm";
  uid = 1000;
  runtimeDir = "/run/user/${toString uid}";

  inherit (import ./vm-tools.nix { inherit pkgs self; }) vmclick portalClient testApp;

  wmConfig = (pkgs.formats.yaml { }).generate "lys-wm.yaml" {
    input.keyboard = {
      layout = "us,de";
      options = "grp:alt_shift_toggle";
    };
    binds = [
      {
        chord = "SUPER + R";
        action = "submap resize";
      }
    ];
    submaps.resize = [
      {
        chord = "Escape";
        action = "submap reset";
      }
    ];
  };

  shellConfig = (pkgs.formats.yaml { }).generate "config.yaml" {
    login.lock = {
      enabled = true;
      pam-service = "lys-shell";
    };
    osd.duration = 8000;
    bar.layout = [
      {
        monitor = "*";
        left = [
          "lys-wm-workspaces"
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
  name = "lys-shell-lys-wm-vm";

  nodes.machine =
    { ... }:
    {
      imports = [
        self.nixosModules.default
        lys-wm.nixosModules.default
      ];

      programs.lys-shell = {
        enable = true;
        lock.enable = true;
        systemd.enable = true;
      };
      programs.lys-wm.enable = true;

      virtualisation.graphics = true;
      virtualisation.cores = 4;
      virtualisation.memorySize = 4096;
      virtualisation.qemu.options = [
        "-vga none"
        "-device virtio-gpu-pci"
        "-device virtio-gpu-pci"
      ];

      services.getty.autologinUser = user;
      environment.loginShellInit = ''
        if [ "$(tty)" = /dev/tty1 ]; then
          exec lys-wm --systemd > /tmp/lys/lys-wm.log 2>&1
        fi
      '';
      environment.sessionVariables.LIBGL_ALWAYS_SOFTWARE = "1";
      systemd.tmpfiles.rules = [
        "d /tmp/lys 0777 root root -"
        "d /home/${user}/.config 0755 ${user} users -"
        "d /home/${user}/.config/lys-wm 0755 ${user} users -"
        "d /home/${user}/.config/lys-shell 0755 ${user} users -"
        "L+ /home/${user}/.config/lys-wm/config.yaml - - - - ${wmConfig}"
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

      services.pipewire.enable = true;

      environment.systemPackages = [
        portalClient
        testApp
        vmclick
        pkgs.foot
        pkgs.grim
        pkgs.file
        pkgs.libnotify
      ];
    };

  testScript = ''
    import json
    import os
    import time

    start_all()
    machine.wait_for_unit("multi-user.target")

    def shell_log():
        return machine.execute("journalctl --no-pager -o cat _SYSTEMD_USER_UNIT=lys-shell.service")[1]

    def logs():
        print(machine.execute("cat /tmp/lys/*.log | tail -n 80; tail -n 40 /tmp/lys/events.jsonl")[1])
        print(shell_log()[-8000:])
        print(machine.execute(f"su - ${user} -c '{env} systemctl --user status --no-pager lys-shell.service lys-wm-session.target graphical-session.target'")[1])

    def wait(what, check, tries=60, pause=1.0):
        for _ in range(tries):
            if check():
                return
            time.sleep(pause)
        logs()
        raise Exception("timed out waiting for " + what)

    wait("lys-wm's socket", lambda: machine.execute("ls ${runtimeDir}/lys-wm/*.sock")[0] == 0, tries=120)
    sock = machine.succeed("ls ${runtimeDir}/lys-wm/*.sock").split()[0]
    display = os.path.basename(sock)[: -len(".sock")]
    env = (
        f"XDG_RUNTIME_DIR=${runtimeDir} LYS_WM_SOCKET={sock} WAYLAND_DISPLAY={display} "
        "DBUS_SESSION_BUS_ADDRESS=unix:path=${runtimeDir}/bus"
    )

    def run(command):
        return machine.succeed(f"su - ${user} -c '{env} {command}'")

    def run_bg(command, log):
        machine.succeed(f"su - ${user} -c '{env} {command} > /tmp/lys/{log} 2>&1 &'")

    def wmctl(args):
        return json.loads(run(f"lys-wmctl {args}"))

    def dispatch(action):
        run(f"lys-wmctl dispatch {action}")

    def events(kind):
        found = []
        for line in machine.execute("cat /tmp/lys/events.jsonl")[1].splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("event") == kind:
                found.append(event)
        return found

    def region(x, y, w, h):
        return run(f"grim -t ppm -g \"{x},{y} {w}x{h}\" - | tail -n +4 | sha256sum").split()[0]

    def colors(x, y, w, h):
        return int(run(f"grim -t ppm -g \"{x},{y} {w}x{h}\" - | tail -n +4 | od -An -v -tx1 -w3 | sort -u | wc -l"))

    def shot(name):
        machine.screenshot(name)
        for o in outputs():
            path = f"/tmp/lys/{name}-{o['name']}.png"
            run(f"grim -o {o['name']} {path}")
            machine.copy_from_machine(path)

    def outputs():
        return sorted(wmctl("outputs"), key=lambda o: o["name"])

    def windows():
        return wmctl("windows")

    def focused_workspace():
        return [w["id"] for w in wmctl("workspaces") if w["focused"]]

    def count(line):
        return shell_log().count(line)

    run_bg("lys-wmctl watch", "events.jsonl")

    wait("both outputs", lambda: [o["name"] for o in outputs()] == ["Virtual-1", "Virtual-2"])
    first, second = outputs()
    print("outputs:", first, second)

    def bar_strip(o):
        r = o["rect"]
        return (r["x"], r["y"], r["width"], 24)

    for o in (first, second):
        wait(f"the bar on {o['name']}", lambda o=o: colors(*bar_strip(o)) > 2, tries=120)
    shot("01-bars")

    dispatch("exec foot")
    wait("a foot window", lambda: len(windows()) == 1, tries=60)
    foot = windows()[0]
    print("foot:", foot)
    out = next(o for o in outputs() if o["name"] == foot["output"])
    if foot["rect"]["y"] - out["rect"]["y"] < 20:
        raise Exception(f"the window starts at {foot['rect']} on {out}: the bar reserves no space")
    wait("foot focused", lambda: wmctl("activewindow") is not None and wmctl("activewindow")["app_id"] == "foot")
    time.sleep(2)
    shot("02-foot-focused")

    def title_strip(o):
        r = o["rect"]
        return (r["x"], r["y"], r["width"] // 3, 24)

    def right_strip(o):
        r = o["rect"]
        return (r["x"] + r["width"] * 2 // 3, r["y"], r["width"] // 3, 24)

    titled = region(*title_strip(out))
    machine.send_chars("PS1='$ '\n")
    machine.send_chars("printf '\\033]2;renamed-by-test\\007'\n")
    wait("the new title", lambda: (wmctl("activewindow") or {}).get("title") == "renamed-by-test")
    wait("the bar to show the new title", lambda: region(*title_strip(out)) != titled, tries=20)
    shot("03-title-renamed")

    renamed = region(*title_strip(out))
    shown = wmctl("workspaces")
    print("workspaces:", shown)
    dispatch("workspace 5")
    wait("workspace 5 focused", lambda: focused_workspace() == [5])
    wait("the bar to follow the empty workspace", lambda: region(*title_strip(out)) != renamed, tries=20)
    shot("04-workspace-5")
    print("workspaces:", wmctl("workspaces"))

    def click(x, y):
        layout = outputs()
        width = max(o["rect"]["x"] + o["rect"]["width"] for o in layout)
        height = max(o["rect"]["y"] + o["rect"]["height"] for o in layout)
        run(f"vmclick {x} {y} {width} {height}")

    clicked = None
    for x in range(out["rect"]["x"] + 6, out["rect"]["x"] + 160, 6):
        click(x, out["rect"]["y"] + 12)
        time.sleep(0.5)
        if focused_workspace() == [foot["workspace"]]:
            clicked = x
            break
    if clicked is None:
        raise Exception(f"no click on the bar switched back to workspace {foot['workspace']}")
    print("workspace segment clicked at x =", clicked)
    wait("the bar's dispatch in the event stream", lambda: any(e["action"] == f"workspace {foot['workspace']}" for e in events("dispatch")))
    shot("05-workspace-clicked")

    plain = region(*right_strip(out))
    dispatch("submap resize")
    wait("the submap event", lambda: any(e["submap"] == "resize" for e in events("submap")))
    wait("the keybind mode to show", lambda: region(*right_strip(out)) != plain, tries=20)
    shot("06-submap-resize")
    machine.send_key("esc")
    wait("the submap reset", lambda: any(e["submap"] == "" for e in events("submap")))
    wait("the keybind mode to hide", lambda: region(*right_strip(out)) == plain, tries=20)

    dispatch("workspace 6")
    wait("workspace 6 focused", lambda: focused_workspace() == [6])
    time.sleep(1)
    r = out["rect"]
    toast_area = (r["x"] + r["width"] // 4, r["y"] + r["height"] // 2, r["width"] // 2, r["height"] // 2 - 40)
    if colors(*toast_area) != 1:
        raise Exception("the toast area is not empty before the layout switch")
    before = region(*right_strip(out))
    machine.send_key("alt-shift")
    wait("the layout event", lambda: any(e["layout"]["index"] == 1 for e in events("layout")))
    print("layout events:", events("layout"))
    wait("the layout toast", lambda: colors(*toast_area) > 1, tries=20, pause=0.25)
    shot("07-layout-toast")
    wait("the keyboard layout module", lambda: region(*right_strip(out)) != before, tries=20)
    machine.send_key("alt-shift")
    wait("the layout back", lambda: events("layout")[-1]["layout"]["index"] == 0)

    run("lys-shell toast --duration 6000 Hello-from-the-test")
    wait("the CLI toast", lambda: colors(*toast_area) > 1, tries=20, pause=0.25)
    shot("08-cli-toast")

    dispatch("workspace " + str(foot["workspace"]))
    wait("foot focused again", lambda: (wmctl("activewindow") or {}).get("app_id") == "foot")
    saved = run(f"lys-shell screenshot output {out['name']}").strip()
    print(saved)
    path = saved.removeprefix("Screenshot saved to ")
    machine.succeed(f"test -s '{path}'")
    size = machine.succeed(f"file '{path}'")
    if f"{out['rect']['width']} x {out['rect']['height']}" not in size:
        raise Exception(f"the output screenshot is {size}, want the output's size")
    saved = run("lys-shell screenshot window").strip()
    print(saved)
    path = saved.removeprefix("Screenshot saved to ")
    size = machine.succeed(f"file '{path}'")
    if f"{foot['rect']['width']} x {foot['rect']['height']}" not in size:
        raise Exception(f"the window screenshot is {size}, want foot's {foot['rect']}")
    machine.copy_from_machine(path)

    run("lys-shell lock")
    wait("the session lock", lambda: count("lock: session locked (2 monitors)") == 1)
    time.sleep(1)
    shot("10-locked")
    machine.send_chars("${password}\n")
    wait("the unlock", lambda: count("lock: session unlocked") == 1)
    shot("11-unlocked")

    run("lys-shell lock")
    wait("the second lock", lambda: count("lock: session locked (2 monitors)") == 2)
    run("systemctl --user kill --signal=KILL lys-shell.service")
    wait("the restarted shell to re-acquire the lock", lambda: "re-acquiring" in shell_log() and count("lock: session locked (2 monitors)") == 3, tries=60)
    time.sleep(1)
    shot("12-relocked-after-restart")
    machine.send_chars("${password}\n")
    wait("the unlock after the restart", lambda: count("lock: session unlocked") == 2)
    for o in (first, second):
        wait(f"the bar on {o['name']} after the restart", lambda o=o: colors(*bar_strip(o)) > 2)
    shot("13-unlocked-after-restart")

    def capture(name, o):
        path = f"/tmp/lys/{name}.ppm"
        run(f"grim -t ppm -o {o['name']} {path}")
        machine.copy_from_machine(path)
        with open(os.path.join(os.environ["out"], f"{name}.ppm"), "rb") as f:
            data = f.read()
        header = data.split(b"\n", 3)
        width, height = map(int, header[1].split())
        return width, height, header[3]

    def changed_box(before, after):
        width, height, a = before
        _, _, b = after
        rows = [y for y in range(foot["rect"]["y"], height) if a[y * width * 3 : (y + 1) * width * 3] != b[y * width * 3 : (y + 1) * width * 3]]
        if not rows:
            return None
        cols = [x for x in range(width) if any(a[(y * width + x) * 3 : (y * width + x) * 3 + 3] != b[(y * width + x) * 3 : (y * width + x) * 3 + 3] for y in range(rows[0], rows[-1] + 1, 4))]
        return cols[0], rows[0], cols[-1], rows[-1]

    def allow(mode, log, name):
        calm = capture("before-" + name, out)
        machine.succeed(f"su - ${user} -c '{env} portal-client {mode} > /tmp/lys/{log} 2>&1 &'")

        def dialog_shown():
            box = changed_box(calm, capture(name, out))
            return box is not None and box[2] - box[0] > 100

        wait(f"the {mode} consent dialog", dialog_shown, tries=30)
        time.sleep(1)
        shot(name)
        box = changed_box(calm, capture(name, out))
        print(f"{mode} consent dialog at", box)
        click(out["rect"]["x"] + box[2] - 60, out["rect"]["y"] + box[3] - 32)

    allow("screenshot", "shot.json", "09-screenshot-consent")
    wait("the screenshot answer", lambda: machine.execute("grep -q code /tmp/lys/shot.json")[0] == 0, tries=30)
    answer = json.loads(machine.succeed("cat /tmp/lys/shot.json"))
    print("portal screenshot:", answer)
    uri = answer["results"]["uri"]
    if not uri.startswith("file://"):
        raise Exception(f"the screenshot portal answered {answer}")
    machine.succeed(f"test -s '{uri[len('file://'):]}'")

    allow("globalshortcuts", "shortcuts.json", "14-shortcut-consent")
    wait("the bound shortcut", lambda: machine.execute("grep -q bound /tmp/lys/shortcuts.json")[0] == 0, tries=30)
    bound = json.loads(machine.succeed("head -n1 /tmp/lys/shortcuts.json"))
    if bound["bound"] != 0:
        raise Exception(f"BindShortcuts answered {bound}")
    dispatch("global dev.stubbe.lys.VmTest:lys-vm-test")
    wait("the shortcut's activation", lambda: machine.execute("grep -q activated /tmp/lys/shortcuts.json")[0] == 0, tries=30)
    print("global shortcut:", machine.succeed("cat /tmp/lys/shortcuts.json"))

    logs()
  '';
}
