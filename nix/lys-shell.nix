{
  lib,
  buildGo127Module,
  makeWrapper,
  libva,
  pam,
  vulkan-loader,
}:
let
  # The libraries the binaries dlopen through purego: libpam (lock
  # screen), libvulkan (lys-lib's GPU renderer, which falls back to the
  # CPU without it, the portal's zero-copy dmabufs, which fall back to
  # shared memory, and the recorder's capture and H.264 encoding) and
  # libva with libva-drm (the recorder's encoder on a GPU whose Vulkan
  # driver encodes no video, Intel Xe2 among them; the VA driver itself
  # comes from the system's graphics stack). The portal speaks
  # PipeWire's protocol itself and loads no libpipewire.
  libs = [
    libva
    pam
    vulkan-loader
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
buildGo127Module {
  pname = "lys-shell";
  inherit version src;

  # The module cache's hash: every go.mod change (a lys-ui bump) changes
  # it; `just vendor-hash` recomputes it (a stale one is reused, not
  # reported, the derivation being fixed-output).
  vendorHash = "sha256-kSBERyFoWaTYrlpCvqrL07QuO5WYEuN9hxESW62w51Y=";

  subPackages = [
    "cmd/lys-shell"
    "cmd/lys-greeter"
    "cmd/lys-lock"
    "cmd/lys-settings"
  ];

  # lys-ui and the purego bindings are pure Go; cgo stays off, as in the
  # devShell.
  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];

  # The suite runs in `just check` (it needs a compositor and D-Bus).
  doCheck = false;

  nativeBuildInputs = [ makeWrapper ];

  # The dlopened libraries on the loader path.
  postFixup = ''
    for bin in lys-shell lys-greeter lys-lock lys-settings; do
      wrapProgram $out/bin/$bin \
        --prefix LD_LIBRARY_PATH : "${lib.makeLibraryPath libs}"
    done
  '';

  # What the NixOS and home-manager modules read from the package.
  postInstall = ''
    # Handler for the globalprotectcallback: URI scheme (GlobalProtect
    # SAML sign-in answers).
    install -Dm0644 resources/dev.stubbe.lys.vpn-sso-callback.desktop -t $out/share/applications
    install -Dm0644 resources/dev.stubbe.lys.settings.desktop -t $out/share/applications
    install -Dm0644 resources/lys-settings.svg \
      $out/share/icons/hicolor/scalable/apps/lys-settings.svg
    # Polkit action for `pkexec lys-greeter apply-config`.
    install -Dm0644 resources/dev.stubbe.lys.greeter.policy \
      -t $out/share/polkit-1/actions
    install -Dm0644 resources/icons/hicolor/scalable/actions/*.svg \
      -t $out/share/icons/hicolor/scalable/actions
    # Reference copy of the systemd user unit; the modules define their own.
    install -Dm0644 resources/lys-shell.service -t $out/share/lys-shell

    # The xdg-desktop-portal backend: the interface declaration, the D-Bus
    # activation file, and the reference unit and portals.conf.
    install -Dm0644 resources/lys-shell.portal \
      -t $out/share/xdg-desktop-portal/portals
    install -d $out/share/dbus-1/services
    substitute resources/org.freedesktop.impl.portal.desktop.lys.service \
      $out/share/dbus-1/services/org.freedesktop.impl.portal.desktop.lys.service \
      --replace-fail /usr/bin/lys-shell "$out/bin/lys-shell"
    substitute resources/xdg-desktop-portal-lys-shell.service \
      $out/share/lys-shell/xdg-desktop-portal-lys-shell.service \
      --replace-fail /usr/bin/lys-shell "$out/bin/lys-shell"
    install -Dm0644 resources/lys-shell-portals.conf -t $out/share/lys-shell
  '';

  # The dev shell puts the same libraries on its loader path.
  passthru = { inherit libs; };

  meta = {
    description = "Wayland desktop shell";
    homepage = "https://github.com/stubbedev/lys-shell";
    license = lib.licenses.mit;
    mainProgram = "lys-shell";
    platforms = lib.platforms.linux;
  };
}
