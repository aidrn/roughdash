# Roughdash macOS Helper

This is the native macOS helper application for Roughdash sync. It is intentionally separate from the Go backend, React dashboard, and existing command-line helper in the repository root.

The helper uses Apple File Provider for Finder integration and targets a dedicated APFS encrypted external SSD. The initial source tree is split into app, File Provider extension, File Provider UI extension, and shared core modules. Create the Xcode app and extension targets from these folders on macOS, then attach the entitlements and Info.plist templates under `Configuration/`.

## Targets

- `RoughdashHelperApp`: SwiftUI app for pairing, volume setup, sync project selection, transfer status, and conflict management.
- `RoughdashFileProviderExtension`: `NSFileProviderReplicatedExtension` implementation for placeholders, enumeration, hydration, and change reporting.
- `RoughdashFileProviderUI`: File Provider UI extension for conflicts, authentication, and custom actions.
- `RoughdashHelperCore`: shared API client, models, transfer client, volume validator, local catalog, and mapping helpers.

## Development Notes

The `Package.swift` file builds the shared logic and app source shape on macOS, but Xcode is still required to produce signed app and extension bundles. Configure an App Group shared by all targets before running File Provider flows.
