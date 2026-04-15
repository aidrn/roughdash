#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v swift >/dev/null 2>&1; then
  echo "swift is required. Build the signed app and extensions from Xcode on macOS." >&2
  exit 1
fi

swift build
