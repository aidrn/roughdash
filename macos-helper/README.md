# Roughdash macOS Helper

This is the native macOS helper application for Roughdash sync. It is intentionally separate from the Go backend, React dashboard, and existing command-line helper in the repository root.

The helper uses Apple File Provider for Finder integration and targets a dedicated APFS encrypted external SSD. The initial source tree is split into app, File Provider extension, File Provider UI extension, and shared core modules.

## Targets

- `RoughdashHelperApp`: SwiftUI app for pairing, volume setup, sync project selection, transfer status, and conflict management.
- `RoughdashFileProviderExtension`: `NSFileProviderReplicatedExtension` implementation for placeholders, enumeration, hydration, and change reporting.
- `RoughdashFileProviderUI`: File Provider UI extension for conflicts, authentication, and custom actions.
- `RoughdashHelperCore`: shared API client, models, transfer client, volume validator, local catalog, and mapping helpers.

## Development Notes

Use `script/build_and_run.sh` for a fast Swift package compile. It writes build artifacts to `/tmp` by default because NAS-mounted `.build` directories can make Swift module cache renames fail.

```sh
script/build_and_run.sh
```

Use XcodeGen for the signed app and extension bundle project:

```sh
script/check_macos_prereqs.sh
script/bootstrap_xcode_project.sh
```

The bootstrap script creates `Configuration/Local.xcconfig` on first run. Edit that file with:

- `ROUGHDDASH_DEVELOPMENT_TEAM`: your Apple Developer Team ID.
- `ROUGHDDASH_BUNDLE_ID_PREFIX`: a bundle ID prefix you can sign, for example `com.yourname.roughdash.helper`.
- `ROUGHDDASH_APP_GROUP_IDENTIFIER`: the App Group shared by the app and both extensions, for example `group.com.yourname.roughdash.helper`.

After editing the local config, build the generated project:

```sh
script/build_xcode.sh Debug
```

To verify source and project structure before signing is configured:

```sh
ROUGHDDASH_SKIP_SIGNING=1 script/build_xcode.sh Debug
```

To let Xcode create/update development provisioning profiles from the command line after signing is configured:

```sh
ROUGHDDASH_ALLOW_PROVISIONING_UPDATES=1 script/build_xcode.sh Debug
```

For File Provider runtime testing, install the signed dev app into `/Applications` so Launch Services and PlugInKit can discover the embedded extensions:

```sh
ROUGHDDASH_ALLOW_PROVISIONING_UPDATES=1 script/build_xcode.sh Debug
script/install_dev_app.sh Debug
open /Applications/RoughdashHelper.app
```

Select the SSD using the app's `Choose Volume` button, then click `Register File Provider Domain`. Passing a `/Volumes/...` path on the command line is not enough for sandboxed File Provider access; macOS grants the external-volume permission through the file picker.

If macOS returns `NSFeatureUnsupportedError` for an external-volume domain, the helper falls back to a standard replicated File Provider domain for local smoke testing. That fallback verifies app, extension, enumeration, and Finder integration, but it is not the final v1 behavior because the user-visible domain is not stored on the external SSD.

Open `RoughdashHelper.xcodeproj` in Xcode only after generation. Ensure the app, File Provider extension, and File Provider UI extension targets all use the same development team and App Group. Xcode is still required for signing, extension embedding, and File Provider runtime testing.

To validate the SSD before File Provider testing, pass the mounted volume path:

```sh
script/check_macos_prereqs.sh /Volumes/RoughdashSSD
```
