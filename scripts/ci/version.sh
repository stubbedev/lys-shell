#!/usr/bin/env bash
# Prints the release version internal/version/version.go declares.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
sed -n 's/^const Version = "\(.*\)"$/\1/p' "$SCRIPT_DIR/../../internal/version/version.go"
