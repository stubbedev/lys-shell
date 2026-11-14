{
  pkgs,
  lib,
  self,
  lys-wm,
  lys-login,
}:

let
  user = "alice";
  password = "lys-shell-vm";
  uid = 1000;
  runtimeDir = "/run/user/${toString uid}";

  inherit (import ./vm-tools.nix { inherit pkgs self; }) vmclick portalClient testApp;

  flatWallpaper =
    pkgs.runCommand "flat-wallpaper.png" { nativeBuildInputs = [ pkgs.imagemagick ]; }
      ''
        magick -size 64x64 xc:'#20242c' "png:$out"
      '';

  wmConfig = (pkgs.formats.yaml { }).generate "lys-wm.yaml" {
    input.keyboard = {
      layout = "us,de";
      options = "grp:alt_shift_toggle";
    };
    client_hotkeys = true;
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
    animations.indicators = false;
    osd.duration = 8000;
    wallpaper.wallpaper = "${flatWallpaper}";
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
        lys-login.nixosModules.default
      ];

      programs.lys-shell = {
        enable = true;
        systemd.enable = true;
      };
      programs.lys-login = {
        enable = true;
        lock.enable = true;
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
      environment.sessionVariables = {
        LIBGL_ALWAYS_SOFTWARE = "1";
        LYS_WM_RENDERER = "gles";
      };
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

      security.auditd.enable = true;
      security.audit.enable = true;
      security.audit.rules = [ "-a always,exit -F arch=b64 -S execve,execveat -F uid=${toString uid} -k user-exec" ];

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
        # lys-login logs to the shell's stream from a scope of its own:
        # the stream's identifier finds both, the unit only the shell.
        return machine.execute("journalctl --no-pager -o cat SYSLOG_IDENTIFIER=lys-shell")[1]

    bus = "XDG_RUNTIME_DIR=${runtimeDir} DBUS_SESSION_BUS_ADDRESS=unix:path=${runtimeDir}/bus"

    def logs():
        print(machine.execute("cat /tmp/lys/*.log | tail -n 80; tail -n 40 /tmp/lys/events.jsonl")[1])
        print(shell_log()[-8000:])
        print(machine.execute(f"su - ${user} -c '{bus} systemctl --user status --no-pager lys-shell.service lys-wm-session.target graphical-session.target'")[1])

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
    env = f"{bus} LYS_WM_SOCKET={sock} WAYLAND_DISPLAY={display}"

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

    def settled(rect):
        shown = [region(*rect)]
        def still():
            shown.append(region(*rect))
            return shown[-1] == shown[-2]
        wait(f"{rect} to stop changing", still, tries=20, pause=0.25)
        return shown[-1]

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

    def shell_execs():
        records = machine.execute("ausearch --raw -k user-exec")[1]
        return [line for line in records.splitlines() if line.startswith("type=SYSCALL") and "lys-shell" in line]

    def middle_of(o):
        r = o["rect"]
        return (r["x"] + r["width"] // 4, r["y"] + r["height"] // 4, r["width"] // 2, r["height"] // 2)

    def middles():
        return [region(*middle_of(o)) for o in (first, second)]

    calm = middles()
    execs = shell_execs()
    dispatch("global dev.stubbe.lys.shell:launcher")
    wait("the launcher through its global shortcut", lambda: middles() != calm, tries=30)
    shot("02-launcher-from-its-shortcut")
    machine.send_key("esc")
    wait("the launcher to close", lambda: middles() == calm, tries=30)
    hidden = False
    for action in ("bar-toggle", "bar-toggle", "bar-hide", "bar-show"):
        hidden = action == "bar-hide" or action == "bar-toggle" and not hidden
        dispatch(f"global dev.stubbe.lys.shell:{action}")
        for o in (first, second):
            wait(f"{action} on {o['name']}", lambda o=o: (colors(*bar_strip(o)) <= 2) == hidden, tries=30)
    started = shell_execs()[len(execs):]
    if started:
        logs()
        print("\n".join(started))
        print(machine.execute("ausearch -i -k user-exec | tail -n 60")[1])
        raise Exception(f"the shortcuts started lys-shell {len(started)} times, want none")
    run("lys-shell --version")
    wait("the exec count to see a CLI run", lambda: len(shell_execs()) > len(execs), tries=10)

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

    above_foot = foot["rect"]["y"] - out["rect"]["y"]
    bar_y = out["rect"]["y"] + above_foot // 2

    def title_strip(o):
        r = o["rect"]
        return (r["x"], r["y"], r["width"] // 3, above_foot)

    def right_strip(o):
        r = o["rect"]
        return (r["x"] + r["width"] * 2 // 3, r["y"], r["width"] // 3, above_foot)

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
        click(x, bar_y)
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

    centre = out["rect"]["x"] + out["rect"]["width"] // 2
    below_clock = (centre - 100, out["rect"]["y"] + above_foot, 200, 120)
    away = (out["rect"]["x"] + out["rect"]["width"] - 60, out["rect"]["y"] + out["rect"]["height"] - 60)

    def typed_reaches_foot(name):
        machine.send_chars(f"touch /tmp/lys/{name}\n")
        wait(f"{name} from keys typed into foot", lambda: machine.execute(f"test -e /tmp/lys/{name}")[0] == 0, tries=20)

    closed = region(*below_clock)
    click(centre, bar_y)
    wait("the clock's dropdown to open", lambda: region(*below_clock) != closed, tries=20)
    settled(below_clock)
    seconds = int(machine.succeed("date +%S"))
    if seconds > 40:
        time.sleep(61 - seconds)
    opened = settled(below_clock)
    shot("06a-dropdown-open")
    typed_reaches_foot("key-beside-dropdown")
    if region(*below_clock) != opened:
        raise Exception("a key typed beside the open dropdown closed or changed it")
    if (wmctl("activewindow") or {}).get("app_id") != "foot":
        raise Exception("the open dropdown took the focus from foot")

    click(*away)
    wait("a click away to close the dropdown", lambda: region(*below_clock) == closed, tries=20)
    shot("06b-dropdown-clicked-away")
    typed_reaches_foot("key-after-click-away")

    click(centre, bar_y)
    wait("the dropdown to reopen for Esc beside it", lambda: region(*below_clock) != closed, tries=20)
    reopened = settled(below_clock)
    machine.send_key("esc")
    # Not compared with closed: the pointer still rests on the clock, whose
    # tooltip then shows there.
    wait("Esc beside the dropdown to close it", lambda: region(*below_clock) != reopened, tries=20)
    if (wmctl("activewindow") or {}).get("app_id") != "foot":
        raise Exception("Esc beside the dropdown took the focus from foot")
    # Readline reads a leaked ESC and the next t as M-t, so the touch fails.
    typed_reaches_foot("esc-beside")

    click(centre, bar_y)
    wait("the dropdown to reopen", lambda: region(*below_clock) != closed, tries=20)
    settled(below_clock)
    click(centre, below_clock[1] + 20)
    time.sleep(0.5)
    machine.send_key("esc")
    wait("Esc to close the dropdown it reached", lambda: region(*below_clock) == closed, tries=20)
    typed_reaches_foot("key-after-esc")

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
    # A lock client that dies leaves the session locked; the shell starts
    # lys-login again, which re-acquires it.
    machine.succeed("pkill -KILL -f 'lys-login lock'")
    wait("the shell to re-acquire the lock", lambda: "still locked; re-acquiring" in shell_log() and count("lock: session locked (2 monitors)") == 3, tries=60)
    time.sleep(1)
    shot("12-relocked-after-the-lock-client-died")
    # A shell restarted while locked leaves the lock to the lys-login
    # holding it, in a scope of its own.
    run("systemctl --user kill --signal=KILL lys-shell.service")
    wait("the restarted shell to find the lock held", lambda: count("another lys-login already holds the session locked") >= 1, tries=60)
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

    def layers_open(namespace):
        still = set()
        for e in events("layer"):
            if e["namespace"] != namespace:
                continue
            if e["change"] == "open":
                still.add(e["id"])
            elif e["change"] == "close":
                still.discard(e["id"])
        return still

    # A portal dialog answers its request first and plays its exit after,
    # so the app has its answer while the dialog is still fading: the
    # screen a later step compares against is captured once the dialog's
    # surface is gone, not once the answer is in.
    def calm_capture(name):
        wait("the portal dialogs to leave the screen", lambda: not layers_open("lys-shell-portal-dialog"), tries=30)
        return capture(name, out)

    def allow(mode, log, name):
        calm = calm_capture("before-" + name)
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

    # lys-wm offers xx_hotkey_v1 (client_hotkeys): the bind shows the
    # shortcuts editor with the app's preferred trigger, saved as is, and
    # the real key fires the shortcut.
    allow("globalshortcuts", "shortcuts.json", "14-shortcut-editor")
    wait("the bound shortcut", lambda: machine.execute("grep -q bound /tmp/lys/shortcuts.json")[0] == 0, tries=30)
    bound = json.loads(machine.succeed("head -n1 /tmp/lys/shortcuts.json"))
    if bound["bound"] != 0:
        raise Exception(f"BindShortcuts answered {bound}")
    described = [s["trigger_description"] for s in bound["results"]["shortcuts"]]
    if described != ["Ctrl+Alt+P"]:
        raise Exception(f"the bound shortcut is described as {described}, want the preferred Ctrl+Alt+P")
    machine.send_key("ctrl-alt-p")
    wait("the shortcut's activation", lambda: machine.execute("grep -q activated /tmp/lys/shortcuts.json")[0] == 0, tries=30)
    print("global shortcut:", machine.succeed("cat /tmp/lys/shortcuts.json"))

    def shell_state():
        return run("systemctl --user show -P ActiveState lys-shell.service").strip()

    calm = calm_capture("before-chooser")

    def chooser_shown():
        box = changed_box(calm, capture("chooser", out))
        print("changed against before-chooser:", box)
        return box is not None and box[2] - box[0] > 400 and box[3] - box[1] > 300

    run("systemctl --user stop lys-shell.service")
    wait("lys-shell to stop", lambda: shell_state() == "inactive", tries=30)
    machine.succeed(f"su - ${user} -c '{env} portal-client filechooser > /tmp/lys/chooser.json 2>&1 &'")
    wait("the file chooser request to start lys-shell", lambda: shell_state() == "active", tries=30)
    wait("the file chooser dialog from the activated shell", chooser_shown, tries=30)
    time.sleep(1)
    shot("15-file-chooser-after-activation")
    machine.send_key("esc")
    wait("the file chooser answer", lambda: machine.execute("grep -q code /tmp/lys/chooser.json")[0] == 0, tries=30)
    answer = json.loads(machine.succeed("cat /tmp/lys/chooser.json"))
    print("portal file chooser:", answer)
    if answer["code"] != 1:
        raise Exception(f"Esc in the file chooser answered {answer}, want cancelled")
    wait("the file chooser to close", lambda: not chooser_shown(), tries=30)

    logs()
  '';
}
