#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIGURATION="${1:-Debug}"
DERIVED_DATA="${ROUGHDDASH_DERIVED_DATA:-/tmp/roughdash-helper-derived-data}"
SOURCE_APP="${ROUGHDDASH_APP_PATH:-$DERIVED_DATA/Build/Products/$CONFIGURATION/RoughdashHelper.app}"
DEST_APP="${ROUGHDDASH_INSTALL_PATH:-/Applications/RoughdashHelper.app}"

if [[ ! -d "$SOURCE_APP" ]]; then
  echo "Built app not found: $SOURCE_APP" >&2
  echo "Run script/build_xcode.sh $CONFIGURATION first, or set ROUGHDDASH_APP_PATH." >&2
  exit 1
fi

pkill -x RoughdashHelper >/dev/null 2>&1 || true
rm -rf "$DEST_APP"
ditto "$SOURCE_APP" "$DEST_APP"

/System/Library/Frameworks/CoreServices.framework/Versions/Current/Frameworks/LaunchServices.framework/Versions/Current/Support/lsregister \
  -f -R -trusted "$DEST_APP"

pluginkit -a "$DEST_APP/Contents/PlugIns/RoughdashFileProviderExtension.appex" >/dev/null 2>&1 || true
pluginkit -a "$DEST_APP/Contents/PlugIns/RoughdashFileProviderUI.appex" >/dev/null 2>&1 || true

echo "Installed $DEST_APP"
pluginkit -m -p com.apple.fileprovider-nonui | grep -Ei 'com\.aiden\.roughdash|roughdash' || true
