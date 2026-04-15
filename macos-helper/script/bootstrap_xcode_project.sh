#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if ! command -v xcodegen >/dev/null 2>&1; then
  echo "xcodegen is required. Install it with: brew install xcodegen" >&2
  exit 1
fi

if [[ ! -f Configuration/Local.xcconfig ]]; then
  cp Configuration/Local.xcconfig.example Configuration/Local.xcconfig
  echo "Created Configuration/Local.xcconfig from the example."
  echo "Edit it with your Apple Developer Team ID, bundle ID prefix, and App Group before signing."
fi

xcodegen generate --spec project.yml
echo "Generated RoughdashHelper.xcodeproj"
