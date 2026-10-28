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
  imports = [
    (lib.mkRemovedOptionModule [ "programs" "lys-shell" "secrets" "enable" ] ''
      lys-secrets moved to its own flake, github:stubbedev/lys-secrets: import
      its homeManagerModules.default and set `services.lys-secrets.enable = true;`.
    '')
    (lib.mkRemovedOptionModule [ "programs" "lys-shell" "greeter" ] ''
      lys-login moved to its own flake, github:stubbedev/lys-login: import
      its homeManagerModules.default and use `programs.lys-login.greeter`.
    '')
    (lib.mkRemovedOptionModule [ "programs" "lys-shell" "lock" ] ''
      lys-login moved to its own flake, github:stubbedev/lys-login: import
      its homeManagerModules.default and use `programs.lys-login.lock`.
    '')
  ];

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
        power-profiles-daemon, GeoClue for the night light auto-schedule,
        pipewire/wireplumber) are not installed by home-manager — enable those
        at the NixOS level.
      '';
    };

    settings = lib.mkOption {
      type = yamlFormat.type;
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
        {file}`$XDG_CONFIG_HOME/lys-shell/config.yaml`. Leave empty to manage the
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

  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ] ++ lib.optionals cfg.autoInstallDependencies softDeps;

    xdg.configFile."lys-shell/config.yaml" = lib.mkIf (cfg.settings != { }) {
      source = yamlFormat.generate "lys-shell-config.yaml" cfg.settings;
    };

    systemd.user.services.lys-shell = lib.mkIf cfg.systemd.enable {
      Unit = {
        Description = "lys-shell desktop shell";
        PartOf = [ cfg.systemd.target ];
        # D-Bus activation (a Notify before the shell is up) must not start
        # the shell outside the session: with no compositor it exits, and
        # Restart= would loop it. Requisite fails that start at once.
        Requisite = [ cfg.systemd.target ];
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
    # the `lys-shell/config.yaml` entry above.
    xdg.dataFile."xdg-desktop-portal/portals/lys-shell.portal" = lib.mkIf cfg.portal.enable {
      source = "${cfg.package}/share/xdg-desktop-portal/portals/lys-shell.portal";
    };
    xdg.dataFile."dbus-1/services/org.freedesktop.impl.portal.desktop.lys.service" =
      lib.mkIf cfg.portal.enable
        {
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
