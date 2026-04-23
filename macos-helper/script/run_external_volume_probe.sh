#!/usr/bin/env bash
set -euo pipefail

APP_PATH="${ROUGHDDASH_INSTALL_PATH:-/Applications/RoughdashHelper.app}"

if [[ ! -d "$APP_PATH" ]]; then
  echo "Installed app not found: $APP_PATH" >&2
  echo "Run script/install_dev_app.sh Debug first." >&2
  exit 1
fi

INFO_PLIST="$APP_PATH/Contents/Info.plist"
if [[ ! -f "$INFO_PLIST" ]]; then
  echo "Missing Info.plist at $INFO_PLIST" >&2
  exit 1
fi

APP_GROUP="$(/usr/libexec/PlistBuddy -c 'Print RoughdashAppGroupIdentifier' "$INFO_PLIST" 2>/dev/null || true)"
if [[ -z "$APP_GROUP" ]]; then
  echo "Could not read RoughdashAppGroupIdentifier from $INFO_PLIST" >&2
  exit 1
fi

REPORT_PATH="$HOME/Library/Group Containers/$APP_GROUP/Roughdash/external-volume-probe.json"
previous_mtime=0
if [[ -f "$REPORT_PATH" ]]; then
  previous_mtime="$(stat -f '%m' "$REPORT_PATH")"
fi

pkill -x RoughdashHelper >/dev/null 2>&1 || true
open -na "$APP_PATH" --args --probe-selected-volume

deadline=$((SECONDS + 20))
while (( SECONDS < deadline )); do
  if [[ -f "$REPORT_PATH" ]]; then
    current_mtime="$(stat -f '%m' "$REPORT_PATH")"
    if (( current_mtime > previous_mtime )); then
      echo "Probe report: $REPORT_PATH"
      cat "$REPORT_PATH"
      exit 0
    fi
  fi
  sleep 1
done

echo "Probe report did not update within 20 seconds: $REPORT_PATH" >&2
if [[ -f "$REPORT_PATH" ]]; then
  echo "Latest report:"
  cat "$REPORT_PATH"
fi
exit 1
