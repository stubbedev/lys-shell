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
        inputsFrom = [ self'.packages.wayle ];

        packages = with pkgs; [
          # Lint, format and debug (config in .golangci.yml).
          golangci-lint
          gofumpt # the formatter behind `golangci-lint fmt`
          gopls
          delve
          grass-sass # SCSS compiler for `just css`

          just

          # Headless test sessions: fontconfig reads the generated config
          # (shellHook), sway is the headless compositor, dbus the private
          # test buses (internal/dbustest), pipewire the private daemon and
          # pw-link for the ScreenCast producer tests, and libei the C client
          # the EIS server tests drive.
          fontconfig
          sway
          dbus
          pipewire
          libei

          inter # config default font-sans
          jetbrains-mono # config default font-mono
          nerd-fonts.jetbrains-mono # the common Nerd Font a user config names
        ];

        # The binaries dlopen these through purego; the EIS tests add libei.
        LD_LIBRARY_PATH = lib.makeLibraryPath (self'.packages.wayle.libs ++ [ pkgs.libei ]);

        # The recorder's GStreamer pipeline finds its plugins via this path.
        GST_PLUGIN_SYSTEM_PATH_1_0 = self'.packages.wayle.gstPluginPath;

        # gelm is pure Go by design; keep accidental cgo out (`just race`
        # re-enables it per invocation).
        CGO_ENABLED = "0";

        shellHook =
          let
            # The config's defaults (Inter, JetBrains Mono) plus the common
            # Nerd Font, layered over the host fontconfig when one exists:
            # `wayle shell` finds fonts in a bare container or headless
            # session, and sees the same set on a NixOS host as outside the
            # shell.
            extraFontDirs = lib.concatMapStrings (font: "  <dir>${font}/share/fonts</dir>\n") [
              pkgs.inter
              pkgs.jetbrains-mono
              pkgs.nerd-fonts.jetbrains-mono
            ];
          in
          ''
            fontconf="''${TMPDIR:-/tmp}/wayle-fonts.conf"
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
