self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.lys-shell;
  yamlFormat = pkgs.formats.yaml { };

  # Directory the greeter remembers the last-selected session in. Must be
  # writable by the greetd user (`greeter`); the tmpfiles rule below creates it.
  greeterStateDir = "/var/lib/lys-greeter";

  # Forces both render layers onto their software paths: wlroots' pixman
  # renderer (DRM dumb buffers, no EGL/GBM) for cage, and GTK's cairo GSK
  # renderer (no GL context) for the greeter client. This needs no GPU vendor
  # driver at all, so it always renders — on a VM, a driverless box, or a
  # non-NixOS install without nixGL. `renderer = "auto"` omits these and lets
  # each layer try acceleration and fall back to software on its own.
  softwareRenderEnv = [
    "WLR_RENDERER=pixman"
    "WLR_DRM_NO_MODIFIERS=1"
    "GSK_RENDERER=cairo"
    "GDK_DISABLE=gl"
  ];

  # cage draws its own cursor until the greeter window maps (and wlroots scales
  # it per output). Give both cage and the GTK greeter the same cursor as the
  # rest of the system.
  #
  # The greeter runs as the unprivileged `greeter` user (home /var/empty) and
  # cannot read a normal user's often-0700 home, so a cursor theme installed only
  # via home-manager is invisible to it — with no theme resolvable it shows the
  # large built-in fallback cursor. So we don't invent a lys-shell-specific cursor
  # knob: we inherit the system-wide cursor the user already configures
  # (`environment.sessionVariables`, falling back to `environment.variables`) and
  # point XCURSOR_PATH at the system icon dir, where a theme installed via
  # `environment.systemPackages` lands. Set your cursor there — install the theme
  # system-wide and export XCURSOR_THEME/XCURSOR_SIZE — and the greeter follows.
  # An explicit `greeter.settings.login.greeter.cursor-theme`/`cursor-size`
  # still wins; the former `greeter.settings.greeter.*` keys are read too, as
  # the greeter itself still reads them.
  # sessionVariables wins over variables (that's the layer users set cursor in).
  sysVars = config.environment.variables // config.environment.sessionVariables;
  greeterSettings = (cfg.greeter.settings.greeter or { }) // (cfg.greeter.settings.login.greeter or { });
  cursorName =
    if (greeterSettings.cursor-theme or "") != "" then
      greeterSettings.cursor-theme
    else
      (sysVars.XCURSOR_THEME or "");
  cursorSize =
    if (greeterSettings.cursor-size or null) != null then
      toString greeterSettings.cursor-size
    else
      (sysVars.XCURSOR_SIZE or "24");
  cursorEnv =
    [
      "XCURSOR_SIZE=${toString cursorSize}"
      "XCURSOR_PATH=/run/current-system/sw/share/icons"
    ]
    ++ lib.optional (cursorName != "") "XCURSOR_THEME=${cursorName}";

  # The greetd `default_session` command: a kiosk compositor (cage) hosting the
  # lys-shell greeter. The greeter discovers Wayland sessions from `session.dirs`,
  # lets the user pick one, and remembers the choice under `greeterStateDir`.
  # cage's `-s` allows VT switching. The greeter reads its theme from
  # /etc/lys-shell/config.yaml (written below from `greeter.settings`).
  #
  # Layering, outermost first: [graphicsWrapper] env[software vars] cage → greeter.
  # The graphicsWrapper (e.g. nixGL) must wrap the whole thing so it injects the
  # host GPU driver into cage *and* the greeter it spawns.
  greeterCommand =
    let
      cageExe = lib.getExe cfg.greeter.cagePackage;
      greeterExe = lib.getExe' cfg.greeter.package "lys-login";
      envArgs = lib.concatMapStringsSep " " (
        e: "--env ${lib.escapeShellArg e}"
      ) cfg.greeter.session.environment;
      sessionDirArgs = lib.concatMapStringsSep " " (
        d: "--sessions ${lib.escapeShellArg d}"
      ) cfg.greeter.session.dirs;
      xsessionDirArgs = lib.concatMapStringsSep " " (
        d: "--xsessions ${lib.escapeShellArg d}"
      ) cfg.greeter.session.x11Dirs;
      # Optional explicit fallback session, appended as a "Custom" entry.
      fallback = lib.optionalString (
        cfg.greeter.session.command != ""
      ) "-- ${cfg.greeter.session.command}";
      wrapperPrefix = lib.optionalString (
        cfg.greeter.graphicsWrapper != [ ]
      ) (lib.concatStringsSep " " cfg.greeter.graphicsWrapper + " ");
      envPrefix =
        "env "
        + lib.concatStringsSep " " (
          cursorEnv ++ lib.optionals (cfg.greeter.renderer == "software") softwareRenderEnv
        )
        + " ";
    in
    "${wrapperPrefix}${envPrefix}${cageExe} -s -- "
    + "${greeterExe} greet --config /etc/lys-shell/config.yaml "
    + "--state ${greeterStateDir}/last-session ${sessionDirArgs} ${xsessionDirArgs} ${envArgs} ${fallback}";
in
{
  options.programs.lys-shell = {
    enable = lib.mkEnableOption "the lys-shell desktop shell";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.lys-shell;
      defaultText = lib.literalExpression "lys-shell.packages.\${system}.lys-shell";
      description = "The lys-shell package to install.";
    };

    autoInstallDependencies = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Enable the backing system services lys-shell integrates with: UPower
        (battery), BlueZ (bluetooth), and GeoClue (location for the night light
        auto-schedule), plus wl-clipboard and xdg-utils. Set with `mkDefault` so
        your own settings win. Networking (NetworkManager),
        power-profiles-daemon, and PipeWire are intentionally left out — enable
        those deliberately to avoid clashing with your setup.
      '';
    };

    systemd = {
      enable = lib.mkEnableOption ''
        a systemd user service that runs `lys-shell shell` in the graphical session.
        Leave this off if you start the bar from your compositor config instead'';

      target = lib.mkOption {
        type = lib.types.str;
        default = "graphical-session.target";
        description = "Systemd target the lys-shell user service binds to.";
      };
    };

    portal = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Register lys-shell as the xdg-desktop-portal backend so screen sharing,
          screenshots, remote control, global shortcuts, appearance settings,
          notifications, wallpaper, idle-inhibit, file/app/print dialogs and the
          rest work on any Wayland compositor. lys-shell implements every interface
          natively, so this enables `xdg.portal`, adds only lys-shell to
          `extraPortals`, routes everything to it via `xdg.portal.config.common`
          (with `mkDefault`, so your own routing wins), and defines the
          `xdg-desktop-portal-lys-shell` user service the frontend activates. No
          xdg-desktop-portal-gtk needed.
        '';
      };
    };

    secrets.enable = lib.mkEnableOption ''
      lys-secrets as the session's Secret Service (`org.freedesktop.secrets`):
      libsecret, Chromium and Electron apps, git's libsecret credential helper
      and the portal's per-app secrets keep their secrets in collections
      encrypted at rest under `$XDG_DATA_HOME/lys-secrets`. The lock screen
      unlocks the login collection with the session password. Only one
      provider can own the name, so this cannot be combined with
      `services.gnome.gnome-keyring.enable`'';

    lock = {
      enable = lib.mkEnableOption ''
        a PAM service for the lys-shell session lock. lys-shell locks the session
        natively via `ext-session-lock-v1` (the `lock` CLI / logind `Lock`
        signal); authenticating the unlock needs a PAM service, which NixOS does
        not provide by default. This declares one named `lock.pamService`'';

      pamService = lib.mkOption {
        type = lib.types.str;
        default = "lys-shell";
        description = ''
          Name of the PAM service created for unlock authentication. Must match
          `login.lock.pam-service` in your lys-shell config (the config default is
          `system-auth`, which exists on Arch/Fedora but not NixOS — point it at
          this service, or set this to a service that already exists).
        '';
      };
    };

    greeter = {
      enable = lib.mkEnableOption ''
        the lys-shell greetd greeter: a login screen that shares the desktop/lock
        theme and lets the user pick a Wayland session. Enables `services.greetd`
        with a kiosk compositor (cage) hosting the greeter; on login it starts
        the selected session (discovered from `greeter.session.dirs`)'';

      package = lib.mkOption {
        type = lib.types.package;
        default = cfg.package;
        defaultText = lib.literalExpression "config.programs.lys-shell.package";
        description = "Package providing the `lys-login` binary.";
      };

      cagePackage = lib.mkOption {
        type = lib.types.package;
        default = pkgs.cage;
        defaultText = lib.literalExpression "pkgs.cage";
        description = "Kiosk compositor that hosts the greeter under greetd.";
      };

      renderer = lib.mkOption {
        type = lib.types.enum [
          "auto"
          "software"
        ];
        default = "auto";
        description = ''
          Render path for the greeter stack.

          `auto` sets nothing: wlroots (cage) tries its GLES2 renderer and GTK
          tries its GL renderer, each falling back to software on failure — so
          you get acceleration where a GPU driver is present and software
          otherwise. `software` forces both onto their software paths (wlroots
          pixman + GTK cairo), which needs no GPU driver at all. Use `software`
          on VMs, headless-ish boxes, or any host where GL init is unreliable and
          you would rather guarantee the login screen renders.
        '';
      };

      graphicsWrapper = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        example = lib.literalExpression ''[ "''${pkgs.nixgl.nixGLIntel}/bin/nixGLIntel" ]'';
        description = ''
          Command prefix wrapping the whole greeter launch (cage + greeter).
          Unused on NixOS, where drivers come from {file}`/run/opengl-driver`.
          Its purpose is nixGL on non-NixOS hosts: set this to a nixGL wrapper so
          cage — and the greeter it spawns — find the host's GPU driver and run
          accelerated. Leave empty (with `renderer = "software"`) to skip GL
          entirely instead.
        '';
      };

      session = {
        dirs = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [ "${config.services.displayManager.sessionData.desktops}/share/wayland-sessions" ];
          defaultText = lib.literalExpression ''["''${config.services.displayManager.sessionData.desktops}/share/wayland-sessions"]'';
          description = ''
            Directories scanned for `*.desktop` Wayland session files to offer in
            the picker. Defaults to the aggregate NixOS sessions directory, which
            collects the session files of every installed Wayland compositor.
          '';
        };

        x11Dirs = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [ "${config.services.displayManager.sessionData.desktops}/share/xsessions" ];
          defaultText = lib.literalExpression ''["''${config.services.displayManager.sessionData.desktops}/share/xsessions"]'';
          description = ''
            Directories scanned for `*.desktop` X11 session files, offered with
            an "(X11)" label and launched through `startx` (greetd does not
            manage an X server itself, so `startx` must be on the session PATH —
            typically via `services.xserver.enable`). Set to `[]` to offer
            Wayland sessions only.
          '';
        };

        command = lib.mkOption {
          type = lib.types.str;
          default = "";
          example = lib.literalExpression ''"''${pkgs.niri}/bin/niri --session"'';
          description = ''
            Optional explicit fallback session, offered as a "Custom" entry in
            the picker (and the only session when `session.dirs` yields none).
            Usually unnecessary — installed compositors provide their own session
            files automatically.
          '';
        };

        environment = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [ ];
          example = [ "XDG_SESSION_TYPE=wayland" ];
          description = "Extra `KEY=value` environment entries for the session.";
        };
      };

      settings = lib.mkOption {
        type = yamlFormat.type;
        default = { };
        example = lib.literalExpression ''
          {
            styling.appearance = "dark";
            login.background-mode = "color";
            login.background-color = "#1e1e2e";
          }
        '';
        description = ''
          Theme/background/clock config written to {file}`/etc/lys-shell/config.yaml`
          and read by the greeter. Uses the full lys-shell config schema; the
          greeter honours `styling`, `login.*` (background, blur, clock, and
          `login.greeter` for the cursor, user list and power buttons), and
          `wallpaper`. Leave empty to use the
          built-in defaults.
        '';
      };
    };
  };

  config = lib.mkIf cfg.enable (lib.mkMerge [
    {
      environment.systemPackages = [ cfg.package ];

      systemd.user.services.lys-shell = lib.mkIf cfg.systemd.enable {
        description = "lys-shell desktop shell";
        partOf = [ cfg.systemd.target ];
        # The recorder captures via the xdg-desktop-portal ScreenCast interface
        # over PipeWire; order after (and weakly want) both so they're up when
        # the shell starts. Wants is weak: a missing unit just logs.
        after = [
          cfg.systemd.target
          "pipewire.service"
          "xdg-desktop-portal.service"
        ];
        wants = [
          "pipewire.service"
          "xdg-desktop-portal.service"
        ];
        wantedBy = [ cfg.systemd.target ];
        serviceConfig = {
          ExecStart = "${lib.getExe cfg.package} shell";
          Restart = "on-failure";
          RestartSec = 3;
          Slice = "session.slice";
        };
      };
    }

    (lib.mkIf cfg.portal.enable {
      xdg.portal = {
        enable = true;
        # lys-shell implements every interface natively — no xdg-desktop-portal-gtk.
        extraPortals = [ cfg.package ];
        # Route everything to lys-shell regardless of XDG_CURRENT_DESKTOP. mkDefault
        # so a user's own xdg.portal.config wins.
        config.common = lib.mkDefault {
          default = [ "lys-shell" ];
        };
      };

      # The frontend D-Bus-activates this via the .portal's DBusName /
      # SystemdService; it is not wantedBy the session (started on demand).
      #
      # MUST NOT be After/Requires xdg-desktop-portal.service: the frontend
      # activates this backend (StartServiceByName) while it is still itself
      # activating, so ordering the backend after the frontend deadlocks both
      # — the frontend blocks on the backend's interfaces, the backend blocks
      # on the frontend finishing — until 25s D-Bus timeouts break it and the
      # frontend fails. graphical-session.target ordering is enough.
      systemd.user.services.xdg-desktop-portal-lys-shell = {
        description = "lys-shell xdg-desktop-portal backend";
        partOf = [ "graphical-session.target" ];
        after = [ "graphical-session.target" ];
        serviceConfig = {
          Type = "dbus";
          BusName = "org.freedesktop.impl.portal.desktop.lys";
          ExecStart = "${lib.getExe cfg.package} portal";
          Restart = "on-failure";
          RestartSec = 3;
          Slice = "session.slice";
        };
      };
    })

    (lib.mkIf cfg.secrets.enable {
      assertions = [
        {
          assertion = !config.services.gnome.gnome-keyring.enable;
          message = ''
            programs.lys-shell.secrets.enable and services.gnome.gnome-keyring.enable
            both provide org.freedesktop.secrets; enable one of them.
          '';
        }
      ];
      # The activation file clients start lys-secrets through, handing it to
      # the user unit below.
      services.dbus.packages = [
        (pkgs.runCommand "lys-secrets-dbus" { } ''
          install -Dm0644 ${cfg.package}/share/lys-shell/org.freedesktop.secrets.service \
            $out/share/dbus-1/services/org.freedesktop.secrets.service
        '')
      ];
      systemd.user.services.lys-secrets = {
        description = "lys-secrets Secret Service provider";
        wantedBy = [ "graphical-session.target" ];
        partOf = [ "graphical-session.target" ];
        after = [ "graphical-session.target" ];
        serviceConfig = {
          Type = "dbus";
          BusName = "org.freedesktop.secrets";
          ExecStart = lib.getExe' cfg.package "lys-secrets";
          Restart = "on-failure";
          RestartSec = 3;
          Slice = "session.slice";
        };
      };
    })

    (lib.mkIf cfg.lock.enable {
      # The lock surface authenticates the unlock against this PAM service.
      # The NixOS default stack (pam_unix password auth) is what an unlock
      # needs; enableGnomeKeyring additionally forwards the password to a
      # running gnome-keyring-daemon so unlocking the session also unlocks the
      # `login` keyring. That matters on autologin setups (e.g. greetd
      # initial_session straight into a locked compositor): no password is ever
      # typed at login, so the lys-shell unlock is the only PAM interaction that
      # can unlock the keyring — without this, secret-service clients (Chrome,
      # ...) prompt for the keyring password on first use every session. The
      # lines are `optional`, so with no keyring daemon running they no-op.
      # With `secrets.enable` lys-shell hands the password to lys-secrets
      # itself after the unlock, and the gnome-keyring lines stay unused.
      # The user's lys-shell config `login.lock.pam-service` must name this same service.
      security.pam.services.${cfg.lock.pamService}.enableGnomeKeyring = !cfg.secrets.enable;
    })

    (lib.mkIf cfg.greeter.enable {
      # greetd runs the kiosk compositor + greeter as the unprivileged greeter
      # user. mkDefault so an explicit greetd config wins.
      services.greetd = {
        enable = true;
        settings.default_session.command = lib.mkDefault greeterCommand;
      };

      # Persist the last-selected session. Owned by the greetd user (`greeter`)
      # so the greeter can write it; 0700 since only that user needs it.
      systemd.tmpfiles.rules = [
        "d ${greeterStateDir} 0700 greeter greeter -"
      ];

      # The greeter runs as the greetd user with no $HOME, so its theme config
      # lives system-wide. environment.etc files are world-readable (0444).
      environment.etc."lys-shell/config.yaml" = lib.mkIf (cfg.greeter.settings != { }) {
        source = yamlFormat.generate "lys-greeter-config.yaml" cfg.greeter.settings;
      };

      # Let any local user push their greeter (login-screen) choices from
      # lys-settings to the system config via `pkexec lys-login
      # apply-config` (writes only greeter keys to /etc/lys-shell/runtime.yaml,
      # which the greeter overlays on config.yaml). Admin auth is required.
      security.polkit.enable = true;
      environment.etc."polkit-1/actions/dev.stubbe.lys.greeter.policy".source =
        "${cfg.greeter.package}/share/polkit-1/actions/dev.stubbe.lys.greeter.policy";

      environment.systemPackages = [ cfg.greeter.cagePackage ];
    })

    (lib.mkIf cfg.autoInstallDependencies {
      services.upower.enable = lib.mkDefault true;
      hardware.bluetooth.enable = lib.mkDefault true;

      # GeoClue backs the night light auto-schedule's location lookup. The
      # appConfig entry whitelists lys-shell (DesktopId "lys-shell") so the daemon
      # serves it without an agent prompt; isSystem skips the per-user agent.
      # If GeoClue is unavailable the schedule falls back to the configured
      # latitude/longitude, so this is purely a convenience.
      services.geoclue2 = {
        enable = lib.mkDefault true;
        appConfig.lys-shell = {
          desktopID = "lys-shell";
          isAllowed = true;
          isSystem = true;
        };
      };

      environment.systemPackages =
        lib.optional (pkgs ? wl-clipboard) pkgs.wl-clipboard
        ++ lib.optional (pkgs ? xdg-utils) pkgs.xdg-utils;
    })
  ]);
}
