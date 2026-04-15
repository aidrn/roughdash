// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "RoughdashHelper",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "RoughdashHelperCore", targets: ["RoughdashHelperCore"]),
        .executable(name: "RoughdashHelperApp", targets: ["RoughdashHelperApp"])
    ],
    targets: [
        .target(name: "RoughdashHelperCore"),
        .executableTarget(
            name: "RoughdashHelperApp",
            dependencies: ["RoughdashHelperCore"]
        ),
        .target(
            name: "RoughdashFileProviderExtension",
            dependencies: ["RoughdashHelperCore"]
        ),
        .target(
            name: "RoughdashFileProviderUI",
            dependencies: ["RoughdashHelperCore"]
        )
    ]
)
