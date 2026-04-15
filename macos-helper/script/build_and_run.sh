#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v swift >/dev/null 2>&1; then
  echo "swift is required. Build the signed app and extensions from Xcode on macOS." >&2
  exit 1
fi

BUILD_PATH="${ROUGHDDASH_SWIFT_BUILD_PATH:-/tmp/roughdash-helper-build}"
INDEX_PATH="${ROUGHDDASH_SWIFT_INDEX_STORE_PATH:-/tmp/roughdash-helper-index}"
ACTION="${1:-build}"

swift_args=(--build-path "$BUILD_PATH" -Xswiftc -index-store-path -Xswiftc "$INDEX_PATH")

case "$ACTION" in
  build)
    swift build "${swift_args[@]}"
    ;;
  run)
    swift run "${swift_args[@]}" RoughdashHelperApp
    ;;
  *)
    echo "usage: $0 [build|run]" >&2
    exit 2
    ;;
esac
