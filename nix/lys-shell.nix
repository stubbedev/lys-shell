{
  lib,
  buildGo127Module,
  makeWrapper,
  libX11,
  xkeyboard-config,
  libva,
  vulkan-loader,
  bubblewrap,
}:
let
  # The libraries the binaries dlopen through purego: libvulkan (lys-lib's GPU renderer, which falls back to the
  # CPU without it, the portal's zero-copy dmabufs, which fall back to
  # shared memory, and the recorder's capture and H.264 encoding) and
  # libva with libva-drm (the recorder's encoder on a GPU whose Vulkan
  # driver encodes no video, Intel Xe2 among them; the VA driver itself
  # comes from the system's graphics stack). The portal speaks
  # PipeWire's protocol itself and loads no libpipewire.
  libs = [
    libva
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

  nonGoDirs = [
    "contrib"
    "docs"
    "nix"
    "packaging"
    "schema"
    "scripts"
    "tests"
  ];
  goDirs = lib.attrNames (
    lib.filterAttrs (
      name: type: type == "directory" && !lib.hasPrefix "." name && !lib.elem name nonGoDirs
    ) (builtins.readDir ../.)
  );

  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions (
      [
        ../go.mod
        ../go.sum
      ]
      ++ map (dir: ../. + "/${dir}") goDirs
    );
  };

  # The binaries that draw through lys-ui and dlopen the GPU and media
  # libraries.
  wrapped = [
    "lys-shell"
    "lys-lock"
  ];
in
buildGo127Module {
  pname = "lys-shell";
  inherit version src;

  # The vendored modules' hash: a go.mod change (a lys-ui bump) and the
  # first import of a package not vendored yet both change it, since only
  # imported packages are vendored; `just vendor-hash` recomputes it (a
  # stale one is reused, not reported, the derivation being fixed-output).
  vendorHash = "sha256-6c1f+gkEl7/hI7tOaPr9CKLJC8lvmoXo7zcNNhuI2yA=";

  subPackages = [
    "cmd/lys-shell"
    "cmd/lys-lock"
  ];

  # lys-ui and the purego bindings are pure Go; cgo stays off, as in the
  # devShell.
  env.CGO_ENABLED = "0";
  ldflags = [
    "-s"
    "-w"
    "-X github.com/stubbedev/lys-lib/xkb.defaultRoot=${xkeyboard-config}/share/X11/xkb"
    "-X github.com/stubbedev/lys-lib/xkb.defaultXLocaleDir=${libX11}/share/X11/locale"
  ];

  # The suite runs in `just check` (it needs a compositor and D-Bus).
  doCheck = false;

  nativeBuildInputs = [ makeWrapper ];

  # The dlopened libraries on the loader path, and bwrap after the
  # system's own on PATH: the sandbox the file chooser runs installed
  # thumbnailers in, which it refuses to run without.
  postFixup = ''
    for bin in ${lib.concatStringsSep " " wrapped}; do
      wrapProgram $out/bin/$bin \
        --prefix LD_LIBRARY_PATH : "${lib.makeLibraryPath libs}" \
        --suffix PATH : "${lib.makeBinPath [ bubblewrap ]}"
    done
  '';

  # What the NixOS and home-manager modules read from the package.
  postInstall = ''
    # Handler for the globalprotectcallback: URI scheme (GlobalProtect
    # SAML sign-in answers).
    install -Dm0644 resources/dev.stubbe.lys.vpn-sso-callback.desktop -t $out/share/applications
    go tool install-icons $out/share/icons/hicolor/scalable/actions
    # Reference copy of the systemd user unit; the modules define their own.
    install -Dm0644 resources/lys-shell.service -t $out/share/lys-shell

    # The xdg-desktop-portal backend: the interface declaration, the D-Bus
    # activation file, and the reference unit and portals.conf.
    install -Dm0644 resources/lys-shell.portal resources/lys-shell-lys-wm.portal \
      -t $out/share/xdg-desktop-portal/portals
    install -d $out/share/dbus-1/services
    substitute resources/org.freedesktop.impl.portal.desktop.lys.service \
      $out/share/dbus-1/services/org.freedesktop.impl.portal.desktop.lys.service \
      --replace-fail /usr/bin/lys-shell "$out/bin/lys-shell"
    for activation in resources/dev.stubbe.lys.*.service; do
      substitute "$activation" "$out/share/dbus-1/services/$(basename "$activation")" \
        --replace-fail /usr/bin/lys-shell "$out/bin/lys-shell"
    done
    substitute resources/xdg-desktop-portal-lys-shell.service \
      $out/share/lys-shell/xdg-desktop-portal-lys-shell.service \
      --replace-fail /usr/bin/lys-shell "$out/bin/lys-shell"
    install -Dm0644 resources/lys-shell-portals.conf -t $out/share/lys-shell
  '';

  # The dev shell puts the same libraries on its loader path.
  passthru = { inherit libs wrapped; };

  meta = {
    description = "Wayland desktop shell";
    homepage = "https://github.com/stubbedev/lys-shell";
    license = lib.licenses.mit;
    mainProgram = "lys-shell";
    platforms = lib.platforms.linux;
  };
}
