# Roughdash macOS Helper Requirements

This folder contains the Roughdash macOS helper application. It is separate from the main Roughdash NAS/dashboard application in the repository root. The root app remains the Go backend and React dashboard; this helper is the native macOS client that integrates with Finder and the dedicated Roughdash SSD.

## Product Role

- Pair a Mac with the Roughdash NAS service.
- Register one Roughdash File Provider domain on a dedicated APFS encrypted external SSD.
- Present multiple TrueNAS project roots as top-level folders in the Roughdash domain.
- Provide Finder placeholders, hydrate-on-open, Quick Look download, local edit detection, local eviction, keep-downloaded pins, delete-everywhere actions, and conflict notifications.
- Keep TrueNAS as the canonical revision journal while allowing offline SSD edits that later upload or become explicit conflicts.
- Prefer peer-to-peer speed: LAN direct first, Tailscale direct second, Tailscale peer relay third, DERP relay only as fallback.

## Architecture

- `Sources/RoughdashHelperApp`: SwiftUI macOS app target. Owns pairing, SSD setup, project selection, status, conflicts, settings, and transfer diagnostics.
- `Sources/RoughdashFileProviderExtension`: File Provider extension target. Owns item enumeration, placeholder metadata, hydration, local edit/delete reporting, and eviction state.
- `Sources/RoughdashFileProviderUI`: File Provider UI/action extension target. Owns authentication, conflict, and custom action presentation.
- `Sources/RoughdashHelperCore`: shared models, API client, local catalog, transfer client, volume validation, and File Provider mapping.
- `Configuration`: entitlements and Info.plist templates for the app and extensions.

## Required Behavior

- The selected SSD must be external, writable, APFS, encrypted, and eligible for File Provider domains. Reject unsupported volumes with a specific reason.
- The user-facing sync location is a Roughdash domain/folder on the SSD, not the entire drive.
- Multiple projects are v1 behavior. Each Roughdash sync project appears as a top-level folder in the single File Provider domain.
- Only one Mac may actively sync a given SSD. Acquire a NAS-backed lease keyed by SSD volume UUID and helper device ID before syncing.
- Normal Finder delete follows cloud semantics and creates a remote tombstone. The helper must also expose an explicit Delete Everywhere action for clarity.
- Remove Download may evict only clean local content. Dirty local files must remain materialized until uploaded or conflict-resolved.
- Keep Downloaded creates a pin. Folder pins recurse; file pins apply only to the selected file.
- Offline edits upload only if the NAS revision still matches the local base revision. If NAS and SSD both changed, create a conflict and keep both versions available.
- Default ignores exclude only system/local junk: `.DS_Store`, `.Spotlight-V100`, `.Trashes`, `.fseventsd`, Roughdash metadata, temporary/partial files, and editor swap files. Do not ignore `.git`, `node_modules`, media libraries, app packages, or project directories by default.

## API Contract

- Use the Roughdash sync REST endpoints under `/api/sync/*`.
- User-session auth is acceptable for setup flows. Helper sync calls should use `Authorization: Bearer <helper-token>` and `X-Roughdash-Helper-ID: <helper-id>`.
- All item paths sent to Roughdash are project-relative POSIX paths. Never send absolute SSD paths as catalog item paths.
- Uploads use chunked transfer sessions: create transfer, PUT chunks, then complete. Each completed upload must be hash-verified.
- Surface connection mode exactly as one of: `LAN direct`, `Tailscale direct`, `peer relay`, or `DERP relay`.

## Implementation Rules

- Keep File Provider code thin. It should translate Finder/File Provider requests into local catalog/API/transfer operations, not contain business policy.
- Keep all durable local helper state in the configured App Group container and `.roughdash/` SSD metadata.
- Do not silently overwrite local dirty content or NAS changes.
- Do not build custom filesystem behavior outside File Provider.
- Do not add Windows support in this helper.
- Keep Swift files focused by responsibility. Avoid a monolithic `ContentView` or one-file app.

## Test Expectations

- Register a File Provider domain on an APFS encrypted external SSD.
- Show multiple placeholder project roots in Finder.
- Hydrate files on open and Quick Look.
- Edit offline, unplug/replug, and detect local changes.
- Evict a clean file and keep the placeholder.
- Reject eviction for dirty local files.
- Acquire a lease for one Mac and reject another Mac until forced/stale takeover.
- Create conflicts when NAS and SSD revisions both changed from the same base.
- Prefer LAN direct over Tailscale when both are reachable.
