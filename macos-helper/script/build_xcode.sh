#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ ! -d RoughdashHelper.xcodeproj ]]; then
  script/bootstrap_xcode_project.sh
fi

DERIVED_DATA="${ROUGHDDASH_DERIVED_DATA:-/tmp/roughdash-helper-derived-data}"
CONFIGURATION="${1:-Debug}"
extra_args=("")

if [[ "${ROUGHDDASH_SKIP_SIGNING:-0}" == "1" ]]; then
  extra_args+=("CODE_SIGNING_ALLOWED=NO")
fi

if [[ "${ROUGHDDASH_ALLOW_PROVISIONING_UPDATES:-0}" == "1" ]]; then
  extra_args+=("-allowProvisioningUpdates")
fi

xcodebuild \
  -project RoughdashHelper.xcodeproj \
  -scheme RoughdashHelperApp \
  -configuration "$CONFIGURATION" \
  -destination "platform=macOS" \
  -derivedDataPath "$DERIVED_DATA" \
  "${extra_args[@]:1}" \
  build
