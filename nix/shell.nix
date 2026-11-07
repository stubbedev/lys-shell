{ inputs, ... }:
{
  perSystem =
    {
      lib,
      pkgs,
      self',
      ...
    }:
    let
      # Vulkan and VA-API drivers off NixOS, as lys-lib's and lys-ui's
      # shells set them up: without it the GPU tests and sway's renderer
      # find no driver there.
      gpuDrivers = inputs.lys-lib.lib.gpuDriverEnv pkgs;
    in
    {
      devShells.default = pkgs.mkShell {
        # The Go toolchain go.mod pins, from the package.
        inputsFrom = [ self'.packages.lys-shell ];

        packages = with pkgs; [
          # Lint, format and debug (config in .golangci.yml).
          golangci-lint
          gofumpt # the formatter behind `golangci-lint fmt`
          gopls
          delve

          just
          git
          gh
          "${inputs.lys-ci}" # no-fused

          # Headless test sessions: fontconfig reads the generated config
          # (shellHook), sway is the headless compositor, dbus the private
          # test buses (lys-lib's dbustest), and pipewire the private daemon
          # and pw-link for the ScreenCast portal tests (lys-lib's pwtest).
          fontconfig
          sway
          dbus
          pipewire
          # loginctl, which the default lock commands run against the fake
          # logind on a private system bus (service/logind/logindtest).
          systemd
          # ffprobe, which the recorder tests read their files back with.
          ffmpeg-headless
          # The compositor the minimized-windows module and dock tests run on
          # headless (service/lyswm/lyswmtest.Headless).
          inputs.lys-wm.packages.${pkgs.stdenv.hostPlatform.system}.default
          # wl-paste, the out-of-process data-control reader the screenshot
          # clipboard tests read the selection back with.
          wl-clipboard
          # bwrap, the sandbox the file chooser runs thumbnailers in.
          bubblewrap
          # GTK 3 and GTK 4 apps, through PyGObject, printing through the
          # real portal to the print dialog (portal's GTK print tests).
          (python3.withPackages (ps: [ ps.pygobject3 ]))
          gobject-introspection
          gtk3
          gtk4
          inter # config default font-sans
          jetbrains-mono # config default font-mono
          nerd-fonts.jetbrains-mono # the common Nerd Font a user config names
        ];

        # The binaries dlopen these through purego, with the GPU driver
        # setup's own; the ScreenCast tests add libpipewire, whose streams
        # are the reference consumers the portal is checked against, and the
        # RemoteDesktop and InputCapture tests libei, the reference EIS client.
        LD_LIBRARY_PATH = lib.makeLibraryPath (
          lib.unique (gpuDrivers.libraries ++ self'.packages.lys-shell.libs ++ [ pkgs.pipewire pkgs.libei ])
        );

        # The real xdg-desktop-portal frontend the portal contract tests
        # route through to the backend.
        XDG_DESKTOP_PORTAL = "${pkgs.xdg-desktop-portal}/libexec/xdg-desktop-portal";
        LYS_SHELL_TEST_EMOJI_FONT = "${pkgs.noto-fonts-color-emoji}/share/fonts/noto/NotoColorEmoji.ttf";
        XKB_CONFIG_ROOT = "${pkgs.xkeyboard-config}/share/X11/xkb";
        XLOCALEDIR = "${pkgs.libX11}/share/X11/locale";

        # lys-ui is pure Go by design; keep accidental cgo out (`just race`
        # re-enables it per invocation).
        CGO_ENABLED = "0";
        GOTOOLCHAIN = "local";

        shellHook =
          let
            # The config's defaults (Inter, JetBrains Mono) plus the common
            # Nerd Font, layered over the host fontconfig when one exists:
            # `lys-shell shell` finds fonts in a bare container or headless
            # session, and sees the same set on a NixOS host as outside the
            # shell.
            extraFontDirs = lib.concatMapStrings (font: "  <dir>${font}/share/fonts</dir>\n") [
              pkgs.inter
              pkgs.jetbrains-mono
              pkgs.nerd-fonts.jetbrains-mono
            ];
          in
          ''
            fontconf="''${TMPDIR:-/tmp}/lys-shell-fonts.conf"
            cat > "$fontconf" <<EOF
            <?xml version="1.0"?>
            <!DOCTYPE fontconfig SYSTEM "fonts.dtd">
            <fontconfig>
              <include ignore_missing="yes">/etc/fonts/fonts.conf</include>
            ${extraFontDirs}  <cachedir>''${XDG_CACHE_HOME:-$HOME/.cache}/fontconfig</cachedir>
            </fontconfig>
            EOF
            export FONTCONFIG_FILE="$fontconf"
          ''
          + gpuDrivers.shellHook;
      };

      formatter = pkgs.nixfmt;
    };
}
