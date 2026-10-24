#!/usr/bin/env bash
# Stages the built binaries and their resources into the release archive
# wayle-<version>-<arch>-linux.tar.gz.
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

STAGING="wayle-${VERSION}-${ARCH}-linux"
rm -rf "${STAGING}"
mkdir -p "${STAGING}/icons" "${STAGING}/completions"

for bin in wayle wayle-greeter wayle-lock wayle-settings; do
	cp "${BIN_DIR}/${bin}" "${STAGING}/"
done
"${BIN_DIR}/wayle" completions bash >"${STAGING}/completions/completions.bash"
"${BIN_DIR}/wayle" completions zsh >"${STAGING}/completions/_wayle"
"${BIN_DIR}/wayle" completions fish >"${STAGING}/completions/wayle.fish"

cp LICENSE "${STAGING}/"
cp -r resources/icons/hicolor "${STAGING}/icons/"
cp resources/wayle.service \
	resources/com.wayle.settings.desktop \
	resources/com.wayle.vpn-sso-callback.desktop \
	resources/wayle-settings.svg \
	resources/dev.stubbe.wayle.greeter.policy \
	resources/wayle.portal \
	resources/org.freedesktop.impl.portal.desktop.wayle.service \
	resources/xdg-desktop-portal-wayle.service \
	resources/wayle-portals.conf \
	"${STAGING}/"
tar czf "${STAGING}.tar.gz" "${STAGING}"
