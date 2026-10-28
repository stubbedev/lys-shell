self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.lys-shell;
in
{
  imports = [
    (lib.mkRemovedOptionModule [ "programs" "lys-shell" "secrets" "enable" ] ''
      lys-secrets moved to its own flake, github:stubbedev/lys-secrets: import
      its nixosModules.default and set `services.lys-secrets.enable = true;`.
    '')
    (lib.mkRemovedOptionModule [ "programs" "lys-shell" "greeter" ] ''
      lys-login moved to its own flake, github:stubbedev/lys-login: import
      its nixosModules.default and use `programs.lys-login.greeter`.
    '')
    (lib.mkRemovedOptionModule [ "programs" "lys-shell" "lock" ] ''
      lys-login moved to its own flake, github:stubbedev/lys-login: import
      its nixosModules.default and use `programs.lys-login.lock`.
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

  };

  config = lib.mkIf cfg.enable (
    lib.mkMerge [
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
          # NixOS gives a unit a PATH of its own; the shell starts programs
          # from the system profile, `lys-login lock` among them.
          path = [ "/run/current-system/sw" ];
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
    ]
  );
}
