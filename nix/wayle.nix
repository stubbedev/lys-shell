{
  lib,
  buildGoModule,
  makeWrapper,
  glib,
  pam,
  pipewire,
  libgbm,
  gst_all_1,
}:
let
  # GStreamer plugins the recorder's pipelines load at runtime
  # (pipewiresrc, v4l2src, x264enc, opusenc, mp4/matroska/webm mux,
  # compositor).
  gstPlugins = (with gst_all_1; [
    gstreamer
    gst-plugins-base
    gst-plugins-good
    gst-plugins-bad
    gst-plugins-ugly
    gst-libav
  ]) ++ [ pipewire ];

  # The libraries the binaries dlopen through purego: libgstreamer and
  # libglib (recorder), libpam (lock screen), libpipewire (the ScreenCast
  # portal) and libgbm (its dmabuf path).
  libs = [
    gst_all_1.gstreamer
    glib
    pam
    pipewire
    libgbm
  ];

  # internal/version is the one place the release version lives.
  version =
    let
      line = lib.findFirst (lib.hasPrefix "const Version = ") (throw "internal/version/version.go declares no Version") (
        lib.splitString "\n" (builtins.readFile ../internal/version/version.go)
      );
    in
    lib.removeSuffix "\"" (lib.removePrefix "const Version = \"" line);

  # Only the Go tree, so editing docs or nix files does not rebuild it.
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../cava
      ../cmd
      ../config
      ../greeter
      ../i18n
      ../internal
      ../portal
      ../resources
      ../service
      ../shell
      ../strftime
      ../styling
      ../third_party
    ];
  };
in
buildGoModule {
  pname = "wayle";
  inherit version src;

  # The module cache's hash: every go.mod change (a gelm bump) changes
  # it; `just go-vendor-hash` recomputes it (a stale one is reused, not
  # reported, the derivation being fixed-output).
  vendorHash = "sha256-51ku3cs4jMBteTn+n0dkvMOaX/SJduKWEGj0gPULtgQ=";

  subPackages = [
    "cmd/wayle"
    "cmd/wayle-greeter"
    "cmd/wayle-lock"
    "cmd/wayle-settings"
  ];

  # gelm and the purego bindings are pure Go; cgo stays off, as in the
  # devShell.
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];

  # The suite runs in `just check` (it needs a compositor and D-Bus).
  doCheck = false;

  nativeBuildInputs = [ makeWrapper ];

  # The dlopened libraries on the loader path, and the recorder's
  # GStreamer plugins on GST_PLUGIN_SYSTEM_PATH_1_0.
  postFixup = ''
    for bin in wayle wayle-greeter wayle-lock wayle-settings; do
      wrapProgram $out/bin/$bin \
        --prefix LD_LIBRARY_PATH : "${lib.makeLibraryPath libs}" \
        --prefix GST_PLUGIN_SYSTEM_PATH_1_0 : "${lib.makeSearchPath "lib/gstreamer-1.0" gstPlugins}"
    done
  '';

  # What the NixOS and home-manager modules read from the package.
  postInstall = ''
    # Handler for the globalprotectcallback: URI scheme (GlobalProtect
    # SAML sign-in answers).
    install -Dm0644 resources/com.wayle.vpn-sso-callback.desktop -t $out/share/applications
    install -Dm0644 resources/com.wayle.settings.desktop -t $out/share/applications
    install -Dm0644 resources/wayle-settings.svg \
      $out/share/icons/hicolor/scalable/apps/wayle-settings.svg
    # Polkit action for `pkexec wayle-greeter apply-config`.
    install -Dm0644 resources/dev.stubbe.wayle.greeter.policy \
      -t $out/share/polkit-1/actions
    install -Dm0644 resources/icons/hicolor/scalable/actions/*.svg \
      -t $out/share/icons/hicolor/scalable/actions
    # Reference copy of the systemd user unit; the modules define their own.
    install -Dm0644 resources/wayle.service -t $out/share/wayle

    # The xdg-desktop-portal backend: the interface declaration, the D-Bus
    # activation file, and the reference unit and portals.conf.
    install -Dm0644 resources/wayle.portal \
      -t $out/share/xdg-desktop-portal/portals
    install -d $out/share/dbus-1/services
    substitute resources/org.freedesktop.impl.portal.desktop.wayle.service \
      $out/share/dbus-1/services/org.freedesktop.impl.portal.desktop.wayle.service \
      --replace-fail /usr/bin/wayle "$out/bin/wayle"
    substitute resources/xdg-desktop-portal-wayle.service \
      $out/share/wayle/xdg-desktop-portal-wayle.service \
      --replace-fail /usr/bin/wayle "$out/bin/wayle"
    install -Dm0644 resources/wayle-portals.conf -t $out/share/wayle
  '';

  # The dev shell puts the same libraries and plugins on its paths.
  passthru = { inherit libs gstPlugins; };

  meta = {
    description = "Wayland desktop shell";
    homepage = "https://github.com/stubbedev/wayle";
    license = lib.licenses.mit;
    mainProgram = "wayle";
    platforms = lib.platforms.linux;
  };
}
