{
  perSystem =
    {
      lib,
      pkgs,
      self',
      ...
    }:
    let
      inherit (self'.packages) wayle wayle-rust;

      # Everything the compiled binaries dlopen: the Rust libs plus the go-test
      # extras (libei for the EIS server tests).
      runtimeLibs = wayle-rust.libs ++ [ pkgs.libei ];

      # One shell for both halves of the rewrite: the Go toolchain go.mod pins
      # (from the package), the gelm lint stack, and the native build env the
      # Rust workspace and its -sys crates need.
      default = pkgs.mkShell {
        inputsFrom = [ wayle ];

        packages = with pkgs; [
          # The Rust toolchain comes from rustup, which honors the repo's
          # rust-toolchain.toml (dated nightly, byte-identical to CI; nixpkgs'
          # rust is behind Cargo.toml's rust-version).
          rustup
          pkg-config
          cmake
          clang
          mold # fast linker — wired in via RUSTFLAGS below
          cargo-nextest # `just test` runner

          # Go lint/format/debug stack (config in .golangci.yml, gelm parity).
          golangci-lint
          gofumpt # the formatter behind `golangci-lint fmt`
          gopls
          delve
          grass-sass # SCSS compiler for `just go-css` (the grass crate wayle-styling builds with)

          just

          # Headless test-session deps: fontconfig reads the generated config
          # (shellHook), sway is the headless compositor for the go-test gate,
          # dbus the private test buses (internal/dbustest), pipewire the
          # private daemon and pw-link for the ScreenCast producer tests, and
          # libei the C client the EIS server tests drive.
          fontconfig
          sway
          dbus
          pipewire
          libei

          inter # config default font-sans
          jetbrains-mono # config default font-mono
          nerd-fonts.jetbrains-mono # the user config's font-sans/font-mono
        ];

        # System libraries linked by the workspace and its -sys crates
        # (gtk4 + layer-shell, gtksourceview5, audio, cava/fftw, udev, …).
        # pkg-config + each buildInput populate PKG_CONFIG_PATH, so
        # `just release-patch` works straight out of the shell.
        buildInputs = wayle-rust.libs;

        # Link with mold via clang instead of the default bfd linker — cuts
        # relink time hard with 591 crates. Mirrors the RUSTFLAGS
        # nix/wayle-rust.nix builds with; -Zthreads (parallel rustc frontend)
        # is nightly-only, so it is set here and never in the package.
        RUSTFLAGS = "-C linker=clang -C link-arg=-fuse-ld=mold -Zthreads=8";

        # bindgen (via the cava build script) needs libclang at runtime.
        LIBCLANG_PATH = "${pkgs.llvmPackages.libclang.lib}/lib";

        # The binaries dlopen GTK/glib/pipewire/gstreamer at runtime, so
        # `just test`, `just run` and the Go tests need these on the loader
        # path — linking alone (via pkg-config) is not enough.
        LD_LIBRARY_PATH = lib.makeLibraryPath runtimeLibs;

        # The recorder's GStreamer pipeline finds its plugins via this path.
        GST_PLUGIN_SYSTEM_PATH_1_0 = lib.makeSearchPath "lib/gstreamer-1.0" wayle-rust.gstPlugins;

        # gelm is pure Go by design; keep accidental cgo out (`just go-race`
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
            # rustup resolves rust-toolchain.toml; put the pinned toolchain's
            # own bin (cargo/rustc/rustfmt/clippy-driver) on PATH.
            export PATH="$(dirname "$(${lib.getExe pkgs.rustup} which cargo)"):$PATH"

            fontconf="''${TMPDIR:-/tmp}/wayle-go-fonts.conf"
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
    in
    {
      devShells = {
        inherit default;

        # `nix develop .#css` — the default shell with WAYLE_DEV=1 exported, so
        # any `cargo run` (shell or wayle-settings) hot-reloads SCSS from
        # crates/wayle-styling/scss/** on save with no restart. For rapid CSS
        # iteration; `just dev-settings` runs a single session the same way.
        css = default.overrideAttrs (old: {
          WAYLE_DEV = "1";
          shellHook = (old.shellHook or "") + ''
            echo "WAYLE_DEV=1 — SCSS hot-reload on. Edit crates/wayle-styling/scss/**, then: cargo run --bin wayle-settings"
          '';
        });
      };

      formatter = pkgs.nixfmt;
    };
}
