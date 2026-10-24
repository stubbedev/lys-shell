self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.lys-shell;
  tomlFormat = pkgs.formats.toml { };

  # A self-contained launch wrapper for the greeter: `cage -s -- lys-greeter`
  # with the render env baked in, passing any extra args straight through. Point
  # your (non-NixOS) greetd at this one path — or, for GPU acceleration, wrap it
  # with nixGL: `nixGLIntel lys-greeter-session --config /etc/lys-shell/config.toml
  # --state /var/lib/lys-greeter/last-session`. `renderer = "software"` bakes
  # the driver-free pixman/cairo path so it runs without nixGL at all.
  # cage's own cursor (shown until the greeter maps) follows XCURSOR_*; mirror
  # the greeter's cursor config so both cursors match.
  greeterCursorEnv =
    "XCURSOR_SIZE=${toString (cfg.greeter.settings.greeter.cursor-size or 24)}"
    + lib.optionalString (
      (cfg.greeter.settings.greeter.cursor-theme or "") != ""
    ) " XCURSOR_THEME=${cfg.greeter.settings.greeter.cursor-theme}";

  greeterSession = pkgs.writeShellScriptBin "lys-greeter-session" ''
    exec env ${greeterCursorEnv} ${lib.optionalString (cfg.greeter.renderer == "software")
      "WLR_RENDERER=pixman WLR_DRM_NO_MODIFIERS=1 GSK_RENDERER=cairo GDK_DISABLE=gl "
    }${lib.getExe cfg.greeter.cagePackage} -s -- \
      ${lib.getExe' cfg.package "lys-greeter"} "$@"
  '';

  pkgIfPresent = name: lib.optional (builtins.hasAttr name pkgs) (builtins.getAttr name pkgs);

  # Modules referenced anywhere in the bar layout. Used to install the soft
  # dependencies a module shells out to only when that module is actually used.
  layoutZones = cfg.settings.bar.layout or [ ];
  layoutModules = lib.concatMap (
    zone: (zone.left or [ ]) ++ (zone.center or [ ]) ++ (zone.right or [ ])
  ) layoutZones;
  usesMail = lib.elem "mail" layoutModules;

  # Soft dependencies lys-shell shells out to, selected from the config. Theme
  # providers are spawned for wallpaper-driven color extraction; wl-clipboard
  # and xdg-utils back copy / open-link actions; notmuch backs the mail module.
  # Missing packages are skipped.
  themeProvider = cfg.settings.styling.theme-provider or "lys-shell";
  softDeps =
    pkgIfPresent "wl-clipboard"
    ++ pkgIfPresent "xdg-utils"
    ++ lib.optionals (themeProvider == "matugen") (pkgIfPresent "matugen")
    ++ lib.optionals (themeProvider == "wallust") (pkgIfPresent "wallust")
    ++ lib.optionals (themeProvider == "pywal") (pkgIfPresent "pywal")
    ++ lib.optionals usesMail (pkgIfPresent "notmuch");
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
        Install the user-space soft dependencies lys-shell's config implies
        (theme-provider tools like matugen/wallust/pywal, wl-clipboard and
        xdg-utils, plus notmuch when the `mail` module is in the layout).
        System-level dependencies (NetworkManager, bluez, upower,
        power-profiles-daemon, GeoClue for the hyprsunset auto-schedule,
        pipewire/wireplumber) are not installed by home-manager — enable those
        at the NixOS level.
      '';
    };

    settings = lib.mkOption {
      type = tomlFormat.type;
      default = { };
      example = lib.literalExpression ''
        {
          bar.layout = [
            {
              monitor = "*";
              left = [ "dashboard" ];
              center = [ "clock" ];
              right = [ "battery" "network" "volume" "systray" ];
            }
          ];
          styling.appearance = "dark";
        }
      '';
      description = ''
        lys-shell configuration written to
        {file}`$XDG_CONFIG_HOME/lys-shell/config.toml`. Leave empty to manage the
        file yourself.
      '';
    };

    systemd = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Run `lys-shell shell` from a systemd user service bound to the graphical
          session. Disable if you start the bar from your compositor config.
        '';
      };

      target = lib.mkOption {
        type = lib.types.str;
        default = "graphical-session.target";
        description = "Systemd target the lys-shell user service binds to.";
      };
    };

    portal = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Install lys-shell as the xdg-desktop-portal backend at the user level: the
          `.portal` interface declaration, the D-Bus activation file, a
          `xdg-desktop-portal-lys-shell` user service, and a generic `portals.conf`
          routing every interface to lys-shell (it implements them all natively — no
          xdg-desktop-portal-gtk).

          Off by default: on NixOS use the system module's
          `programs.lys-shell.portal.enable` instead (it routes via
          `xdg.portal.config` without writing a generic `portals.conf`). Enable
          this for non-NixOS / standalone home-manager setups.
        '';
      };
    };

    lock = {
      enable = lib.mkEnableOption ''
        guidance for the lys-shell session lock's PAM service. lys-shell locks the
        session natively via `ext-session-lock-v1`; authenticating the unlock
        needs a PAM service, which home-manager cannot create (PAM lives under
        {file}`/etc/pam.d`, root-owned). Enabling this emits a warning with
        instructions. On NixOS use the system module's `programs.lys-shell.lock`,
        which provisions the service for you'';

      pamService = lib.mkOption {
        type = lib.types.str;
        default = "lys-shell";
        description = ''
          Name of the PAM service the unlock authenticates against. Must match
          `lock.pam-service` in your lys-shell config. The config default is
          `system-auth` (exists on Arch/Fedora); on other distros create
          {file}`/etc/pam.d/<this name>` and point the config at it.
        '';
      };
    };

    greeter = {
      enable = lib.mkEnableOption ''
        lys-shell greeter tooling: installs the kiosk compositor (cage) and writes a
        themed greeter config. NOTE: greetd is a system service home-manager
        cannot manage — on NixOS use the system module's
        `programs.lys-shell.greeter`; elsewhere configure greetd yourself'';

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
          Render path baked into the {command}`lys-greeter-session` wrapper.
          `auto` sets nothing (cage's GLES2 and GTK's GL renderer each fall back
          to software on their own); `software` forces the driver-free pixman +
          cairo path, so the wrapper runs without nixGL on a non-NixOS host. Use
          `auto` together with a nixGL wrapper when you want GPU acceleration.
        '';
      };

      settings = lib.mkOption {
        type = tomlFormat.type;
        default = { };
        example = lib.literalExpression ''
          {
            styling.appearance = "dark";
            lock.background-mode = "color";
            lock.background-color = "#1e1e2e";
          }
        '';
        description = ''
          Theme/background/clock config written to
          {file}`$XDG_CONFIG_HOME/lys-shell/greeter.toml`. The greeter runs as the
          greetd user, so copy this somewhere that user can read (e.g.
          {file}`/etc/lys-shell/config.toml`) and point greetd's command at it:
          {command}`cage -s -- lys-greeter --config /etc/lys-shell/config.toml --state /var/lib/lys-greeter/last-session`.
          The greeter discovers Wayland sessions from the standard
          {file}`wayland-sessions` dirs; add `--sessions DIR` to override.
        '';
      };
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages =
      [ cfg.package ]
      ++ lib.optionals cfg.autoInstallDependencies softDeps
      ++ lib.optionals cfg.greeter.enable [
        cfg.greeter.cagePackage
        greeterSession
      ];

    # greetd is system-level; home-manager can only ship the binary + cage and
    # generate a themed config to copy somewhere the greetd user can read.
    warnings =
      lib.optional cfg.greeter.enable ''
        programs.lys-shell.greeter (home-manager) installs cage + the
        `lys-greeter-session` wrapper, but cannot configure greetd (a system
        service). On NixOS use programs.lys-shell.greeter in the system module;
        otherwise enable greetd yourself and point its command at the wrapper:
          lys-greeter-session --config /etc/lys-shell/config.toml --state /var/lib/lys-greeter/last-session
        For GPU acceleration on a non-NixOS host, wrap it with nixGL:
          nixGLIntel lys-greeter-session --config /etc/lys-shell/config.toml --state /var/lib/lys-greeter/last-session
        (or set greeter.renderer = "software" to run driver-free without nixGL).
        The greeter discovers Wayland sessions from the standard
        wayland-sessions dirs and remembers the last pick under --state (make
        that dir writable by the greetd user). It runs as the greetd user, so
        place the config where that user can read it (e.g. /etc/lys-shell/config.toml).
      ''
      # PAM is root-owned (/etc/pam.d); home-manager cannot create it. Point the
      # consumer at the manual step the system module does automatically.
      ++ lib.optional cfg.lock.enable ''
        programs.lys-shell.lock (home-manager) cannot create the PAM service the
        unlock authenticates against (/etc/pam.d is root-owned). Create
        /etc/pam.d/${cfg.lock.pamService} yourself — this stack authenticates
        the unlock AND unlocks the GNOME `login` keyring (pam_unix must be
        `required`, not `sufficient`: a sufficient pass short-circuits the
        stack and skips the keyring hook below it; keyring lines are
        `optional`, so without gnome-keyring installed they no-op):
          auth     required   pam_unix.so
          auth     optional   pam_gnome_keyring.so
          account  required   pam_unix.so
          password required   pam_unix.so
          password optional   pam_gnome_keyring.so use_authtok
          session  required   pam_unix.so
          session  optional   pam_gnome_keyring.so auto_start
        (pam_gnome_keyring.so is packaged as libpam-gnome-keyring on
        Debian/Ubuntu, bundled with gnome-keyring on Arch/Fedora.)
        — and set `lock.pam-service = "${cfg.lock.pamService}"` in your lys-shell
        config. On Arch/Fedora the config default `system-auth` already exists;
        set lock.pam-service to that instead and skip this. On NixOS use the
        system module's programs.lys-shell.lock, which provisions it for you.
      '';

    xdg.configFile."lys-shell/greeter.toml" = lib.mkIf (cfg.greeter.enable && cfg.greeter.settings != { }) {
      source = tomlFormat.generate "lys-greeter-config.toml" cfg.greeter.settings;
    };

    xdg.configFile."lys-shell/config.toml" = lib.mkIf (cfg.settings != { }) {
      source = tomlFormat.generate "lys-shell-config.toml" cfg.settings;
    };

    systemd.user.services.lys-shell = lib.mkIf cfg.systemd.enable {
      Unit = {
        Description = "lys-shell desktop shell";
        PartOf = [ cfg.systemd.target ];
        # The recorder captures via the xdg-desktop-portal ScreenCast interface
        # over PipeWire; order after (and weakly want) both so they're up when
        # the shell starts. Wants is weak: a missing unit just logs, never
        # blocks the shell.
        After = [
          cfg.systemd.target
          "pipewire.service"
          "xdg-desktop-portal.service"
        ];
        Wants = [
          "pipewire.service"
          "xdg-desktop-portal.service"
        ];
      };
      Service = {
        ExecStart = "${lib.getExe cfg.package} shell";
        Restart = "on-failure";
        RestartSec = 3;
        Slice = "session.slice";
      };
      Install.WantedBy = [ cfg.systemd.target ];
    };

    # User-level xdg-desktop-portal backend: discovery (.portal), D-Bus
    # activation, routing, and the activated service. The frontend reads these
    # from $XDG_DATA_HOME / $XDG_CONFIG_HOME. Per-key form so these merge with
    # the `lys-shell/config.toml` entry above.
    xdg.dataFile."xdg-desktop-portal/portals/lys-shell.portal" = lib.mkIf cfg.portal.enable {
      source = "${cfg.package}/share/xdg-desktop-portal/portals/lys-shell.portal";
    };
    xdg.dataFile."dbus-1/services/org.freedesktop.impl.portal.desktop.lys.service" =
      lib.mkIf cfg.portal.enable {
        source = "${cfg.package}/share/dbus-1/services/org.freedesktop.impl.portal.desktop.lys.service";
      };
    # Generic fallback config (read when no <desktop>-portals.conf matches).
    xdg.configFile."xdg-desktop-portal/portals.conf" = lib.mkIf cfg.portal.enable {
      source = "${cfg.package}/share/lys-shell/lys-shell-portals.conf";
    };

    systemd.user.services.xdg-desktop-portal-lys-shell = lib.mkIf cfg.portal.enable {
      Unit = {
        Description = "lys-shell xdg-desktop-portal backend";
        PartOf = [ cfg.systemd.target ];
        # MUST NOT be After/Requires xdg-desktop-portal.service: the frontend
        # activates this backend (StartServiceByName) while still activating
        # itself, so ordering the backend after the frontend deadlocks both
        # until 25s D-Bus timeouts break it and the frontend fails. The session
        # target ordering is enough.
        After = [ cfg.systemd.target ];
      };
      # D-Bus-activated by the frontend via the .portal's DBusName; no WantedBy.
      Service = {
        Type = "dbus";
        BusName = "org.freedesktop.impl.portal.desktop.lys";
        ExecStart = "${lib.getExe cfg.package} portal";
        Restart = "on-failure";
        RestartSec = 3;
        Slice = "session.slice";
      };
    };
  };
}
