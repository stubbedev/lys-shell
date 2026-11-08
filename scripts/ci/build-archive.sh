#!/usr/bin/env bash
# Stages the built binaries and their resources into the release archive
# lys-shell-<version>-<arch>-linux.tar.gz.
set -euo pipefail

if [[ $# -ne 3 ]]; then
	echo "Usage: $0 <bin-dir> <version> <arch>" >&2
	exit 1
fi

BIN_DIR="$1"
VERSION="$2"
ARCH="$3"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_DIR"

STAGING="lys-shell-${VERSION}-${ARCH}-linux"
rm -rf "${STAGING}"
mkdir -p "${STAGING}/icons" "${STAGING}/completions"

for bin in lys-shell lys-lock; do
	cp "${BIN_DIR}/${bin}" "${STAGING}/"
done
cp "${BIN_DIR}/completions/completions.bash" "${BIN_DIR}/completions/_lys-shell" "${BIN_DIR}/completions/lys-shell.fish" "${STAGING}/completions/"

cp LICENSE "${BIN_DIR}/eff_large_wordlist.NOTICE" "${STAGING}/"
cp -R resources/icons/hicolor "${STAGING}/icons/"
cp resources/lys-shell.service \
	resources/dev.stubbe.lys.vpn-sso-callback.desktop \
	resources/lys-shell.portal \
	resources/lys-shell-lys-wm.portal \
	resources/org.freedesktop.impl.portal.desktop.lys.service \
	resources/dev.stubbe.lys.*.service \
	resources/xdg-desktop-portal-lys-shell.service \
	resources/lys-shell-portals.conf \
	resources/geoclue-lys-shell.conf \
	"${STAGING}/"
tar czf "${STAGING}.tar.gz" "${STAGING}"
