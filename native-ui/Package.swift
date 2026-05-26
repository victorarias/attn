// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "attn-native",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "attn-native", targets: ["AttnNative"]),
    ],
    targets: [
        .binaryTarget(
            name: "GhosttyKit",
            path: "Vendor/GhosttyKit.xcframework"
        ),
        .executableTarget(
            name: "AttnNative",
            dependencies: ["GhosttyKit"],
            path: "Sources/AttnNative",
            linkerSettings: [
                .linkedLibrary("c++"),
                .linkedFramework("AppKit"),
                .linkedFramework("Carbon"),
                .linkedFramework("CoreFoundation"),
                .linkedFramework("CoreGraphics"),
                .linkedFramework("CoreText"),
                .linkedFramework("CoreVideo"),
                .linkedFramework("Foundation"),
                .linkedFramework("IOSurface"),
                .linkedFramework("Metal"),
                .linkedFramework("Network"),
                .linkedFramework("QuartzCore"),
                .linkedFramework("Security"),
            ]
        ),
        .testTarget(
            name: "AttnNativeTests",
            dependencies: ["AttnNative"],
            path: "Tests/AttnNativeTests"
        ),
    ]
)
