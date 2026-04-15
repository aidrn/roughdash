#!/usr/bin/env bash
set -euo pipefail

if ! command -v sw_vers >/dev/null 2>&1; then
  echo "This check must run on macOS." >&2
  exit 1
fi

echo "macOS: $(sw_vers -productVersion) ($(sw_vers -buildVersion))"

if command -v xcodebuild >/dev/null 2>&1; then
  xcodebuild -version
else
  echo "xcodebuild: missing"
fi

if command -v swift >/dev/null 2>&1; then
  swift --version | head -n 1
else
  echo "swift: missing"
fi

if command -v xcodegen >/dev/null 2>&1; then
  echo "xcodegen: $(xcodegen --version)"
else
  echo "xcodegen: missing"
fi

volume="${1:-}"
if [[ -z "$volume" ]]; then
  echo "No SSD path supplied. Pass a mounted volume path to validate it, for example:"
  echo "  script/check_macos_prereqs.sh /Volumes/RoughdashSSD"
  exit 0
fi

if ! command -v diskutil >/dev/null 2>&1; then
  echo "diskutil: missing"
  exit 1
fi

info="$(diskutil info "$volume")"
echo "$info" | sed -n '1,80p'

check() {
  local label="$1"
  local pattern="$2"
  if echo "$info" | grep -Eq "$pattern"; then
    echo "OK: $label"
  else
    echo "FAIL: $label"
    return 1
  fi
}

failed=0
check "volume is APFS" "File System Personality:[[:space:]]+APFS" || failed=1
check "volume is encrypted" "Encrypted:[[:space:]]+Yes|FileVault:[[:space:]]+Yes" || failed=1
check "volume is writable" "(Volume Read-Only|Read-Only Volume):[[:space:]]+No" || failed=1
check "volume is external" "Device Location:[[:space:]]+External" || failed=1

exit "$failed"
