{
  perSystem =
    {
      lib,
      pkgs,
      self',
      ...
    }:
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

          # Headless test sessions: fontconfig reads the generated config
          # (shellHook), sway is the headless compositor, dbus the private
          # test buses (internal/dbustest), and pipewire the private daemon
          # and pw-link for the ScreenCast portal tests (lys-lib's pwtest).
          fontconfig
          sway
          dbus
          pipewire
          # ffprobe, which the recorder tests read their files back with.
          ffmpeg-headless

          inter # config default font-sans
          jetbrains-mono # config default font-mono
          nerd-fonts.jetbrains-mono # the common Nerd Font a user config names
        ];

        # The binaries dlopen these through purego; the ScreenCast tests
        # add libpipewire, whose streams are the reference consumers the
        # portal is checked against.
        LD_LIBRARY_PATH = lib.makeLibraryPath (self'.packages.lys-shell.libs ++ [ pkgs.pipewire ]);

        # The real xdg-desktop-portal frontend the portal contract tests
        # route through to the backend.
        XDG_DESKTOP_PORTAL = "${pkgs.xdg-desktop-portal}/libexec/xdg-desktop-portal";

        # The recorder's GStreamer pipeline finds its plugins via this path.
        GST_PLUGIN_SYSTEM_PATH_1_0 = self'.packages.lys-shell.gstPluginPath;

        # lys-ui is pure Go by design; keep accidental cgo out (`just race`
        # re-enables it per invocation).
        CGO_ENABLED = "0";

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
          '';
      };

      formatter = pkgs.nixfmt;
    };
}
